# DuckStream Architecture

## System Overview

DuckStream is a real-time streaming SQL engine built on DuckDB. It ingests events into a configurable `events` table by default at `/ingest`, and also supports table-specific ingestion on `/ingest/<table>`. Registered SQL queries can target any table that exposes a valid cursor column (e.g., `id` BIGINT or `created_at` TIMESTAMP), and are delivered as continuously running streams over QUIC.

## High-Level Architecture

```mermaid
flowchart TB
    subgraph Clients
        REPL[REPL Control]
        HTTP[HTTP API]
        QUIC[QUIC Clients]
        Browser[Web Dashboard]
    end

    subgraph DuckStream
        subgraph Ingestion
            HTTPIng[HTTP Ingest]
            Batch[Batch Buffer]
        end

        subgraph Storage
            DuckDB[(DuckDB)]
            Events[(events table)]
        end

        subgraph QueryRuntime
            QM[Query Manager]
            QE1[Executor q1]
            QE2[Executor q2]
            QE3[Executor qN]
        end

        subgraph Transport
            QS[QUIC Server]
            S1[Session 1]
            S2[Session 2]
        end
    end

    HTTPIng --> Batch
    Batch --> DuckDB
    Events --> QM
    QM --> QE1
    QM --> QE2
    QM --> QE3
    QE1 --> QS
    QE2 --> QS
    QE3 --> QS
    QS --> S1
    QS --> S2
    REPL --> QM
    HTTP --> QM
    QUIC --> QS
    Browser --> HTTP
```

## Component Interactions

### Data Flow

```mermaid
sequenceDiagram
    participant Client
    participant HTTP as HTTP Server
    participant Ingest as Ingest Handler
    participant DuckDB
    participant QM as Query Manager
    participant Exec as Executor
    participant QUIC as QUIC Server
    participant Session

    Client->>HTTP: POST /ingest {data: {...}}
    HTTP->>Ingest: Handle request
    Ingest->>DuckDB: Appender API batch insert
    DuckDB-->>Ingest: Success
    Ingest-->>Client: 200 OK

    loop Every 100ms
        Exec->>DuckDB: SELECT * WHERE id > lastID
        DuckDB-->>Exec: New rows
        Exec->>Protocol: EncodeRowBatch
        Protocol->>QUIC: SendToQuery(queryID, data)
        QUIC->>Session: Write to stream
        Session-->>Client: Streaming results
    end
```

### Query Lifecycle

```mermaid
stateDiagram-v2
    [*] --> Idle
    
    Idle --> Validating: REGISTER QUERY q1 AS ...
    
    Validating --> ParseSQL: Parse SQL statement
    ParseSQL --> CheckConstraints: Validate single-table, no JOIN/GROUP/DISTINCT/OVER
    CheckConstraints --> ValidateTable: Verify table exists
    ValidateTable --> DetectCursor: Auto-detect cursor (id or created_at)
    DetectCursor --> ValidateCursorType: Verify cursor is BIGINT or TIMESTAMP
    ValidateCursorType --> BuildStreamPlan: Create StreamPlan with SQL, cursor, mode
    
    BuildStreamPlan --> Running: StreamPlan valid
    
    DetectCursor --> Error: Cursor not found
    ValidateCursorType --> Error: Cursor type invalid
    CheckConstraints --> Error: Forbidden construct detected
    ValidateTable --> Error: Table doesn't exist
    
    Running --> Initializing: Executor starts
    Initializing --> DetermineStart: Apply start mode (BEGINNING/NOW)
    DetermineStart --> Polling: Initialize cursor value
    Polling --> Polling: Poll every 100ms: WHERE cursor > value
    Polling --> Streaming: New rows found
    
    Streaming --> Delivering: Send over QUIC (at-least-once)
    Delivering --> CursorAdvance: Advance cursor past sent rows
    CursorAdvance --> Polling: Continue polling
    
    Running --> Stopped: UNREGISTER QUERY
    Stopped --> [*]
    Error --> [*]
```

### Query Compilation (StreamPlan)

The **StreamPlan** is the compiled representation of a query, created during registration:

1. **SQL Parsing**: Extract table name, columns, WHERE clause from SELECT statement
2. **Constraint Validation**: Reject JOINs, GROUP BY, DISTINCT, window functions, subqueries
3. **Table Resolution**: Verify table exists in DuckDB and is accessible
4. **Cursor Detection**: Auto-detect monotonic cursor (`id` BIGINT → preferred, `created_at` TIMESTAMP → fallback)
5. **Cursor Validation**: Verify cursor column exists and is BIGINT or TIMESTAMP
6. **SQL Building**: Construct parameterized polling query `SELECT ... WHERE <cursor> > ?`
7. **Stream State Initialization**: Prepare cursor value based on start mode (BEGINNING/NOW)

### Query State and Metrics

- **StreamState** tracked per query in `Executor`:
  - `CursorValue` (`int64` for BIGINT or `time.Time` for TIMESTAMP)
  - `RowsSent` (streamed rows count)
  - `LastUpdateAt` (last emitted update timestamp)
  - `InitializedAt` (stream initialization moment)
- `Manager.GetAllQueryStates()` exposes active states.
- `/metrics` includes per-query block:
  - `id`, `last_cursor`, `rows_streamed`, `lag_ms` (computed for TIMESTAMP cursor)

### SQL Validation & Error Handling

DuckStream performs strict SQL validation at registration time to ensure queries can be safely and efficiently streamed. All validation errors are explicit and fail fast:

#### Validation Rules

**1. Single-Table SELECT Enforcement**

```
Error: "only single-table SELECT queries are supported"
Details: Query specifies multiple tables (detected by parsing FROM clause or JOIN keywords)
```

**2. JOIN Rejection**

```
Error: "JOINs are not supported in streaming queries"
Details: All join types (INNER, LEFT, RIGHT, FULL, CROSS) are forbidden
Reason: Streaming requires independent cursor tracking per table; joins break this property
```

**3. GROUP BY Rejection**

```
Error: "GROUP BY is not supported in streaming queries"
Details: Aggregation queries (COUNT, SUM, AVG, GROUP_CONCAT, etc.) after GROUP BY are forbidden
Reason: Aggregations re-compute entire results on each poll; no incremental semantics possible
```

**4. DISTINCT Rejection**

```
Error: "DISTINCT is not supported"
Details: DISTINCT keyword in SELECT clause is forbidden
Reason: Requires buffering all rows to eliminate duplicates; breaks streaming
```

**5. Window Function Rejection**

```
Error: "Window functions are not supported"
Details: OVER(...) window function syntax in SELECT expressions is forbidden
Reason: Complex per-row computations require buffering; incompatible with cursor-based streaming
```

**6. Subquery Rejection**

```
Error: "Subqueries are not supported"
Details: Nested SELECT queries in FROM, WHERE, or SELECT clauses are forbidden
Reason: Subqueries complicate cursor tracking and incremental execution
```

**7. Cursor Column Discovery**

```
Error: "no valid cursor column found (expected id or created_at)"
Details: After all other validations pass, DuckStream searches for a cursor column:
  - First: Look for `id` (BIGINT type required)
  - Second: Look for `created_at` (TIMESTAMP type required)
  - None found: Registration fails
```

**8. Cursor Type Validation**

```
Error: "cursor column must be BIGINT or TIMESTAMP"
Details: If cursor column found but wrong type (e.g., VARCHAR, INT, NUMERIC):
  - BIGINT: Supports auto-incrementing sequences and numeric IDs
  - TIMESTAMP: Supports monotonic insertion time tracking
  - Other types: Insufficient guarantees for incremental delivery
```

#### Online vs. Upfront Validation

- **Upfront Validation (Registration Time)**: All constraints above are checked when `REGISTER QUERY` is issued
- **Online Detection (Runtime)**: Per-query metrics and cursor state are tracked during execution
- **No Runtime SQL Errors**: If a query passes registration, it will never fail during polling (assuming data integrity)

## Internal Components

### Query Manager

The query manager maintains the registry of active queries and coordinates their execution.

```mermaid
classDiagram
    class Manager {
        -queries map[string]*Query
        -client *duckdb.Client
        -sender protocol.Sender
        -maxQueries int
        +Register(ctx, id, sql) error
        +Unregister(ctx, id) error
        +List() []*Query
    }
    
    class Query {
        +ID string
        +SQL string
        +Active bool
        +Ctx context.Context
        +Cancel context.CancelFunc
        +Executor *Executor
    }
    
    class Executor {
        -client *duckdb.Client
        -sender protocol.Sender
        -queryID string
        -streamID string
        -lastID int64
        -running bool
        +Start(ctx, sql)
        +Stop()
    }
    
    Manager --> Query
    Query --> Executor
```

### QUIC Server

The QUIC server manages client connections and multiplexes query streams.

```mermaid
classDiagram
    class Server {
        -addr string
        -transport *quic.Transport
        -sessions map[string]*Session
        -maxClients int
        -activeClients atomic.Int64
        +Start(ctx) error
        +CanAccept() bool
        +GetSessions() []*Session
    }
    
    class Session {
        -conn *quic.Conn
        -streams map[string]*Stream
        -maxStreams int
        -activeStreams atomic.Int64
        +Run(ctx)
        +CanOpenStream() bool
        +SendToQuery(queryID, data) error
    }
    
    class Stream {
        -stream *quic.Stream
        -id quic.StreamID
        +Read(p []byte) (int, error)
        +Write(p []byte) (int, error)
    }
    
    Server --> Session
    Session --> Stream
```

## Data Flow Details

### Event Ingestion

```mermaid
flowchart LR
    subgraph Input
        JSON[JSON Data]
    end
    
    subgraph IngestHandler
        Buffer[(Buffer)]
        Flush[Flush Timer]
    end
    
    subgraph DuckDB
        APP[Appender API]
        EVT[(events)]
    end

    JSON --> Buffer
    Flush --> Buffer
    Buffer --> APP
    APP --> EVT
```

### Query Execution

```mermaid
flowchart TB
    subgraph Executor Loop
        T1[Ticker 100ms]
        Q1[Query: WHERE id > lastID]
        R1[Scan Rows]
        E1[Encode Row Batch]
        S1[Send over QUIC]
        U1[Update lastID]
    end
    
    T1 --> Q1
    Q1 --> R1
    R1 --> E1
    E1 --> S1
    S1 --> U1
    U1 --> Q1
```

## Protocol

### Binary Message Format

```
[message_type: 1 byte][payload_length: 4 bytes][payload: n bytes]
```

| Type | Code | Description |
|------|------|-------------|
| Row Batch | 0x01 | JSON array of rows |
| Completed | 0x02 | Query finished |
| Error | 0x03 | Error message |
| Heartbeat | 0x04 | Keep-alive |

### Query Stream Mapping

Each registered query gets a dedicated QUIC stream:

```mermaid
flowchart LR
    subgraph Client
        Q1[Query q1 stream]
        Q2[Query q2 stream]
    end
    
    subgraph Server
        QM[Query Manager]
        E1[Executor q1]
        E2[Executor q2]
    end
    
    Q1 --> E1
    Q2 --> E2
```

## Configuration

```mermaid
erDiagram
    CONFIG ||--o| DUCKDB : uses
    CONFIG ||--o| QUIC : configures
    CONFIG ||--o| HTTP : configures
    
    CONFIG {
        string DuckDBPath
        string QUICAddr
        string IngestAddr
        int BatchSize
        int BatchTimeout
        int PollInterval
        int MaxClients
        int MaxStreamsPerClient
        int MaxQueries
    }
    
    DUCKDB {
        string path
    }
    
    QUIC {
        string addr
        int maxClients
    }
    
    HTTP {
        string addr
    }
```

## Security & Limits

```mermaid
flowchart TB
    subgraph Limits
        LC[Max Clients: 100]
        LS[Max Streams/Client: 10]
        LQ[Max Queries: 50]
    end
    
    subgraph Enforcement
        Accept[CanAccept?]
        CheckClient[Check activeClients]
    end
    
    LC --> Accept
    LS --> CheckClient
    LQ --> Accept
```

## Deployment

```mermaid
graph TB
    subgraph Production
        LB[Load Balancer]
        DS1[DuckStream 1]
        DS2[DuckStream 2]
        DS3[DuckStream 3]
        DuckDB[(Shared DuckDB)]
        QUIC1[QUIC:4242]
        QUIC2[QUIC:4242]
        QUIC3[QUIC:4242]
    end
    
    subgraph Clients
        Web[Web Dashboard]
        App[Application]
        CLI[CLI/REPL]
    end
    
    LB --> DS1
    LB --> DS2
    LB --> DS3
    DS1 --> DuckDB
    DS2 --> DuckDB
    DS3 --> DuckDB
    Web --> LB
    App --> QUIC1
    App --> QUIC2
    App --> QUIC3
    CLI --> LB
```

## Monitoring

```mermaid
flowchart LR
    subgraph Metrics
        MR[Queries Registered]
        MU[Queries Unregistered]
        RS[Rows Sent]
        ER[Errors]
        AC[Active Clients]
        AQ[Active Queries]
    end
    
    subgraph Collection
        MC[Metrics Collector]
        PP[Prometheus]
        GD[Grafana]
    end
    
    MR --> MC
    MU --> MC
    RS --> MC
    ER --> MC
    AC --> MC
    AQ --> MC
    MC --> PP
    PP --> GD
```