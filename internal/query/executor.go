package query

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/duckstream/duckstream/internal/duckdb"
	"github.com/duckstream/duckstream/internal/metrics"
	"github.com/duckstream/duckstream/internal/protocol"
)

// StreamState holds the current state of a streaming query
type StreamState struct {
	QueryID       string
	CursorValue   interface{} // int64 for BIGINT, time.Time for TIMESTAMP
	RowsSent      int64
	ErrorCount    int64
	LastUpdateAt  time.Time
	InitializedAt time.Time
}

type Executor struct {
	client       *duckdb.Client
	sender       protocol.Sender
	queryID      string
	streamID     string
	pollInterval time.Duration

	mu      sync.RWMutex
	running bool
	plan    *StreamPlan
	state   *StreamState
	stopCh  chan struct{}
}

func NewExecutor(client *duckdb.Client, sender protocol.Sender, queryID string, pollInterval time.Duration) *Executor {
	return &Executor{
		client:       client,
		sender:       sender,
		queryID:      queryID,
		streamID:     "query:" + queryID,
		pollInterval: pollInterval,
		running:      false,
		stopCh:       make(chan struct{}),
	}
}

func (e *Executor) Start(ctx context.Context, plan *StreamPlan) {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return
	}
	e.running = true
	e.plan = plan
	e.stopCh = make(chan struct{})

	// Initialize the stream state
	state := &StreamState{
		QueryID:       plan.ID,
		InitializedAt: time.Now(),
	}
	e.mu.Unlock()

	// Initialize cursor based on start mode (outside lock)
	if err := e.initializeStreamState(ctx, plan, state); err != nil {
		e.sendError(fmt.Sprintf("initialization error: %v", err))
		e.mu.Lock()
		e.running = false
		e.mu.Unlock()
		return
	}

	e.mu.Lock()
	e.state = state
	e.mu.Unlock()

	go e.run(ctx)
}

// initializeStreamState sets up the cursor position based on start mode
func (e *Executor) initializeStreamState(ctx context.Context, plan *StreamPlan, state *StreamState) error {
	switch plan.Mode {
	case StartModeBeginning:
		// Start from the beginning
		if strings.Contains(strings.ToUpper(plan.CursorType), "TIMESTAMP") {
			state.CursorValue = time.Time{} // epoch
		} else {
			state.CursorValue = int64(0)
		}
		return nil

	case StartModeNow:
		// Start from the current maximum value
		maxCursor, err := e.getMaxCursor(ctx, plan)
		if err != nil {
			return fmt.Errorf("failed to get max cursor: %w", err)
		}

		if maxCursor != nil {
			state.CursorValue = maxCursor
		} else {
			// If table is empty, start from beginning
			if strings.Contains(strings.ToUpper(plan.CursorType), "TIMESTAMP") {
				state.CursorValue = time.Time{}
			} else {
				state.CursorValue = int64(0)
			}
		}
		return nil

	default:
		return fmt.Errorf("unknown start mode: %v", plan.Mode)
	}
}

// getMaxCursor queries the database for the maximum cursor value
func (e *Executor) getMaxCursor(ctx context.Context, plan *StreamPlan) (interface{}, error) {
	query := fmt.Sprintf("SELECT MAX(%s) FROM %s", plan.CursorColumn, plan.Table)

	row := e.client.DB().QueryRowContext(ctx, query)
	var maxValue sql.NullString
	err := row.Scan(&maxValue)
	if err != nil {
		return nil, err
	}

	// If no rows or NULL, return nil
	if !maxValue.Valid || maxValue.String == "" {
		return nil, nil
	}

	// Parse based on cursor type
	return e.parseCursorValue(plan.CursorType, maxValue.String), nil
}

func (e *Executor) run(ctx context.Context) {
	e.mu.RLock()
	plan := e.plan
	e.mu.RUnlock()

	ticker := time.NewTicker(e.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.sendError("context cancelled")
			return
		case <-e.stopCh:
			_ = e.sender.SendToQuery(e.streamID, protocol.EncodeCompleted())
			return
		case <-ticker.C:
			e.poll(ctx, plan)
		}
	}
}

// poll executes the streaming query and sends results
func (e *Executor) poll(ctx context.Context, plan *StreamPlan) {
	e.mu.RLock()
	state := e.state
	e.mu.RUnlock()

	if state == nil {
		e.sendError("stream state not initialized")
		return
	}

	// Build the streaming SQL query
	streamingSQL := BuildStreamingSQL(*plan)

	// Execute query with cursor and batch size parameters
	rows, err := e.client.DB().QueryContext(ctx, streamingSQL, state.CursorValue, plan.BatchSize)
	if err != nil {
		e.sendError(fmt.Sprintf("query error: %v", err))
		return
	}
	defer rows.Close()

	// Get column names
	cols, err := rows.Columns()
	if err != nil {
		e.sendError(fmt.Sprintf("column error: %v", err))
		return
	}

	// Find cursor column index for cursor value tracking
	cursorColIndex := -1
	for i, col := range cols {
		if col == plan.CursorColumn {
			cursorColIndex = i
			break
		}
	}
	if cursorColIndex == -1 {
		e.sendError(fmt.Sprintf("cursor column %q not found in result set", plan.CursorColumn))
		return
	}

	// Collect rows and track max cursor value
	var rowMaps []map[string]interface{}
	var maxCursor interface{}

	values := make([][]byte, len(cols))
	valuePtrs := make([]interface{}, len(cols))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	for rows.Next() {
		if err := rows.Scan(valuePtrs...); err != nil {
			e.sendError(fmt.Sprintf("scan error: %v", err))
			continue
		}

		// Convert row to map
		row := make(map[string]interface{})
		for i, col := range cols {
			row[col] = string(values[i])
		}
		rowMaps = append(rowMaps, row)

		// Track cursor value from this row
		maxCursor = e.parseCursorValue(plan.CursorType, string(values[cursorColIndex]))
	}

	// If we got rows, send them and update cursor
	if len(rowMaps) > 0 {
		data, err := protocol.EncodeRowBatch(rowMaps)
		if err != nil {
			e.sendError(fmt.Sprintf("encode error: %v", err))
			metrics.IncErrors()
			return
		}

		if err := e.sender.SendToQuery(e.streamID, data); err != nil {
			e.sendError(fmt.Sprintf("send error: %v", err))
			metrics.IncErrors()
			return
		}

		metrics.IncRowsSent(int64(len(rowMaps)))

		// Update cursor position for next poll (at-least-once delivery guarantee)
		e.mu.Lock()
		if maxCursor != nil {
			state.CursorValue = maxCursor
			state.RowsSent += int64(len(rowMaps))
			state.LastUpdateAt = time.Now()
		}
		e.mu.Unlock()
	}
}

// parseCursorValue converts a string cursor value to the appropriate type
func (e *Executor) parseCursorValue(cursorType string, value string) interface{} {
	normalized := strings.ToUpper(cursorType)

	if strings.Contains(normalized, "TIMESTAMP") {
		// Parse timestamp
		t, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			// Try alternative formats
			t, err = time.Parse("2006-01-02 15:04:05", value)
			if err != nil {
				return nil
			}
		}
		return t
	}

	// Parse as integer for BIGINT
	val, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return nil
	}
	return val
}

func (e *Executor) sendError(msg string) {
	_ = e.sender.SendToQuery(e.streamID, protocol.EncodeError(msg))
	metrics.IncErrors()
}

func (e *Executor) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		e.running = false
		close(e.stopCh)
	}
}

func (e *Executor) IsRunning() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.running
}

// GetState returns a copy of the current stream state
func (e *Executor) GetState() *StreamState {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.state == nil {
		return nil
	}

	// Return a copy to avoid race conditions
	stateCopy := *e.state
	return &stateCopy
}
