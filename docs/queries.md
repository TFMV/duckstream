# Query Language Reference

## Overview

DuckStream queries are SQL statements registered against the `events` table. Each query becomes a persistent, continuously executing stream.

## The Events Table

```sql
CREATE TABLE events (
    id BIGINT DEFAULT (nextval('events_id_seq')),
    data JSON,
    timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

| Column | Type | Description |
|--------|------|-------------|
| id | BIGINT | Auto-incrementing unique ID |
| data | JSON | Event payload |
| timestamp | TIMESTAMP | Event insertion time |

## Registering Queries

### Basic Syntax

```sql
REGISTER QUERY <query_id> AS <sql_statement>
```

**Example:**
```sql
REGISTER QUERY q1 AS SELECT * FROM events
```

### Query ID Rules
- Must be unique (no duplicate IDs)
- Alphanumeric and underscores only
- Max 50 queries allowed (configurable)

## Supported Query Patterns

### Select All

```sql
REGISTER QUERY all_events AS SELECT * FROM events
```

### Column Selection

```sql
REGISTER QUERY ids_only AS SELECT id, timestamp FROM events
```

### Filtering with JSON

```sql
REGISTER QUERY clicks AS SELECT * FROM events WHERE data->>'event' = 'click'
```

### Filter with Comparison

```sql
REGISTER QUERY recent AS SELECT * FROM events WHERE id > 1000
```

### Time-based Queries

```sql
REGISTER QUERY last_hour AS SELECT * FROM events 
  WHERE timestamp > NOW() - INTERVAL '1 hour'
```

### JSON Path Expressions

DuckDB supports rich JSON querying:

```sql
-- Access nested fields
REGISTER QUERY nested AS SELECT * FROM events 
  WHERE data->'user'->>'name' = 'Alice'

-- Check for existence
REGISTER QUERY has_error AS SELECT * FROM events 
  WHERE data ? 'error'

-- Array operations
REGISTER QUERY tags AS SELECT * FROM events 
  WHERE data->'tags' @> '["urgent"]'
```

## Query Behavior

### Continuous Execution

Queries run continuously from registration until explicitly unregistered:

```sql
REGISTER QUERY q1 AS SELECT * FROM events WHERE id > 0

-- New events matching the query are streamed automatically
-- Query never "finishes" - it keeps polling for new rows
```

### Incremental Polling

Each query executor maintains its own `lastID` cursor:

```sql
-- First poll: SELECT * FROM (SELECT * FROM events) WHERE id > 0
-- Returns rows 1, 2, 3...

-- Second poll (100ms later): SELECT * FROM (SELECT * FROM events) WHERE id > 3
-- Returns rows 4, 5...

-- And so on...
```

### Independent Subscriptions

Multiple queries run independently:

```sql
REGISTER QUERY clicks AS SELECT * FROM events WHERE data->>'event' = 'click'
REGISTER QUERY views AS SELECT * FROM events WHERE data->>'event' = 'view'
REGISTER QUERY all AS SELECT * FROM events

-- Insert: {"event": "click", "x": 100}
--   clicks gets: row (matches filter)
--   views gets: nothing (doesn't match)
--   all gets: row (no filter)
```

## Query Limitations

### What's Supported
- SELECT statements with WHERE clauses
- JSON extraction (->>, ->, ?)
- Aggregations (COUNT, SUM, etc.)
- Joins with other tables (if created)

### What's Not Supported
- Mutations (INSERT, UPDATE, DELETE)
- DDL (CREATE, ALTER, DROP)
- Transactions
- Subqueries that reference non-events tables in ways that break incremental polling

## Examples

### Event Analytics

```sql
-- Track click events
REGISTER QUERY click_events AS 
SELECT * FROM events 
WHERE data->>'event' = 'click'

-- Track view events  
REGISTER QUERY view_events AS
SELECT id, data, timestamp FROM events
WHERE data->>'event' = 'view'

-- Error events
REGISTER QUERY errors AS
SELECT * FROM events
WHERE data ? 'error'
```

### Aggregation Streams

```sql
-- Count events (note: aggregation resets each poll)
REGISTER QUERY event_count AS
SELECT COUNT(*) as total FROM events
```

### Time Windows

```sql
-- Events in last 5 minutes
REGISTER QUERY recent_events AS
SELECT * FROM events
WHERE timestamp > NOW() - INTERVAL '5 minutes'
```

### Complex Filters

```sql
-- High-value clicks
REGISTER QUERY high_value AS
SELECT * FROM events
WHERE data->>'event' = 'click'
AND CAST(data->>'value' AS INTEGER) > 100

-- User-specific events
REGISTER QUERY user_events AS
SELECT * FROM events
WHERE data->'user'->>'id' = 'user_123'
```

## Best Practices

1. **Use specific filters** - More selective queries deliver fewer but more relevant rows

2. **Select needed columns** - `SELECT id, data` is more efficient than `SELECT *`

3. **Index with id** - The `id` column is the incremental cursor; filtering by `id` is most efficient

4. **JSON validation** - Ensure your JSON data matches the query expectations

5. **Unregister unused queries** - Clean up queries that are no longer needed to free resources

```sql
-- Good
REGISTER QUERY filtered AS SELECT id, data FROM events WHERE data->>'event' = 'click'

-- Less efficient
REGISTER QUERY all AS SELECT * FROM events
```