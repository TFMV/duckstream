![Logo](assets/logo.svg)

SQL queries registered in DuckDB become long-running streaming queries, with results continuously streamed over QUIC until explicitly stopped.

## Architecture

```
Data → Ingestion → DuckDB → Query Runtime → QUIC → Client
```

- **Ingestion Layer**: HTTP POST endpoint receives JSON data, batch inserts into DuckDB `events` table using DuckDB Appender API
- **Query Runtime**: Manages persistent streaming queries with incremental polling across any table that exposes a cursor column (`id` BIGINT or `created_at` TIMESTAMP)
- **QUIC Transport**: Streams results to connected clients via QUIC
- **Control Surface**: REPL + HTTP API + Web Dashboard for registering/unregistering queries

## Quick Start

```bash
# Terminal 1: Start the server
go run ./cmd/main.go

# Terminal 2: Run the demo client
go run ./cmd/demo

# Terminal 3: Open the dashboard
# Open localhost:8080/frontend/index.html in a browser
```

The server starts:
- QUIC server on `localhost:4242`
- HTTP API on `localhost:8080`
- REPL control surface (stdin)

## HTTP API

```bash
# List queries
curl http://localhost:8080/queries

# Register query
curl -X POST http://localhost:8080/queries \
  -H "Content-Type: application/json" \
  -d '{"id":"q1","sql":"SELECT * FROM events"}'

# Unregister query
curl -X DELETE http://localhost:8080/queries/q1

# Get metrics
curl http://localhost:8080/metrics
```

## Features

### SQL Query Validation
Queries are validated against strict rules at registration time. Invalid SQL is rejected before creating an executor with explicit error messages:

- Queries may target any table (not limited to `events`), as long as table has a valid cursor column:
  - `id` (BIGINT), or
  - `created_at` (TIMESTAMP)

```bash
# Single-table queries only
curl -X POST http://localhost:8080/queries \
  -H "Content-Type: application/json" \
  -d '{"id":"q1","sql":"SELECT * FROM lineitem"}'

# JOINs are rejected
curl -X POST http://localhost:8080/queries \
  -H "Content-Type: application/json" \
  -d '{"id":"bad","sql":"SELECT * FROM lineitem JOIN orders"}'
# Returns: invalid query: JOINs are not supported in streaming queries

# GROUP BY is rejected
curl -X POST http://localhost:8080/queries \
  -H "Content-Type: application/json" \
  -d '{"id":"bad","sql":"SELECT customer_id, COUNT(*) FROM orders GROUP BY customer_id"}'
# Returns: invalid query: GROUP BY is not supported

# Cursor column must exist and be BIGINT or TIMESTAMP
# Valid: id (BIGINT) or created_at (TIMESTAMP)
# Invalid: SELECT * FROM orders WHERE timestamp > X (no cursor column tracking)
```

### Per-Query Metrics Endpoint
The `/metrics` endpoint now returns comprehensive per-query streaming metrics:

```bash
curl http://localhost:8080/metrics | jq .
```

Response includes:
- Global metrics: `queries_registered`, `queries_unregistered`, `rows_sent`, `errors`
- Connection stats: `active_clients`, `active_queries`
- Per-query metrics (array):
  - `id` - query identifier
  - `last_cursor` - current cursor position (int64 or timestamp)
  - `rows_streamed` - rows sent for this query
  - `lag_ms` - milliseconds behind current state (for TIMESTAMP cursor only)

Example per-query metric:
```json
{
  "id": "lineitem_stream",
  "last_cursor": "2026-04-03T12:34:56Z",
  "rows_streamed": 120,
  "lag_ms": 520
}
```

### Start Modes
When registering a query, specify where to start streaming from:

```bash
# Start from the beginning (default)
curl -X POST http://localhost:8080/queries \
  -H "Content-Type: application/json" \
  -d '{"id":"q1","sql":"SELECT * FROM lineitem","mode":"BEGINNING"}'

# Start from now (skip existing rows, only stream new inserts)
curl -X POST http://localhost:8080/queries \
  -H "Content-Type: application/json" \
  -d '{"id":"q1","sql":"SELECT * FROM lineitem","mode":"NOW"}'

# REPL syntax
> REGISTER QUERY q1 AS SELECT * FROM lineitem FROM NOW
> REGISTER QUERY q2 AS SELECT * FROM lineitem WITH CURSOR created_at FROM NOW
```

### Cursor Column Selection
The system automatically detects the cursor column for incremental polling:
1. Uses explicit `cursor` parameter if provided
2. Falls back to `id` column if present
3. Falls back to `created_at` column if present
4. Fails with clear error if neither found

Cursor column must be `BIGINT` or `TIMESTAMP` type.

### Connection Limits (Sane Defaults)
- Max clients: 100
- Max streams per client: 10
- Max queries: 50

### Query-Specific Streams
Each query gets its own QUIC stream - different queries don't share streams.

## Dashboard

Open `frontend/index.html` in a browser for a live dashboard with:
- Query Control Panel - register/unregister queries
- Live Stream Viewer - real-time results for each query
- Event Ingestion Panel - live feed of events
- System Status Bar - connection status, uptime, metrics

![Dashboard](assets/spa.png)

## How It Works

**The Core Concept:** Each registered query polls its source table incrementally, tracking the last cursor position (an auto-incrementing `id` or `created_at` timestamp). New rows matching the query are continuously delivered over QUIC.

**Step by Step:**

1. **Cursor Detection**: When you register a query, the system:
   - Analyzes the table schema
   - Auto-detects cursor column: `id` (int64) or `created_at` (timestamp), or accepts explicit cursor parameter
   - Validates cursor type is BIGINT or TIMESTAMP
   - Fails fast with clear error if no suitable cursor found

2. **Initialization**: Based on `StartMode`:
   - `BEGINNING`: start from cursor value 0 (or epoch for timestamps)
   - `NOW`: find current max cursor value, skip existing rows, stream only new inserts

3. **Polling Loop** (every 100ms):
   - Execute: `SELECT ... WHERE cursor > lastValue ORDER BY cursor ASC LIMIT batchSize`
   - For each row, update cursor position
   - Send batch over QUIC stream
   - At-least-once delivery: only advance cursor after successful send

4. **Per-Query State**:
   - Each executor maintains `StreamState`: cursor position, rows sent, last update time
   - State accessible via `/metrics` endpoint

**Analogy:**
```
table               = append-only log with cursor column
cursor column       = position marker (id or created_at)
query               = subscription with WHERE filter
executor            = background worker that polls incrementally
QUIC stream         = delivery channel to client
```

**Why Cursors:** Polling-based streaming requires a position anchor. Without a cursor column, the system would re-scan the entire table on every poll - inefficient and impractical at scale.

## Configuration

Default configuration in `internal/config/config.go`:
- `DuckDBPath`: `"duckstream.db"`
- `QUICAddr`: `"localhost:4242"`
- `IngestAddr`: `"localhost:8080"`
- `BatchSize`: `100` - Events per batch
- `BatchTimeout`: `1s`
- `PollInterval`: `100ms`
- `MaxClients`: `100`
- `MaxStreamsPerClient`: `10`
- `MaxQueries`: `50`

## REPL Commands

Basic:
- `REGISTER QUERY <id> AS <sql>` - Start streaming from BEGINNING
- `UNREGISTER QUERY <id>` - Stop streaming and remove query
- `LIST QUERIES` - Show active queries
- `HELP` - Show help
- `QUIT` - Exit

Extended syntax (with cursor and start mode):
- `REGISTER QUERY <id> AS <sql> WITH CURSOR <column> FROM NOW`
- `REGISTER QUERY <id> AS <sql> FROM NOW`
- `REGISTER QUERY <id> AS <sql> WITH CURSOR <column>` (defaults to BEGINNING)

Examples:
```
# Simple: stream all rows from lineitem table starting from beginning
> REGISTER QUERY q1 AS SELECT * FROM lineitem

# Skip existing, only new: start from the current max id
> REGISTER QUERY q2 AS SELECT * FROM lineitem FROM NOW

# Explicit cursor: use created_at timestamp instead of auto-detected id
> REGISTER QUERY q3 AS SELECT * FROM lineitem WITH CURSOR created_at

# Combine: use created_at and skip to current time
> REGISTER QUERY q4 AS SELECT * FROM lineitem WITH CURSOR created_at FROM NOW
```

## Behavior

- Tables are append-only signals (events)
- Queries are persistent transformations over those signals
- Execution is a live cursor that never naturally terminates
- QUIC delivers output streams to clients
- Queries run continuously until explicitly unregistered

## Limitations

### Design Constraints

DuckStream enforces strict SQL validation at registration time to ensure queries can be efficiently streamed. Attempts to register an invalid query fail immediately with explicit error messages:

- **`only single-table SELECT queries are supported`** – Multi-table queries are not supported
- **`JOINs are not supported in streaming queries`** – All join types (INNER, LEFT, CROSS, etc.) are rejected
- **`GROUP BY is not supported in streaming queries`** – Aggregation queries are rejected because they re-compute entire results on each poll
- **`DISTINCT is not supported`** – Requires buffering all rows to eliminate duplicates
- **Window functions are not supported** – Complex per-row computations over result sets
- **Subqueries are not supported** – Nested SELECT queries
- **`no valid cursor column found (expected id or created_at)`** – Query must have an auto-incrementing `id` (BIGINT) or `created_at` (TIMESTAMP) column; if neither exists, registration fails
- **`cursor column must be BIGINT or TIMESTAMP`** – Only these types support monotonic cursor tracking

### Cursor Tracking & Latency

Once a query is registered, DuckStream polls the cursor column every 100ms:

- **Polling latency**: 0–100ms delay between insert and delivery (depends on insert timing relative to poll tick)
- **At-least-once semantics**: If connection drops after delivery, cursor is not advanced; rows are re-sent on reconnect
- **Stateful cursor**: Each query maintains its own cursor position in the stream (persisted in memory; lost if server restarts)

### What Works Well

- `SELECT * FROM events` – All rows with an `id` or `created_at` cursor
- `SELECT id, type, data FROM events WHERE data->>'status' = 'active'` – Filtered incremental delivery
- `SELECT * FROM audit_log WHERE timestamp > now() - interval 1 day` – Time-windowed queries
- Tables with auto-incrementing `id` or `created_at` TIMESTAMP columns

### What Doesn't Work

- `SELECT sum(amount) FROM sales` – Aggregations re-compute and re-send entire result on each poll
- `SELECT e.id, u.name FROM events e JOIN users u ON e.user_id = u.id` – JOINs are rejected
- `SELECT * FROM events GROUP BY type` – GROUP BY is rejected
- `SELECT DISTINCT status FROM events` – DISTINCT is rejected
- Arbitrary complex SQL – Window functions, subqueries, CTEs, and complex WHERE clauses may not work as expected

### When to Use DuckStream

- Real-time monitoring dashboards ingesting append-only log tables
- Event stream processing where order matters and rows arrive incrementally
- Sensor data feeds or metrics collection with monotonic timestamps
- Use cases where all historical rows must be delivered to all clients

### When NOT to Use DuckStream

- Analytics queries over historical data (use batch query APIs instead)
- Complex transformations requiring JOINs or aggregations (use DuckDB directly)
- In-memory state tracking across many connected clients (server restart loses cursor state)
- Extremely low-latency requirements (<100ms) – polling introduces inherent delay
- Complex queries without stable `id` column for position tracking