# Query Language Reference

## Overview

DuckStream queries are SQL statements registered against the `events` table. Each query becomes a persistent, continuously executing stream.

## The Events Table

DuckStream queries execute against tables containing an auto-incrementing or timestamp-based cursor column for incremental streaming:

```sql
CREATE TABLE events (
    id BIGINT DEFAULT (nextval('events_id_seq')),
    data JSON,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

| Column | Type | Cursor? | Description |
|--------|------|---------|-------------|
| id | BIGINT | ✓ Preferred | Auto-incrementing unique ID (primary cursor column) |
| created_at | TIMESTAMP | ✓ Alternative | Event insertion timestamp (cursor if id unavailable) |
| data | JSON | ✗ | Event payload |

**Cursor Column Selection**: At registration, DuckStream auto-detects the cursor column:
1. If table has `id` (BIGINT), use it as cursor
2. Else if table has `created_at` (TIMESTAMP), use it as cursor
3. Else registration fails: "no valid cursor column found (expected id or created_at)"

The cursor column must be strictly **monotonically increasing** (BIGINT) or **monotonically increasing time** (TIMESTAMP). Both ensure rows are delivered exactly once.

## Registering Queries

### Extended Syntax

```sql
REGISTER QUERY <query_id> AS <sql_statement> 
  [WITH CURSOR <column_name>] 
  [FROM NOW | FROM BEGINNING]
```

### Parameters

- **query_id**: Unique identifier (alphanumeric + underscores, max 50 registered queries)
- **sql_statement**: SELECT query (single-table, no JOINs/GROUP BY/DISTINCT; see Constraints)
- **WITH CURSOR column_name** (optional): Override auto-detected cursor column (e.g., `WITH CURSOR user_id`)
- **FROM NOW** (optional): Start streaming from current max cursor value (skip all historical rows)
- **FROM BEGINNING** (optional): Start from cursor=0 or earliest timestamp (default)

### Examples

```sql
-- Basic: Stream all events from start
REGISTER QUERY all_events AS SELECT * FROM events

-- With explicit cursor: Use timestamp instead of auto-detected id
REGISTER QUERY by_time AS SELECT * FROM events WITH CURSOR created_at

-- Skip history: Start from current time (FOR TIMESTAMP cursors)
REGISTER QUERY new_only AS SELECT * FROM events FROM NOW

-- Combine: Use created_at cursor and skip to current time
REGISTER QUERY recent AS SELECT * FROM events WITH CURSOR created_at FROM NOW
```

## Supported Query Patterns

DuckStream enforces strict SQL validation at registration time. Only the following patterns are supported:

### Single-Table SELECT Queries

```sql
-- All rows from a table
REGISTER QUERY all AS SELECT * FROM events

-- Specific columns
REGISTER QUERY cols AS SELECT id, data, created_at FROM events
```

### WHERE Clause Filtering

```sql
-- JSON field extraction
REGISTER QUERY clicks AS SELECT * FROM events 
  WHERE data->>'event' = 'click'

-- Numeric comparison
REGISTER QUERY high_value AS SELECT * FROM events 
  WHERE CAST(data->>'value' AS INTEGER) > 100

-- Nested JSON access
REGISTER QUERY user_123 AS SELECT * FROM events 
  WHERE data->'user'->>'id' = '123'

-- Time-based filtering (useful with created_at cursor)
REGISTER QUERY recent AS SELECT * FROM events 
  WHERE created_at > NOW() - INTERVAL '1 hour'
```

### JSON Operations

```sql
-- Check field existence
REGISTER QUERY has_error AS SELECT * FROM events 
  WHERE data ? 'error'

-- Array containment
REGISTER QUERY urgent AS SELECT * FROM events 
  WHERE data->'tags' @> '["urgent"]'
```

## Query Behavior

### Continuous Execution & Cursor Tracking

Once registered, a query runs continuously and maintains its own cursor position:

```sql
-- At registration
REGISTER QUERY q1 AS SELECT * FROM events WHERE id > 0
-- Query state initialized:
--   cursor = 0 (FROM BEGINNING, the default)
--   rows_streamed = 0

-- Every 100ms, the executor polls:
SELECT * FROM events WHERE id > <cursor_value>
-- Returns new rows since last cursor value
-- Cursor is advanced to the max id in the result set
-- Rows are delivered over QUIC to connected clients

-- If connection drops and reconnects
-- Cursor did NOT advance (because delivery failed)
-- Same rows are re-sent (at-least-once semantics)
```

### Multiple Cursor Types

```sql
-- BIGINT cursor (auto-detected from id column)
REGISTER QUERY by_id AS SELECT * FROM events
-- Polling: WHERE id > <last_id_value>
-- Start mode: FROM BEGINNING → cursor=0, FROM NOW → cursor=max_id

-- TIMESTAMP cursor (auto-detected from created_at column, or explicit)
REGISTER QUERY by_time AS SELECT * FROM events WITH CURSOR created_at
-- Polling: WHERE created_at > <last_timestamp_value>
-- Start mode: FROM BEGINNING → cursor=epoch, FROM NOW → cursor=NOW()
```

### Independent Query Streams

Each registered query maintains its own cursor and delivers independently:

```sql
REGISTER QUERY clicks AS SELECT * FROM events WHERE data->>'event' = 'click'
REGISTER QUERY views AS SELECT * FROM events WHERE data->>'event' = 'view'
REGISTER QUERY all AS SELECT * FROM events

-- Insert: {"event": "click", "x": 100}
--   clicks streams: row with cursor advancement (matches WHERE)
--   views does NOT stream: row doesn't match filter, cursor unchanged
--   all streams: row with cursor advancement (no filter)
```

## Query Constraints

DuckStream enforces these constraints at registration time. Attempts to register violating queries fail immediately with explicit error messages:

### Forbidden Constructs

| Construct | Error Message | Reason |
|-----------|---------------|--------|
| Multi-table | `only single-table SELECT queries are supported` | Cannot efficiently cursor-track multiple sources |
| JOIN | `JOINs are not supported in streaming queries` | Requires pre-joined state for incremental delivery |
| GROUP BY | `GROUP BY is not supported` | Aggregation re-computes full result on each poll, no incremental semantics |
| DISTINCT | `DISTINCT is not supported` | Requires buffering all rows to eliminate duplicates |
| Window functions | `Window functions are not supported` | Complex per-row computations break cursor-based streaming |
| Subqueries | `Subqueries are not supported` | Nested SELECT queries break incremental semantics |
| CTEs | Not supported | Common Table Expressions cannot be efficiently incremented |

### Cursor Requirements

| Requirement | Error if violated | Resolution |
|--------------|------------------|------------|
| Valid cursor column | `no valid cursor column found (expected id or created_at)` | Add `id` (BIGINT) or `created_at` (TIMESTAMP) column to table |
| Cursor type | `cursor column must be BIGINT or TIMESTAMP` | Cursor column must be BIGINT (auto-increment) or TIMESTAMP (monotonic time) |

### Not Supported (General)

- Mutations (INSERT, UPDATE, DELETE)
- DDL (CREATE, ALTER, DROP) on the query stream
- Transactions
- Recursive queries

## Examples

### Real-time Event Streaming

```sql
-- Stream all events from start
REGISTER QUERY all_events AS 
SELECT * FROM events

-- Stream only click events
REGISTER QUERY click_events AS 
SELECT * FROM events 
WHERE data->>'event' = 'click'

-- Stream only error events
REGISTER QUERY errors AS
SELECT id, data, created_at FROM events
WHERE data ? 'error'
```

### Filtered Streams with Start Modes

```sql
-- Skip historical events, stream only new ones
REGISTER QUERY new_events AS
SELECT * FROM events FROM NOW

-- Stream user-specific events from the beginning
REGISTER QUERY user_123_all AS
SELECT * FROM events
WHERE data->'user'->>'id' = '123'
FROM BEGINNING

-- Stream high-value clicks, skipping history
REGISTER QUERY high_value_new AS
SELECT * FROM events
WHERE data->>'event' = 'click'
AND CAST(data->>'value' AS INTEGER) > 100
FROM NOW
```

### Timestamp-Based Cursors

```sql
-- Use created_at as cursor instead of auto-detected id
REGISTER QUERY by_timestamp AS
SELECT id, data, created_at FROM events
WITH CURSOR created_at

-- Combine explicit cursor with start mode
REGISTER QUERY recent_by_time AS
SELECT * FROM events
WHERE created_at > NOW() - INTERVAL '1 hour'
WITH CURSOR created_at
FROM NOW
```

## Best Practices

1. **Use specific filters** - Narrow WHERE clauses deliver fewer, more relevant rows per poll
   ```sql
   -- Good: Only click events
   REGISTER QUERY clicks AS SELECT * FROM events WHERE data->>'event' = 'click'
   
   -- Less efficient: All events
   REGISTER QUERY all AS SELECT * FROM events
   ```

2. **Select only needed columns** - Reduces per-row serialization overhead
   ```sql
   -- Good: Only relevant fields
   REGISTER QUERY id_data AS SELECT id, data FROM events WHERE data->>'event' = 'click'
   
   -- More overhead: All columns
   REGISTER QUERY all AS SELECT * FROM events
   ```

3. **Choose appropriate start mode** - Skip unnecessary historical rows
   ```sql
   -- Good for dashboards (don't need old data):
   REGISTER QUERY dashboard AS SELECT * FROM events FROM NOW
   
   -- Good for data pipelines (need all rows):
   REGISTER QUERY pipeline AS SELECT * FROM events FROM BEGINNING
   ```

4. **Use explicit cursor if needed** - Override auto-detection for time-based queries
   ```sql
   -- If you need creation-time semantics:
   REGISTER QUERY by_creation_time AS SELECT * FROM events WITH CURSOR created_at
   ```

5. **Avoid unsupported patterns at registration** - Validation is strict to ensure reliable streaming
   ```sql
   -- WRONG: Will be rejected at registration
   REGISTER QUERY agg AS SELECT COUNT(*) FROM events  -- Error: GROUP BY not supported
   REGISTER QUERY joined AS SELECT * FROM events e JOIN users u ON ... -- Error: JOINs not supported
   ```

6. **Unregister unused queries** - Free resources on the server
   ```sql
   UNREGISTER QUERY old_query
   ```
```