package query

import (
	"testing"
	"time"
)

func TestGetAllQueryStates(t *testing.T) {
	mgr := &Manager{queries: make(map[string]*Query)}
	mgr.queries["q1"] = &Query{
		ID: "q1",
		Executor: &Executor{
			state: &StreamState{
				QueryID:     "q1",
				CursorValue: time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC),
				RowsSent:    123,
			},
		},
	}

	states := mgr.GetAllQueryStates()
	if len(states) != 1 {
		t.Fatalf("expected 1 state, got %d", len(states))
	}

	st, ok := states["q1"]
	if !ok {
		t.Fatal("expected state for q1")
	}

	if st.RowsSent != 123 {
		t.Errorf("expected RowsSent=123, got %d", st.RowsSent)
	}
	if st.QueryID != "q1" {
		t.Errorf("expected QueryID=q1, got %s", st.QueryID)
	}
}
