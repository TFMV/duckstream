package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"github.com/duckstream/duckstream/internal/duckdb"
	"github.com/duckstream/duckstream/internal/protocol"
)

type Query struct {
	ID       string
	SQL      string
	Active   bool
	Ctx      context.Context
	Cancel   context.CancelFunc
	Executor *Executor
}

type Manager struct {
	mu      sync.RWMutex
	queries map[string]*Query
	client  *duckdb.Client
	sender  protocol.Sender
}

func NewManager(client *duckdb.Client, sender protocol.Sender) *Manager {
	return &Manager{
		queries: make(map[string]*Query),
		client:  client,
		sender:  sender,
	}
}

func (m *Manager) Register(ctx context.Context, id, sql string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.queries[id]; exists {
		return ErrQueryExists
	}

	queryCtx, cancel := context.WithCancel(ctx)
	exec := NewExecutor(m.client, m.sender, id)
	exec.Start(queryCtx, sql)

	m.queries[id] = &Query{
		ID:       id,
		SQL:      sql,
		Active:   true,
		Ctx:      queryCtx,
		Cancel:   cancel,
		Executor: exec,
	}

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

var (
	ErrQueryExists   = &queryError{"query already exists"}
	ErrQueryNotFound = &queryError{"query not found"}
)

type queryError struct {
	msg string
}

func (e *queryError) Error() string {
	return e.msg
}
