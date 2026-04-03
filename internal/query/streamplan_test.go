package query

import (
	"strings"
	"testing"
	"time"
)

// TestValidateSQL tests the SQL validation function
func TestValidateSQL(t *testing.T) {
	tests := []struct {
		sql       string
		shouldErr bool
	}{
		{"SELECT * FROM users", false},
		{"SELECT id, name FROM users WHERE age > 18", false},
		{"SELECT * FROM a JOIN b ON a.id = b.id", true},
		{"SELECT * FROM users GROUP BY id", true},
		{"SELECT DISTINCT id FROM users", true},
		{"SELECT *, ROW_NUMBER() OVER (ORDER BY id) FROM users", true},
		{"SELECT * FROM (SELECT * FROM users)", true},
	}

	for _, tt := range tests {
		err := validateSQL(tt.sql)
		if tt.shouldErr {
			if err == nil {
				t.Errorf("validateSQL(%q) should error but didn't", tt.sql)
				continue
			}
			if tt.sql == "SELECT * FROM a JOIN b ON a.id = b.id" && err != ErrForbiddenJoin {
				t.Errorf("validateSQL(%q) expected ErrForbiddenJoin but got %v", tt.sql, err)
			}
			if tt.sql == "SELECT * FROM users GROUP BY id" && err != ErrForbiddenGroupBy {
				t.Errorf("validateSQL(%q) expected ErrForbiddenGroupBy but got %v", tt.sql, err)
			}
		} else {
			if err != nil {
				t.Errorf("validateSQL(%q) should not error but got: %v", tt.sql, err)
			}
		}
	}
}

// TestParseSQL tests SQL parsing
func TestParseSQL(t *testing.T) {
	tests := []struct {
		sql           string
		expectedTable string
		shouldErr     bool
	}{
		{
			sql:           "SELECT * FROM events",
			expectedTable: "events",
			shouldErr:     false,
		},
		{
			sql:           "SELECT id, data FROM events WHERE id > 100",
			expectedTable: "events",
			shouldErr:     false,
		},
	}

	for _, tt := range tests {
		table, _, _, err := parseSQL(tt.sql)
		if tt.shouldErr {
			if err == nil {
				t.Errorf("parseSQL(%q) should error but didn't", tt.sql)
			}
			if tt.sql == "SELECT * FROM a, b" && err != ErrSingleTableQuery {
				t.Errorf("parseSQL(%q) expected ErrSingleTableQuery but got %v", tt.sql, err)
			}
		} else {
			if err != nil {
				t.Errorf("parseSQL(%q) failed: %v", tt.sql, err)
			}
			if table != tt.expectedTable {
				t.Errorf("parseSQL(%q) expected table %q, got %q", tt.sql, tt.expectedTable, table)
			}
		}
	}
}

// TestNormalizeSQL tests SQL normalization
func TestNormalizeSQL(t *testing.T) {
	tests := []struct {
		sql      string
		expected string
	}{
		{
			sql:      "SELECT * FROM events",
			expected: "SELECT * FROM EVENTS",
		},
		{
			sql:      "SELECT  *  FROM  events  WHERE  id > 100",
			expected: "SELECT * FROM EVENTS WHERE ID > 100",
		},
	}

	for _, tt := range tests {
		result := normalizeSQL(tt.sql)
		if result != tt.expected {
			t.Errorf("normalizeSQL(%q) expected %q, got %q", tt.sql, tt.expected, result)
		}
	}
}

// TestHasSubqueries tests subquery detection
func TestHasSubqueries(t *testing.T) {
	tests := []struct {
		sql      string
		expected bool
	}{
		{"SELECT * FROM EVENTS", false},
		{"SELECT * FROM (SELECT * FROM EVENTS) AS SQ", true},
		{"SELECT * FROM EVENTS WHERE ID IN (SELECT ID FROM OTHER)", true},
	}

	for _, tt := range tests {
		normalized := normalizeSQL(tt.sql)
		result := hasSubqueries(normalized)
		if result != tt.expected {
			t.Errorf("hasSubqueries(%q) expected %v, got %v", tt.sql, tt.expected, result)
		}
	}
}

// TestValidateCursorType tests cursor type validation
func TestValidateCursorType(t *testing.T) {
	tests := []struct {
		cursorType string
		shouldErr  bool
	}{
		{"BIGINT", false},
		{"INT64", false},
		{"TIMESTAMP", false},
		{"TIMESTAMP WITH TIME ZONE", false},
		{"VARCHAR", true},
		{"STRING", true},
		{"INT", true},
	}

	for _, tt := range tests {
		err := validateCursorType(tt.cursorType)
		if tt.shouldErr {
			if err == nil {
				t.Errorf("validateCursorType(%q) should error but didn't", tt.cursorType)
			} else if err != ErrCursorType {
				t.Errorf("validateCursorType(%q) expected ErrCursorType but got: %v", tt.cursorType, err)
			}
		}
		if !tt.shouldErr && err != nil {
			t.Errorf("validateCursorType(%q) should not error but got: %v", tt.cursorType, err)
		}
	}
}

// TestDetermineCursorColumn tests cursor column selection
func TestDetermineCursorColumn(t *testing.T) {
	schema := []ColumnSchema{
		{Name: "id", Type: "BIGINT", Nullable: false},
		{Name: "name", Type: "VARCHAR", Nullable: false},
		{Name: "created_at", Type: "TIMESTAMP", Nullable: false},
	}

	tests := []struct {
		hint        *string
		expectedCol string
		shouldErr   bool
		description string
	}{
		{
			hint:        nil,
			expectedCol: "id",
			shouldErr:   false,
			description: "should prefer id when no hint",
		},
		{
			hint:        strPtr("created_at"),
			expectedCol: "created_at",
			shouldErr:   false,
			description: "should use hint when provided",
		},
	}

	for _, tt := range tests {
		result, err := determineCursorColumn(schema, tt.hint)
		if tt.shouldErr {
			if err == nil {
				t.Errorf("%s: expected error but got none", tt.description)
			}
		} else {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", tt.description, err)
			}
			if result != tt.expectedCol {
				t.Errorf("%s: expected %q, got %q", tt.description, tt.expectedCol, result)
			}
		}
	}
}

func TestDetermineCursorColumnNotFound(t *testing.T) {
	schema := []ColumnSchema{
		{Name: "name", Type: "VARCHAR", Nullable: false},
		{Name: "email", Type: "VARCHAR", Nullable: false},
	}

	_, err := determineCursorColumn(schema, nil)
	if err != ErrCursorNotFound {
		t.Fatalf("expected ErrCursorNotFound, got %v", err)
	}
}

// TestGetCursorColumnType tests cursor column type lookup
func TestGetCursorColumnType(t *testing.T) {
	schema := []ColumnSchema{
		{Name: "id", Type: "BIGINT", Nullable: false},
		{Name: "created_at", Type: "TIMESTAMP", Nullable: false},
	}

	tests := []struct {
		columnName   string
		expectedType string
		shouldErr    bool
	}{
		{"id", "BIGINT", false},
		{"created_at", "TIMESTAMP", false},
		{"nonexistent", "", true},
	}

	for _, tt := range tests {
		result, err := getCursorColumnType(schema, tt.columnName)
		if tt.shouldErr {
			if err == nil {
				t.Errorf("getCursorColumnType(%q) should error", tt.columnName)
			}
		} else {
			if err != nil {
				t.Errorf("getCursorColumnType(%q) failed: %v", tt.columnName, err)
			}
			if result != tt.expectedType {
				t.Errorf("getCursorColumnType(%q) expected %q, got %q", tt.columnName, tt.expectedType, result)
			}
		}
	}
}

// Helper function to create string pointer
func strPtr(s string) *string {
	return &s
}

// TestStartMode tests StartMode constants
func TestStartMode(t *testing.T) {
	if StartModeBeginning != "BEGINNING" {
		t.Error("StartModeBeginning constant is incorrect")
	}
	if StartModeNow != "NOW" {
		t.Error("StartModeNow constant is incorrect")
	}
}

// TestStreamPlanStructure tests that StreamPlan has all required fields
func TestStreamPlanStructure(t *testing.T) {
	plan := &StreamPlan{
		ID:            "test-query",
		Table:         "events",
		ProjectionSQL: "*",
		BaseWhereSQL:  "id > 100",
		CursorColumn:  "id",
		CursorType:    "BIGINT",
		Mode:          StartModeBeginning,
		BatchSize:     100,
	}

	if plan.ID != "test-query" {
		t.Error("StreamPlan.ID not set correctly")
	}
	if plan.Table != "events" {
		t.Error("StreamPlan.Table not set correctly")
	}
	if plan.ProjectionSQL != "*" {
		t.Error("StreamPlan.ProjectionSQL not set correctly")
	}
	if plan.BaseWhereSQL != "id > 100" {
		t.Error("StreamPlan.BaseWhereSQL not set correctly")
	}
	if plan.CursorColumn != "id" {
		t.Error("StreamPlan.CursorColumn not set correctly")
	}
	if plan.CursorType != "BIGINT" {
		t.Error("StreamPlan.CursorType not set correctly")
	}
	if plan.Mode != StartModeBeginning {
		t.Error("StreamPlan.Mode not set correctly")
	}
	if plan.BatchSize != 100 {
		t.Error("StreamPlan.BatchSize not set correctly")
	}
}

// TestBuildStreamingSQL tests the streaming SQL builder
func TestBuildStreamingSQL(t *testing.T) {
	tests := []struct {
		name     string
		plan     StreamPlan
		expected string
	}{
		{
			name: "simple query with no WHERE clause",
			plan: StreamPlan{
				ID:            "q1",
				Table:         "events",
				ProjectionSQL: "*",
				BaseWhereSQL:  "",
				CursorColumn:  "id",
				CursorType:    "BIGINT",
				Mode:          StartModeBeginning,
				BatchSize:     100,
			},
			expected: "SELECT *\nFROM events\nWHERE (TRUE) AND id > ?\nORDER BY id ASC\nLIMIT ?",
		},
		{
			name: "query with WHERE clause",
			plan: StreamPlan{
				ID:            "q2",
				Table:         "events",
				ProjectionSQL: "id, data",
				BaseWhereSQL:  "json_extract(data, '$.type') = 'click'",
				CursorColumn:  "id",
				CursorType:    "BIGINT",
				Mode:          StartModeBeginning,
				BatchSize:     100,
			},
			expected: "SELECT id, data\nFROM events\nWHERE (json_extract(data, '$.type') = 'click') AND id > ?\nORDER BY id ASC\nLIMIT ?",
		},
		{
			name: "query with TIMESTAMP cursor",
			plan: StreamPlan{
				ID:            "q3",
				Table:         "users",
				ProjectionSQL: "*",
				BaseWhereSQL:  "status = 'active'",
				CursorColumn:  "created_at",
				CursorType:    "TIMESTAMP",
				Mode:          StartModeBeginning,
				BatchSize:     50,
			},
			expected: "SELECT *\nFROM users\nWHERE (status = 'active') AND created_at > ?\nORDER BY created_at ASC\nLIMIT ?",
		},
		{
			name: "complex projection",
			plan: StreamPlan{
				ID:            "q4",
				Table:         "transactions",
				ProjectionSQL: "id, user_id, amount, created_at, json_extract(metadata, '$.category') as category",
				BaseWhereSQL:  "amount > 100",
				CursorColumn:  "id",
				CursorType:    "BIGINT",
				Mode:          StartModeNow,
				BatchSize:     200,
			},
			expected: "SELECT id, user_id, amount, created_at, json_extract(metadata, '$.category') as category\nFROM transactions\nWHERE (amount > 100) AND id > ?\nORDER BY id ASC\nLIMIT ?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildStreamingSQL(tt.plan)
			if result != tt.expected {
				t.Errorf("BuildStreamingSQL() mismatch:\nExpected:\n%s\n\nGot:\n%s", tt.expected, result)
			}
		})
	}
}

// TestBuildStreamingSQL_OutputStructure verifies the SQL query structure
func TestBuildStreamingSQL_OutputStructure(t *testing.T) {
	plan := StreamPlan{
		ID:            "test",
		Table:         "events",
		ProjectionSQL: "id, data",
		BaseWhereSQL:  "type = 'click'",
		CursorColumn:  "id",
		CursorType:    "BIGINT",
		Mode:          StartModeBeginning,
		BatchSize:     100,
	}

	sql := BuildStreamingSQL(plan)

	// Verify it contains required keywords
	if !contains(sql, "SELECT") {
		t.Error("Query missing SELECT keyword")
	}
	if !contains(sql, "FROM events") {
		t.Error("Query missing FROM clause with table name")
	}
	if !contains(sql, "WHERE") {
		t.Error("Query missing WHERE clause")
	}
	if !contains(sql, "ORDER BY id ASC") {
		t.Error("Query missing ORDER BY clause")
	}
	if !contains(sql, "LIMIT ?") {
		t.Error("Query missing LIMIT with placeholder")
	}

	// Verify cursor condition with placeholder
	if !contains(sql, "id > ?") {
		t.Error("Query missing cursor condition with placeholder")
	}

	// Verify WHERE clause wrapping
	if !contains(sql, "(type = 'click')") {
		t.Error("WHERE clause should be wrapped in parentheses")
	}

	if !contains(sql, "AND") {
		t.Error("Query missing AND between WHERE and cursor condition")
	}
}

// TestBuildStreamingSQL_NoWhereTRUE verifies that empty WHERE becomes TRUE
func TestBuildStreamingSQL_NoWhereTRUE(t *testing.T) {
	plan := StreamPlan{
		Table:         "events",
		ProjectionSQL: "*",
		BaseWhereSQL:  "",
		CursorColumn:  "id",
		CursorType:    "BIGINT",
	}

	sql := BuildStreamingSQL(plan)

	if !contains(sql, "(TRUE)") {
		t.Error("Empty WHERE clause should become (TRUE)")
	}
}

// TestBuildStreamingSQL_ParameterPlaceholders verifies placeholders
func TestBuildStreamingSQL_ParameterPlaceholders(t *testing.T) {
	plan := StreamPlan{
		Table:         "events",
		ProjectionSQL: "*",
		BaseWhereSQL:  "id > 100",
		CursorColumn:  "id",
		CursorType:    "BIGINT",
	}

	sql := BuildStreamingSQL(plan)

	// Count the number of ? placeholders - should be exactly 2
	count := countOccurrences(sql, "?")
	if count != 2 {
		t.Errorf("Expected 2 parameter placeholders, got %d", count)
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// Helper function to count occurrences of a string
func countOccurrences(s, substr string) int {
	count := 0
	for i := 0; i < len(s); {
		if idx := strings.Index(s[i:], substr); idx >= 0 {
			count++
			i += idx + len(substr)
		} else {
			break
		}
	}
	return count
}

// TestStreamStateStructure tests that StreamState has all required fields
func TestStreamStateStructure(t *testing.T) {
	state := &StreamState{
		QueryID:       "test-query",
		CursorValue:   int64(100),
		RowsSent:      500,
		ErrorCount:    2,
		LastUpdateAt:  time.Now(),
		InitializedAt: time.Now(),
	}

	if state.QueryID != "test-query" {
		t.Error("StreamState.QueryID not set correctly")
	}
	if state.CursorValue != int64(100) {
		t.Error("StreamState.CursorValue not set correctly")
	}
	if state.RowsSent != 500 {
		t.Error("StreamState.RowsSent not set correctly")
	}
	if state.ErrorCount != 2 {
		t.Error("StreamState.ErrorCount not set correctly")
	}
}

// TestStreamStateBigintCursor tests StreamState with BIGINT cursor
func TestStreamStateBigintCursor(t *testing.T) {
	state := &StreamState{
		QueryID:     "test",
		CursorValue: int64(0), // Initial value
	}

	if cursorVal, ok := state.CursorValue.(int64); !ok || cursorVal != 0 {
		t.Error("StreamState should support int64 cursor values")
	}

	// Simulate advancement
	state.CursorValue = int64(1000)
	if cursorVal, ok := state.CursorValue.(int64); !ok || cursorVal != 1000 {
		t.Error("StreamState cursor value should advance for BIGINT")
	}
}

// TestStreamStateTimestampCursor tests StreamState with TIMESTAMP cursor
func TestStreamStateTimestampCursor(t *testing.T) {
	now := time.Now()
	state := &StreamState{
		QueryID:     "test",
		CursorValue: now,
	}

	if cursorVal, ok := state.CursorValue.(time.Time); !ok {
		t.Error("StreamState should support time.Time cursor values")
	} else {
		if cursorVal != now {
			t.Error("StreamState cursor value mismatch for TIMESTAMP")
		}
	}
}

// TestStreamStateRowsMetrics tests that StreamState tracks metrics
func TestStreamStateRowsMetrics(t *testing.T) {
	state := &StreamState{
		QueryID:    "test",
		RowsSent:   0,
		ErrorCount: 0,
	}

	// Simulate sending rows
	state.RowsSent += 100
	if state.RowsSent != 100 {
		t.Error("StreamState should track rows sent")
	}

	// Simulate errors
	state.ErrorCount++
	if state.ErrorCount != 1 {
		t.Error("StreamState should track error count")
	}
}

// TestStartModeConstants tests that StartMode constants are correctly defined
func TestStartModeConstants(t *testing.T) {
	if StartModeBeginning != "BEGINNING" {
		t.Error("StartModeBeginning constant is incorrect")
	}
	if StartModeNow != "NOW" {
		t.Error("StartModeNow constant is incorrect")
	}
}

// TestStreamStateTiming tests initialization and update timestamps
func TestStreamStateTiming(t *testing.T) {
	before := time.Now()
	state := &StreamState{
		QueryID:       "test",
		InitializedAt: time.Now(),
	}
	after := time.Now()

	if state.InitializedAt.Before(before) {
		t.Error("InitializedAt should be after test start")
	}
	if state.InitializedAt.After(after.Add(time.Second)) {
		t.Error("InitializedAt should be close to current time")
	}

	// Simulate update
	state.LastUpdateAt = time.Now()
	if state.LastUpdateAt.IsZero() {
		t.Error("LastUpdateAt should be set")
	}
}
