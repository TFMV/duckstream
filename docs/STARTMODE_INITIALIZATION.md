# Query Initialization and StreamState Management

## Overview

DuckStream now implements StartMode-based query initialization and tracks streaming state through the `StreamState` structure. This enables queries to start from the beginning of a table or from the current state, with full state tracking and metrics.

## StartMode Initialization

### StartModeBeginning
When a query is registered with `StartModeBeginning`, the executor initializes the cursor to the minimal value:

- **BIGINT/INT64 cursors**: `int64(0)`
- **TIMESTAMP cursors**: `time.Time{}` (epoch/zero time)

This allows the query to stream all historical data from the start of the table.

```go
plan := &StreamPlan{
    Mode: StartModeBeginning,
    CursorColumn: "id",
    CursorType: "BIGINT",
    // ...
}
// cursor initialized to 0
```

### StartModeNow
When a query is registered with `StartModeNow`, the executor queries the database for the maximum cursor value:

```sql
SELECT MAX(<cursor_column>) FROM <table>
```

Then sets the cursor to that value, causing the query to start streaming only new rows added after initialization.

```go
plan := &StreamPlan{
    Mode: StartModeNow,
    CursorColumn: "created_at",
    CursorType: "TIMESTAMP",
    // ...
}
// cursor initialized to SELECT MAX(created_at) FROM table
```

**Edge case**: If the table is empty (MAX returns NULL), cursor is set to minimal value (same as StartModeBeginning).

## StreamState Structure

The `StreamState` struct tracks the complete state of a streaming query:

```go
type StreamState struct {
    QueryID       string        // Query identifier
    CursorValue   interface{}   // Current cursor position (int64 or time.Time)
    RowsSent      int64         // Total rows streamed so far
    ErrorCount    int64         // Total errors encountered
    LastUpdateAt  time.Time     // Time of last successful poll
    InitializedAt time.Time     // Time query was initialized
}
```

### CursorValue Field

The `CursorValue` is polymorphic and holds:

- **BIGINT queries**: `interface{}` containing `int64` value
- **TIMESTAMP queries**: `interface{}` containing `time.Time` value

This enables type-safe cursor tracking across different cursor types.

## Initialization Flow

```
Manager.Register(ctx, id, sql, cursorHint)
    ↓
CompileStreamPlan() → validates query → returns StreamPlan
    ↓
NewExecutor(client, sender, id, pollInterval)
    ↓
Executor.Start(ctx, plan)
    ├─ Lock: Create StreamState with QueryID
    ├─ Call initializeStreamState(ctx, plan, state)
    │   ├─ If StartModeBeginning: state.CursorValue = minimal value
    │   ├─ If StartModeNow: 
    │   │   ├─ Query: SELECT MAX(cursor) FROM table
    │   │   ├─ If result: state.CursorValue = result
    │   │   └─ Else: state.CursorValue = minimal value
    │   └─ Return state
    ├─ Store state in e.state
    └─ Start polling goroutine
```

## State Updates During Polling

For each successful poll:

1. **Execute streaming SQL** with `state.CursorValue` as starting point
2. **Collect rows** and track maximum cursor value in batch
3. **Send batch** to QUIC clients
4. **Update state** (if send successful):
   ```go
   state.CursorValue = maxCursorSeen
   state.RowsSent += len(rowBatch)
   state.LastUpdateAt = time.Now()
   ```
5. **Sleep** for `pollInterval`
6. **Repeat** with new cursor position

### At-Least-Once Delivery Guarantee

The cursor only advances **after** successful send. If send fails:
- State is not updated
- Same rows attempted on next poll
- No data loss guaranteed

## Accessing StreamState

### From Executor
```go
state := executor.GetState()
if state != nil {
    fmt.Printf("Query %s at cursor %v, sent %d rows\n", 
        state.QueryID, state.CursorValue, state.RowsSent)
}
```

### From Manager
```go
state := manager.GetQueryState(queryID)
if state != nil {
    fmt.Printf("Streaming state: %v\n", state)
}
```

## Type Safety for Cursor Values

The executor provides type-safe cursor value parsing:

```go
// parseCursorValue handles both cursor types
cursor := executor.parseCursorValue(plan.CursorType, rawValue)

// Returns:
// - int64 for BIGINT/INT64 types
// - time.Time for TIMESTAMP types
// - nil if parsing fails
```

### Timestamp Parsing Formats

Supported formats (tried in order):
1. RFC3339Nano: `2006-01-02T15:04:05.999999999Z07:00`
2. SQL format: `2006-01-02 15:04:05`

## Metrics Tracking

StreamState tracks operational metrics:

- **RowsSent**: Cumulative rows delivered to clients
- **ErrorCount**: Total errors encountered
- **LastUpdateAt**: Time of last successful operation
- **InitializedAt**: When query started

These enable monitoring dashboards and performance analysis.

## Edge Cases Handled

| Case | Handling |
|------|----------|
| Empty table + StartModeNow | Start from minimal cursor (epoch/0) |
| NULL cursor value | Treated as minimal cursor |
| Cursor parse failure | Error logged, polling continues |
| State not initialized | Error returned, query stops |
| Type mismatch | Type assertion verified via getMaxCursor |

## Configuration

StartMode is determined at query compile time:

- Default: `StartModeBeginning` (stream all historical data)
- Can be overridden via manager or external API (future enhancement)

```go
plan, err := CompileStreamPlan(
    ctx, client, cfg, 
    queryID, sql, 
    cursorHintPtr,
    StartModeBeginning,  // ← Set here
)
```

## Testing

Comprehensive test coverage includes:

- `TestStreamStateStructure` - Field initialization
- `TestStreamStateBigintCursor` - BIGINT cursor handling
- `TestStreamStateTimestampCursor` - TIMESTAMP cursor handling  
- `TestStreamStateRowsMetrics` - Metrics accumulation
- `TestStreamStateTiming` - Initialization/update timestamps
- `TestStartModeConstants` - Mode constant verification

**Status**: All tests passing ✅

## Future Enhancements

1. **Cursor Persistence**: Save/restore StreamState on server restart
2. **State Export**: REST API endpoint to query StreamState
3. **Cursor Reset**: Allow resetting cursor to beginning/now
4. **State History**: Track cursor advancement rate and patterns
5. **Backpressure**: Pause polling if send queue backs up
6. **Checkpointing**: Periodic state snapshots for recovery
