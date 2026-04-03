# StreamPlan Compiler

The StreamPlan compiler transforms arbitrary DuckDB queries into executable streaming plans, enabling long-running subscriptions to filtered views of data.

## Overview

Instead of being limited to queries on the `events` table, users can now register arbitrary queries that will be continuously streamed. The compiler validates queries and extracts their essential components to build an efficient execution plan.

## Architecture

```
Raw SQL Query
    ↓
Validation (reject forbidden patterns)
    ↓
SQL Parsing (extract table, projection, WHERE)
    ↓
Table Validation (verify table exists)
    ↓
Schema Discovery (DESCRIBE table)
    ↓
Cursor Column Selection (id → created_at → fail)
    ↓
Cursor Type Validation (BIGINT or TIMESTAMP)
    ↓
StreamPlan
```

## StreamPlan Type

```go
type StreamPlan struct {
    ID             string        // Unique query identifier
    Table          string        // Source table name
    ProjectionSQL  string        // SELECT clause (as written)
    BaseWhereSQL   string        // WHERE clause (as written, or empty)
    CursorColumn   string        // Column used for incremental tracking
    CursorType     string        // Type of cursor column (BIGINT or TIMESTAMP)
    Mode           StartMode     // BEGINNING or NOW
    BatchSize      int           // Rows per batch
}
```

## Query Validation

Queries are validated to reject constructs that don't work well with streaming:

### Forbidden Patterns

| Pattern | Reason |
|---------|--------|
| `JOIN` | Requires state from multiple tables |
| `GROUP BY` | Requires aggregation state across rows |
| `DISTINCT` | Requires deduplication state |
| Window functions (`OVER`) | Requires windowing state |
| Subqueries | Complex nesting makes incremental tracking difficult |

### Valid Queries

✅ `SELECT * FROM events`
✅ `SELECT id, data FROM events WHERE id > 100`
✅ `SELECT * FROM users WHERE status = 'active'`
✅ `SELECT user_id, COUNT(*) as count FROM sales` (if SELECT clause doesn't have GROUP BY - wait, GROUP BY is rejected)
✅ `SELECT * FROM orders ORDER BY created_at` (ORDER BY is allowed)

### Invalid Queries

❌ `SELECT * FROM a JOIN b ON a.id = b.id` (JOIN)
❌ `SELECT * FROM events GROUP BY id` (GROUP BY)
❌ `SELECT DISTINCT id FROM events` (DISTINCT)
❌ `SELECT ROW_NUMBER() OVER (ORDER BY id) FROM events` (Window function)
❌ `SELECT * FROM (SELECT * FROM events) AS sq` (Subquery)

## Cursor Column Selection

The cursor column tracks position in the stream and enables incremental polling.

### Selection Priority

1. **User-provided hint**: If specified and found, use it
2. **"id" column**: Auto-incrementing primary key (preferred)
3. **"created_at" column**: Timestamp-based ordering
4. **Fail**: If none available, compilation fails

### Valid Cursor Column Types

- `BIGINT`, `INT64` - for numeric cursors
- `TIMESTAMP`, `TIMESTAMP WITH TIME ZONE` - for time-based cursors

## Usage Example

```go
package main

import (
    "context"
    "github.com/duckstream/duckstream/internal/config"
    "github.com/duckstream/duckstream/internal/duckdb"
    "github.com/duckstream/duckstream/internal/query"
)

func main() {
    client, _ := duckdb.NewClient("duckstream.db")
    cfg := config.Default()
    ctx := context.Background()

    // Compile a streaming plan
    plan, err := query.CompileStreamPlan(
        ctx,
        client,
        cfg,
        "q1",                          // Query ID
        "SELECT * FROM events WHERE json_extract(data, '$.type') = 'click'",
        nil,                           // Use default cursor column (id)
        query.StartModeBeginning,      // Stream from the beginning
    )
    if err != nil {
        panic(err)
    }

    // Use the plan to initialize a stream
    println("Table:", plan.Table)
    println("Cursor:", plan.CursorColumn, "(" + plan.CursorType + ")")
}
```

## Compiler Functions

### CompileStreamPlan

```go
func CompileStreamPlan(
    ctx context.Context,
    client *duckdb.Client,
    cfg *config.Config,
    queryID string,
    rawSQL string,
    cursorColumnHint *string,
    mode StartMode,
) (*StreamPlan, error)
```

Main entry point. Performs all validation and returns a StreamPlan or error.

### validateSQL

```go
func validateSQL(sql string) error
```

Returns error if SQL contains forbidden patterns.

### parseSQL

```go
func parseSQL(sql string) (table, projection, whereClause string, err error)
```

Extracts components from raw SQL, preserving original formatting.

### determineCursorColumn

```go
func determineCursorColumn(schema []ColumnSchema, hint *string) (string, error)
```

Selects cursor column based on availability and hint.

## Integration Points

The StreamPlan compiler will be integrated with:

1. **Query Manager**: Validate queries before registration
2. **Executor**: Use StreamPlan to configure streaming behavior
3. **HTTP API**: Accept cursor hints in registration requests
4. **REPL**: Allow cursor specification in REGISTER QUERY commands

## Error Types

```go
ErrForbiddenJoin
ErrForbiddenGroupBy
ErrForbiddenDistinct
ErrForbiddenWindowFunction
ErrForbiddenSubquery
ErrNoFromClause
ErrInvalidSQL
```

## Next Steps

After StreamPlan implementation, the system will:

1. Update `Executor` to use StreamPlan for incremental polling
2. Use cursor column instead of hardcoded "id" column
3. Support arbitrary cursor types (not just BIGINT)
4. Extend HTTP API to accept cursor column hints
5. Update REPL for query registration with cursor configuration
