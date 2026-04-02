# duckstream

SQL queries registered in DuckDB become long-running streaming queries, with results continuously streamed over QUIC until explicitly stopped.

## Architecture

```
Data → Ingestion → DuckDB → Query Runtime → QUIC → Client
```

- **Ingestion Layer**: HTTP POST endpoint receives JSON data, batch inserts into DuckDB `events` table using DuckDB Appender API
- **Query Runtime**: Manages persistent streaming queries with incremental polling
- **QUIC Transport**: Streams results to connected clients via QUIC
- **Control Surface**: REPL for registering/unregistering queries

## Quick Start

```bash
# Terminal 1: Start the server
go run ./cmd/main.go

# In the server REPL, register a query:
> REGISTER QUERY q1 AS SELECT * FROM events

# Terminal 2: Run the demo client
go run ./cmd/demo
```

The server starts:
- QUIC server on `localhost:4242`
- HTTP ingest on `localhost:8080`
- REPL control surface (stdin)

The demo client continuously ingests events every 5 seconds and displays the streaming results in real-time. Press Ctrl+C to stop.

## How It Works

**The Core Concept:** Think of `events` as an infinite append-only log. Each registered query is like subscribing to a filtered view of that log. New events are continuously delivered to all active subscribers.

**Step by Step:**

1. **Data flows in**: Events are inserted into the `events` table via HTTP POST. Each event gets a unique auto-incrementing `id`.

2. **Queries run continuously**: When you register a query like `SELECT * FROM events WHERE data->>'event' = 'click'`, the system creates a long-running executor that:
   - Remembers the last `id` it has seen
   - Polls DuckDB every 100ms for "new rows where id > lastSeen"
   - Sends any matching rows over QUIC to connected clients

3. **Independent subscriptions**: Each query tracks its own position. You can register 10 different queries with different filters - they'll all run independently, each getting only the rows that match their filter.

4. **Streaming over QUIC**: Results flow over QUIC streams. Each query maps to a stream. Clients connect to the QUIC server and read from their stream to get continuous updates.

5. **Explicit lifecycle**: Queries run forever (or until server stops). Use `UNREGISTER QUERY id` to stop a specific query and free its resources.

**Analogy:**
```
events table     = a journal/log
query            = a subscription to that journal with a filter
executor         = a background worker that checks for new entries
QUIC stream      = a delivery channel to the subscriber
```

**Example flow:**
```
Insert: {"event": "click", "x": 100}
  → q1 (WHERE event='click')  → delivers to client
  → q2 (WHERE event='view')  → skips (no match)
  → q3 (SELECT *)             → delivers to client
Insert: {"event": "view", "y": 200}
  → q1 → skips
  → q2 → delivers
  → q3 → delivers
```

## Behavior

- Tables are append-only signals (events)
- Queries are persistent transformations over those signals
- Execution is a live cursor that never naturally terminates
- QUIC delivers output streams to clients
- Queries run continuously until explicitly unregistered