package repl

import (
	"testing"

	"github.com/duckstream/duckstream/internal/query"
)

func TestParseRegisterCommand(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantID     string
		wantSQL    string
		wantCursor *string
		wantMode   query.StartMode
		wantOK     bool
	}{
		{
			name:       "simple register",
			line:       "REGISTER QUERY q1 AS SELECT * FROM sales",
			wantID:     "q1",
			wantSQL:    "SELECT * FROM sales",
			wantCursor: nil,
			wantMode:   query.StartModeBeginning,
			wantOK:     true,
		},
		{
			name:       "register with cursor",
			line:       "REGISTER QUERY q2 AS SELECT * FROM sales WITH CURSOR created_at",
			wantID:     "q2",
			wantSQL:    "SELECT * FROM sales",
			wantCursor: strPtr("created_at"),
			wantMode:   query.StartModeBeginning,
			wantOK:     true,
		},
		{
			name:       "register from now",
			line:       "REGISTER QUERY q3 AS SELECT * FROM sales FROM NOW",
			wantID:     "q3",
			wantSQL:    "SELECT * FROM sales",
			wantCursor: nil,
			wantMode:   query.StartModeNow,
			wantOK:     true,
		},
		{
			name:       "register with cursor and from now",
			line:       "REGISTER QUERY q4 AS SELECT * FROM sales WITH CURSOR created_at FROM NOW",
			wantID:     "q4",
			wantSQL:    "SELECT * FROM sales",
			wantCursor: strPtr("created_at"),
			wantMode:   query.StartModeNow,
			wantOK:     true,
		},
		{
			name:   "invalid register command",
			line:   "REGISTER q bad",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		id, sql, cursorHint, mode, ok := parseRegisterCommand(tt.line)
		if ok != tt.wantOK {
			t.Fatalf("%s: expected ok=%v got %v", tt.name, tt.wantOK, ok)
		}
		if !ok {
			continue
		}

		if id != tt.wantID {
			t.Errorf("%s: id expected %q got %q", tt.name, tt.wantID, id)
		}
		if sql != tt.wantSQL {
			t.Errorf("%s: sql expected %q got %q", tt.name, tt.wantSQL, sql)
		}
		if (cursorHint == nil) != (tt.wantCursor == nil) {
			t.Errorf("%s: cursorHint expected %v got %v", tt.name, tt.wantCursor, cursorHint)
		} else if cursorHint != nil && *cursorHint != *tt.wantCursor {
			t.Errorf("%s: cursorHint expected %q got %q", tt.name, *tt.wantCursor, *cursorHint)
		}
		if mode != tt.wantMode {
			t.Errorf("%s: mode expected %v got %v", tt.name, tt.wantMode, mode)
		}
	}
}

func strPtr(s string) *string { return &s }
