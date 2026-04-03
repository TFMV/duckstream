package query

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/duckstream/duckstream/internal/config"
	"github.com/duckstream/duckstream/internal/duckdb"
)

// StartMode determines when to start streaming
type StartMode string

const (
	StartModeBeginning StartMode = "BEGINNING"
	StartModeNow       StartMode = "NOW"
)

// StreamPlan describes how to stream a query
type StreamPlan struct {
	ID            string
	Table         string
	ProjectionSQL string
	BaseWhereSQL  string
	CursorColumn  string
	CursorType    string
	Mode          StartMode
	BatchSize     int
}

// CompileStreamPlan compiles a raw SQL query into a StreamPlan
func CompileStreamPlan(ctx context.Context, client *duckdb.Client, cfg *config.Config, queryID, rawSQL string, cursorColumnHint *string, mode StartMode) (*StreamPlan, error) {
	// Validate SQL doesn't contain forbidden constructs
	if err := validateSQL(rawSQL); err != nil {
		return nil, err
	}

	// Parse SQL to extract table, projection, and WHERE clause
	table, projection, whereClause, err := parseSQL(rawSQL)
	if err != nil {
		return nil, err
	}

	// Validate table exists
	if err := validateTableExists(ctx, client, table); err != nil {
		return nil, err
	}

	// Get table schema
	schema, err := getTableSchema(ctx, client, table)
	if err != nil {
		return nil, err
	}

	// Determine cursor column
	cursorCol, err := determineCursorColumn(schema, cursorColumnHint)
	if err != nil {
		return nil, err
	}

	// Get cursor column type
	cursorType, err := getCursorColumnType(schema, cursorCol)
	if err != nil {
		return nil, err
	}

	// Validate cursor type is BIGINT or TIMESTAMP
	if err := validateCursorType(cursorType); err != nil {
		return nil, err
	}

	return &StreamPlan{
		ID:            queryID,
		Table:         table,
		ProjectionSQL: projection,
		BaseWhereSQL:  whereClause,
		CursorColumn:  cursorCol,
		CursorType:    cursorType,
		Mode:          mode,
		BatchSize:     cfg.BatchSize,
	}, nil
}

// validateSQL checks for forbidden SQL constructs
func validateSQL(sql string) error {
	normalized := normalizeSQL(sql)

	// Check for forbidden keywords
	if hasKeywordPattern(normalized, regexp.MustCompile(`\bJOIN\b`)) {
		return ErrForbiddenJoin
	}
	if hasKeywordPattern(normalized, regexp.MustCompile(`\bGROUP\s+BY\b`)) {
		return ErrForbiddenGroupBy
	}
	if hasKeywordPattern(normalized, regexp.MustCompile(`\bDISTINCT\b`)) {
		return ErrForbiddenDistinct
	}
	if hasKeywordPattern(normalized, regexp.MustCompile(`\bOVER\b`)) {
		return ErrForbiddenWindowFunction
	}

	// Check for subqueries (nested SELECT)
	if hasSubqueries(normalized) {
		return ErrForbiddenSubquery
	}

	return nil
}

// normalizeSQL converts SQL to a consistent format for pattern matching
func normalizeSQL(sql string) string {
	// Convert to uppercase and collapse whitespace
	normalized := strings.ToUpper(sql)
	// Collapse multiple spaces
	re := regexp.MustCompile(`\s+`)
	normalized = re.ReplaceAllString(normalized, " ")
	return strings.TrimSpace(normalized)
}

// hasKeywordPattern checks if a pattern exists in the SQL
func hasKeywordPattern(normalized string, pattern *regexp.Regexp) bool {
	return pattern.MatchString(normalized)
}

// hasSubqueries checks for nested SELECT statements (simplified check)
func hasSubqueries(normalized string) bool {
	// This is a simple heuristic: look for SELECT that appears after FROM
	// and is not the top-level SELECT
	fromIdx := strings.Index(normalized, " FROM ")
	if fromIdx == -1 {
		return false
	}

	// Look for SELECT in the FROM clause or WHERE clause
	afterFrom := normalized[fromIdx+6:]
	// Count opening and closing parentheses to find nested ones
	parenDepth := 0
	for _, ch := range afterFrom {
		if ch == '(' {
			parenDepth++
			// If we find a SELECT inside parentheses, that's a subquery
			if parenDepth > 0 {
				// Check if what follows is SELECT
				remainingAfterParen := afterFrom[strings.Index(afterFrom, string(ch))+1:]
				if strings.HasPrefix(strings.TrimSpace(remainingAfterParen), "SELECT") {
					return true
				}
			}
		} else if ch == ')' {
			parenDepth--
		}
	}

	return false
}

// parseSQL extracts table name, projection, and WHERE clause
func parseSQL(sql string) (table, projection, whereClause string, err error) {
	normalized := normalizeSQL(sql)

	// Extract FROM clause
	fromIdx := strings.Index(normalized, " FROM ")
	if fromIdx == -1 {
		return "", "", "", ErrNoFromClause
	}

	// Extract projection (everything from SELECT to FROM)
	selectIdx := strings.Index(normalized, "SELECT ")
	if selectIdx == -1 {
		return "", "", "", ErrInvalidSQL
	}
	// Use normalized string indices to find positions, then extract from original
	normalizedFromIdx := strings.Index(normalized, " FROM ")
	projection = strings.TrimSpace(sql[selectIdx+7 : selectIdx+7+len(normalized[selectIdx+7:normalizedFromIdx])])

	// Find FROM in original SQL to preserve case
	fromInOriginal := strings.Index(sql, " FROM ")
	if fromInOriginal == -1 {
		return "", "", "", ErrInvalidSQL
	}

	// Extract single-table FROM target in normalized SQL
	fromSection := strings.TrimSpace(normalized[fromIdx+6:])

	// Cut off at WHERE if present
	whereIdx := strings.Index(fromSection, " WHERE ")
	if whereIdx != -1 {
		fromSection = strings.TrimSpace(fromSection[:whereIdx])
	}

	// Ensure only one table is used
	if strings.Contains(fromSection, ",") || strings.HasPrefix(fromSection, "(") {
		return "", "", "", ErrSingleTableQuery
	}

	// Extract table identifier from original SQL to preserve case
	fromSectionOriginal := strings.TrimSpace(sql[fromInOriginal+6:])
	whereIdxOriginal := strings.Index(strings.ToUpper(fromSectionOriginal), " WHERE ")
	if whereIdxOriginal != -1 {
		fromSectionOriginal = strings.TrimSpace(fromSectionOriginal[:whereIdxOriginal])
	}
	fromFields := strings.Fields(fromSectionOriginal)
	if len(fromFields) == 0 {
		return "", "", "", ErrInvalidSQL
	}
	table = fromFields[0]

	// Extract WHERE clause if present
	whereIndexClause := strings.Index(strings.ToUpper(sql), " WHERE ")
	if whereIndexClause != -1 {
		afterWhere := sql[whereIndexClause+7:]
		whereClause = strings.TrimSpace(afterWhere)
	}

	return table, projection, whereClause, nil
}

// validateTableExists checks if a table exists in DuckDB
func validateTableExists(ctx context.Context, client *duckdb.Client, tableName string) error {
	query := fmt.Sprintf("SELECT 1 FROM information_schema.tables WHERE table_name = '%s' LIMIT 1", escapeSQLString(tableName))
	row := client.DB().QueryRowContext(ctx, query)
	var exists int
	err := row.Scan(&exists)
	if err == sql.ErrNoRows {
		return fmt.Errorf("table does not exist: %s", tableName)
	}
	if err != nil {
		return fmt.Errorf("failed to check table existence: %w", err)
	}
	return nil
}

// ColumnSchema represents a column in a table
type ColumnSchema struct {
	Name     string
	Type     string
	Nullable bool
}

// getTableSchema retrieves the schema of a table via DESCRIBE
func getTableSchema(ctx context.Context, client *duckdb.Client, tableName string) ([]ColumnSchema, error) {
	// Use DESCRIBE to get schema
	query := fmt.Sprintf("DESCRIBE %s", tableName)
	rows, err := client.DB().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to describe table: %w", err)
	}
	defer rows.Close()

	var schema []ColumnSchema
	for rows.Next() {
		var col ColumnSchema
		var nullable string
		var key, defaultVal, extra interface{}
		// DESCRIBE returns columns: Column Name, Column Type, Null, Key, Default, Extra
		err := rows.Scan(&col.Name, &col.Type, &nullable, &key, &defaultVal, &extra)
		if err != nil {
			return nil, fmt.Errorf("failed to scan schema row: %w", err)
		}
		col.Nullable = nullable == "YES"
		schema = append(schema, col)
	}

	if len(schema) == 0 {
		return nil, fmt.Errorf("table has no columns")
	}

	return schema, nil
}

// determineCursorColumn selects the cursor column
func determineCursorColumn(schema []ColumnSchema, hint *string) (string, error) {
	// Use hint if provided and exists in schema
	if hint != nil {
		for _, col := range schema {
			if col.Name == *hint {
				return col.Name, nil
			}
		}
		return "", fmt.Errorf("cursor column hint '%s' not found in table", *hint)
	}

	// Prefer "id"
	for _, col := range schema {
		if col.Name == "id" {
			return col.Name, nil
		}
	}

	// Prefer "created_at"
	for _, col := range schema {
		if col.Name == "created_at" {
			return col.Name, nil
		}
	}

	return "", ErrCursorNotFound
}

// getCursorColumnType retrieves the type of the cursor column
func getCursorColumnType(schema []ColumnSchema, columnName string) (string, error) {
	for _, col := range schema {
		if col.Name == columnName {
			return col.Type, nil
		}
	}
	return "", fmt.Errorf("cursor column '%s' not found in schema", columnName)
}

// validateCursorType checks that cursor type is BIGINT or TIMESTAMP
func validateCursorType(cursorType string) error {
	normalized := strings.ToUpper(cursorType)

	// Handle various timestamp representations
	if strings.Contains(normalized, "TIMESTAMP") {
		return nil
	}

	// Handle BIGINT
	if normalized == "BIGINT" || normalized == "INT64" {
		return nil
	}

	return ErrCursorType
}

// escapeSQLString escapes a string for use in SQL
func escapeSQLString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// BuildStreamingSQL constructs the incremental polling SQL query from a StreamPlan
func BuildStreamingSQL(plan StreamPlan) string {
	var whereParts []string

	// Add base WHERE clause if present
	if plan.BaseWhereSQL != "" {
		whereParts = append(whereParts, fmt.Sprintf("(%s)", plan.BaseWhereSQL))
	} else {
		whereParts = append(whereParts, "(TRUE)")
	}

	// Add cursor condition
	whereParts = append(whereParts, fmt.Sprintf("%s > ?", plan.CursorColumn))

	// Build the complete query
	query := fmt.Sprintf(
		"SELECT %s\nFROM %s\nWHERE %s\nORDER BY %s ASC\nLIMIT ?",
		plan.ProjectionSQL,
		plan.Table,
		strings.Join(whereParts, " AND "),
		plan.CursorColumn,
	)

	return query
}

// Errors for StreamPlan compilation
var (
	ErrSingleTableQuery        = fmt.Errorf("only single-table SELECT queries are supported")
	ErrForbiddenJoin           = fmt.Errorf("JOINs are not supported in streaming queries")
	ErrForbiddenGroupBy        = fmt.Errorf("GROUP BY is not supported")
	ErrForbiddenDistinct       = fmt.Errorf("queries with DISTINCT are not supported")
	ErrForbiddenWindowFunction = fmt.Errorf("queries with window functions (OVER) are not supported")
	ErrForbiddenSubquery       = fmt.Errorf("queries with subqueries are not supported")
	ErrNoFromClause            = fmt.Errorf("query must have a FROM clause")
	ErrInvalidSQL              = fmt.Errorf("invalid SQL syntax")
	ErrCursorNotFound          = fmt.Errorf("no valid cursor column found (expected id or created_at)")
	ErrCursorType              = fmt.Errorf("cursor column must be BIGINT or TIMESTAMP")
)
