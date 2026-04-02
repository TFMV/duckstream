DuckStream turns DuckDB into a **live SQL stream processor where queries run forever and push results over QUIC like subscriptions.**

---

# duckstream

SQL queries registered in DuckDB become long-running streaming processes, with results continuously pushed over QUIC until explicitly stopped.

## Demo

```bash
go run ./cmd/main.go

> REGISTER QUERY q1 AS SELECT * FROM events

go run ./cmd/demo
```

Server runs:

* QUIC stream server (localhost:4242)
* HTTP ingest endpoint (localhost:8080)
* REPL for query control

Demo client ingests events and streams results in real time.

## Architecture

```
Data → Ingest → DuckDB → Query Runtime → QUIC → Client
```

* HTTP ingestion into `events` table via DuckDB Appender
* Persistent queries executed as live polling cursors
* Each query mapped to a QUIC stream
* Results streamed continuously until unregistered

## Core idea

* tables = append-only signals
* queries = live subscriptions over those signals
* execution = infinite loop over a changing dataset
* QUIC = delivery layer for streaming results
