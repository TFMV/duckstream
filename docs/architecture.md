# DuckStream Architecture

## System Overview

DuckStream is a real-time streaming SQL engine built on DuckDB. It transforms SQL queries registered against an append-only events table into continuously running streams delivered over QUIC.

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
    Validating --> Running: SQL valid
    Validating --> Error: SQL invalid
    
    Running --> Polling: Start executor
    Polling --> Polling: id > lastID
    Polling --> Streaming: New rows found
    
    Streaming --> Delivering: Send over QUIC
    Delivering --> Polling: Done
    
    Running --> Stopped: UNREGISTER QUERY
    Stopped --> [*]
    Error --> [*]
```

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