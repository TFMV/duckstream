package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/duckstream/duckstream/internal/config"
	"github.com/duckstream/duckstream/internal/duckdb"
	"github.com/duckstream/duckstream/internal/metrics"
	"github.com/duckstream/duckstream/internal/protocol"
)

type Query struct {
	ID       string
	SQL      string
	Plan     *StreamPlan
	Active   bool
	Ctx      context.Context
	Cancel   context.CancelFunc
	Executor *Executor
}

type Manager struct {
	mu         sync.RWMutex
	queries    map[string]*Query
	client     *duckdb.Client
	sender     protocol.Sender
	cfg        *config.Config
	maxQueries int
}

func NewManager(client *duckdb.Client, sender protocol.Sender, cfg *config.Config) *Manager {
	return &Manager{
		queries:    make(map[string]*Query),
		client:     client,
		sender:     sender,
		cfg:        cfg,
		maxQueries: cfg.MaxQueries,
	}
}

// Register registers a new streaming query with optional cursor column hint
func (m *Manager) Register(ctx context.Context, id, sql string, cursorColumnHint *string, mode StartMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.queries) >= m.maxQueries {
		return ErrTooManyQueries
	}

	if _, exists := m.queries[id]; exists {
		return ErrQueryExists
	}

	// Compile the query into a StreamPlan
	plan, err := CompileStreamPlan(ctx, m.client, m.cfg, id, sql, cursorColumnHint, mode)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidQuery, err)
	}

	queryCtx, cancel := context.WithCancel(context.Background())
	exec := NewExecutor(m.client, m.sender, id, m.cfg.PollInterval)
	exec.Start(queryCtx, plan)

	m.queries[id] = &Query{
		ID:       id,
		SQL:      sql,
		Plan:     plan,
		Active:   true,
		Ctx:      queryCtx,
		Cancel:   cancel,
		Executor: exec,
	}

	metrics.IncQueriesRegistered()
	return nil
}

func (m *Manager) Unregister(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	q, exists := m.queries[id]
	if !exists {
		return ErrQueryNotFound
	}

	q.Cancel()
	q.Executor.Stop()
	q.Active = false
	delete(m.queries, id)

	metrics.IncQueriesUnregistered()
	return nil
}

func (m *Manager) List() []*Query {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]*Query, 0, len(m.queries))
	for _, q := range m.queries {
		list = append(list, q)
	}
	return list
}

func (m *Manager) GetStreamID(queryID string) string {
	h := sha256.New()
	h.Write([]byte(queryID))
	return hex.EncodeToString(h.Sum(nil))[:8]
}

// GetQueryState returns the current streaming state of a query
func (m *Manager) GetQueryState(queryID string) *StreamState {
	m.mu.RLock()
	query, exists := m.queries[queryID]
	m.mu.RUnlock()

	if !exists || query.Executor == nil {
		return nil
	}

	return query.Executor.GetState()
}

func (m *Manager) GetAllQueryStates() map[string]*StreamState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	states := make(map[string]*StreamState, len(m.queries))
	for id, query := range m.queries {
		if query.Executor == nil {
			continue
		}
		state := query.Executor.GetState()
		if state != nil {
			states[id] = state
		}
	}

	return states
}

var (
	ErrQueryExists    = &queryError{"query already exists"}
	ErrQueryNotFound  = &queryError{"query not found"}
	ErrInvalidQuery   = &queryError{"invalid query"}
	ErrTooManyQueries = &queryError{"max queries reached"}
)

type queryError struct {
	msg string
}

func (e *queryError) Error() string {
	return e.msg
}
