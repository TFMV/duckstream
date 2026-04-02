# duckstream Specification

## Project Overview
- **Project name**: duckstream
- **Type**: Go service with embedded DuckDB and QUIC transport
- **Core functionality**: SQL queries registered in DuckDB become long-running streaming queries, results continuously streamed over QUIC until explicitly stopped
- **Target users**: Clients that need real-time streaming SQL query results

## Architecture

### Components
1. **Ingestion Layer** - Receives data, inserts into DuckDB `events` table
2. **Query Runtime** - Manages persistent streaming query execution
3. **QUIC Transport** - Streams results to clients over QUIC
4. **Control Surface** - CLI/REPL for registering/unregistering queries

### Data Flow
```
Data → Ingestion Layer → DuckDB → Query Runtime → QUIC → Client
```

## Functionality Specification

### 1. Ingestion Layer
- Accept JSON data via HTTP POST to `/ingest` endpoint
- Insert into DuckDB `events` table using batch appends
- Table schema: `events(id INTEGER, data JSON, timestamp TIMESTAMP)`
- Batch size: 100 rows or 1 second timeout

### 2. Query Runtime
- Register queries with: `REGISTER QUERY <id> AS <sql>`
- Store: query ID, SQL string, active state
- Execute as long-running cursor
- Poll DuckDB for new results periodically (100ms interval)
- Use timestamp-based incremental fetching

### 3. QUIC Transport
- Server listens on `localhost:4242`
- One QUIC connection per client
- Each query maps to a QUIC stream (stream_id = query hash)
- Binary protocol with message types

### 4. Protocol
```
[message_type: 1 byte][payload_length: 4 bytes][payload: n bytes]

Message types:
0x01 = row batch (JSON array of rows)
0x02 = query completed
0x03 = error
0x04 = heartbeat
```

### 5. Query Lifecycle
- `REGISTER QUERY q1 AS SELECT ...` - starts streaming
- `UNREGISTER QUERY q1` - stops execution, closes stream
- `LIST QUERIES` - shows active queries
- Interactive REPL control surface

## File Structure
```
duckstream/
├── cmd/
│   └── main.go              # Entry point
├── internal/
│   ├── config/
│   │   └── config.go        # Configuration
│   ├── duckdb/
│   │   ├── client.go        # DuckDB client wrapper
│   │   └── ingest.go        # Data ingestion
│   ├── query/
│   │   ├── manager.go       # Query lifecycle management
│   │   └── executor.go      # Streaming query executor
│   ├── quic/
│   │   ├── server.go        # QUIC server
│   │   └── stream.go        # Stream handling
│   ├── protocol/
│   │   └── encoder.go       # Binary encoding
│   └── repl/
│       └── repl.go          # Control surface REPL
├── go.mod
└── go.sum
```

## Acceptance Criteria
1. DuckDB embedded and operational
2. Can insert data into events table via HTTP
3. Can register persistent queries via REPL
4. Queries stream results continuously over QUIC
5. Can unregister queries to stop streaming
6. Long-running queries don't consume excessive memory
7. Protocol correctly encodes and decodes messages