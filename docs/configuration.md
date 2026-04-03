# Configuration Reference

## Overview

DuckStream can be configured via the `config.Config` struct. While the defaults are suitable for most use cases, you may need to adjust certain parameters for specific workloads.

## Configuration Struct

```go
type Config struct {
    // Database
    DuckDBPath string  // Path to DuckDB file (default: "duckstream.db")

    // Network
    QUICAddr   string  // QUIC server address (default: "localhost:4242")
    IngestAddr string  // HTTP server address (default: "localhost:8080")

    // Ingestion
    BatchSize    int           // Events per batch (default: 100)
    BatchTimeout time.Duration // Max time before flush (default: 1s)
    PollInterval time.Duration // Query polling interval (default: 100ms)

    // Limits
    MaxClients          int // Max concurrent QUIC connections (default: 100)
    MaxStreamsPerClient int // Max streams per client (default: 10)
    MaxQueries          int // Max registered queries (default: 50)
}
```

## Environment-Based Configuration

You can set configuration via environment variables:

```bash
# Using environment variables
DUCKSTREAM_DUCKDB_PATH=/data/stream.db \
DUCKSTREAM_QUIC_ADDR=:4242 \
DUCKSTREAM_INGEST_ADDR=:8080 \
DUCKSTREAM_MAX_CLIENTS=200 \
go run ./cmd/main.go
```

## Tuning Guidelines

### High-Throughput Ingestion

If you're ingesting many events per second:

```go
&Config{
    BatchSize:    500,       // Larger batches
    BatchTimeout: 500 * time.Millisecond,
}
```

### Low-Latency Streaming

For minimal delay between event and stream:

```go
&Config{
    PollInterval: 50 * time.Millisecond,  // More frequent polling
}
```

### Many Concurrent Queries

If running many simultaneous queries:

```go
&Config{
    MaxQueries:   100,
    MaxClients:   200,
    BatchSize:    50,  // Smaller batches to reduce memory pressure
}
```

## Connection Limits

| Parameter | Default | Max | Description |
|-----------|---------|-----|-------------|
| MaxClients | 100 | 1000 | Concurrent QUIC connections |
| MaxStreamsPerClient | 10 | 100 | Streams per connection |
| MaxQueries | 50 | 200 | Registered queries |

These limits prevent resource exhaustion and can be adjusted based on available memory and CPU.

## Monitoring Impact

Settings that affect metrics collection:

- `PollInterval` affects how often metrics are updated
- Lower intervals mean more accurate metrics but higher CPU usage

## Production Recommendations

```go
&Config{
    // Persistence
    DuckDBPath: "/var/lib/duckstream/duckstream.db",
    
    // Network
    QUICAddr:   "0.0.0.0:4242",
    IngestAddr: "0.0.0.0:8080",
    
    // Ingestion (tuned for production)
    BatchSize:    200,
    BatchTimeout: 2 * time.Second,
    PollInterval: 200 * time.Millisecond,
    
    // Limits (adjusted for capacity)
    MaxClients:          500,
    MaxStreamsPerClient:  20,
    MaxQueries:          100,
}
```