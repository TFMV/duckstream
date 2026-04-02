package query

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/duckstream/duckstream/internal/config"
	"github.com/duckstream/duckstream/internal/duckdb"
	"github.com/duckstream/duckstream/internal/protocol"
)

type Executor struct {
	client   *duckdb.Client
	sender   protocol.Sender
	queryID  string
	streamID string

	mu      sync.RWMutex
	running bool
	lastID  int64
	stopCh  chan struct{}
}

func NewExecutor(client *duckdb.Client, sender protocol.Sender, queryID string) *Executor {
	return &Executor{
		client:   client,
		sender:   sender,
		queryID:  queryID,
		streamID: hashToStreamID(queryID),
		running:  false,
		stopCh:   make(chan struct{}),
	}
}

func hashToStreamID(queryID string) string {
	h := sha256Hash(queryID)
	return h[:8]
}

func sha256Hash(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (e *Executor) Start(ctx context.Context, sql string) {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return
	}
	e.running = true
	e.lastID = 0
	e.stopCh = make(chan struct{})
	e.mu.Unlock()

	go e.run(ctx, sql)
}

func (e *Executor) run(ctx context.Context, sql string) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.sendError("context cancelled")
			return
		case <-e.stopCh:
			_ = e.sender.SendToStream(e.streamID, protocol.EncodeCompleted())
			return
		case <-ticker.C:
			e.poll(ctx, sql)
		}
	}
}

func (e *Executor) poll(ctx context.Context, sql string) {
	e.mu.RLock()
	lastID := e.lastID
	e.mu.RUnlock()

	query := fmt.Sprintf("SELECT * FROM (%s) WHERE id > %d ORDER BY id", sql, lastID)
	rows, err := e.client.DB().QueryContext(ctx, query)
	if err != nil {
		e.sendError(fmt.Sprintf("query error: %v", err))
		return
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return
	}

	values := make([][]byte, len(cols))
	valuePtrs := make([]interface{}, len(cols))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	var rowMaps []map[string]interface{}
	for rows.Next() {
		if err := rows.Scan(valuePtrs...); err != nil {
			continue
		}

		row := make(map[string]interface{})
		for i, col := range cols {
			row[col] = string(values[i])
		}
		rowMaps = append(rowMaps, row)

		e.mu.Lock()
		if idVal, ok := row["id"]; ok {
			if idStr, ok := idVal.(string); ok {
				if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
					if id > e.lastID {
						e.lastID = id
					}
				}
			}
		}
		e.mu.Unlock()
	}

	if len(rowMaps) > 0 {
		data, err := protocol.EncodeRowBatch(rowMaps)
		if err != nil {
			return
		}
		_ = e.sender.SendToStream(e.streamID, data)
	}
}

func (e *Executor) sendError(msg string) {
	_ = e.sender.SendToStream(e.streamID, protocol.EncodeError(msg))
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

type configUtil struct{}

func (configUtil) Default() *config.Config {
	return config.Default()
}

var _ = configUtil{}
