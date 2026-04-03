package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

type Client struct {
	db *sql.DB
}

func NewClient(path string) (*Client, error) {
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping duckdb: %w", err)
	}

	if err := initSchema(ctx, db); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return &Client{db: db}, nil
}

func initSchema(ctx context.Context, db *sql.DB) error {
	schema := `
	CREATE SEQUENCE IF NOT EXISTS events_id_seq;
	CREATE TABLE IF NOT EXISTS events (
		id BIGINT DEFAULT (nextval('events_id_seq')),
		data JSON,
		timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (id)
	);
	`
	_, err := db.ExecContext(ctx, schema)
	return err
}

func (c *Client) DB() *sql.DB {
	return c.db
}

func (c *Client) Close() error {
	return c.db.Close()
}

func (c *Client) Exec(ctx context.Context, sql string) error {
	_, err := c.db.ExecContext(ctx, sql)
	return err
}

type Event struct {
	ID        int64
	Data      string
	Timestamp time.Time
}

func (c *Client) InsertEvents(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, "INSERT INTO events (data, timestamp) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, e := range events {
		if _, err := stmt.ExecContext(ctx, e.Data, e.Timestamp); err != nil {
			return fmt.Errorf("insert: %w", err)
		}
	}

	return tx.Commit()
}

func (c *Client) MaxEventID(ctx context.Context) (int64, error) {
	var id int64
	err := c.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM events").Scan(&id)
	return id, err
}

func (c *Client) hasTable(ctx context.Context, tableName string) (bool, error) {
	var exists int
	query := "SELECT 1 FROM information_schema.tables WHERE table_name = ? LIMIT 1"
	row := c.db.QueryRowContext(ctx, query, tableName)
	if err := row.Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return exists == 1, nil
}

func (c *Client) tableColumns(ctx context.Context, tableName string) (map[string]bool, error) {
	query := "SELECT column_name FROM information_schema.columns WHERE table_name = ?"
	rows, err := c.db.QueryContext(ctx, query, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			return nil, err
		}
		cols[col] = true
	}
	return cols, nil
}

// InsertRow inserts a row into any supported table for generic ingestion.
// Table must have at least a 'data' column of type JSON (or compatible) and optionally a 'timestamp' column.
func (c *Client) InsertRow(ctx context.Context, tableName, data string) error {
	ok, err := c.hasTable(ctx, tableName)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("table does not exist: %s", tableName)
	}

	cols, err := c.tableColumns(ctx, tableName)
	if err != nil {
		return err
	}

	if _, ok := cols["data"]; !ok {
		return fmt.Errorf("table %s does not contain required column 'data'", tableName)
	}

	var insertSQL string
	var params []interface{}
	if _, hasTimestamp := cols["timestamp"]; hasTimestamp {
		insertSQL = fmt.Sprintf("INSERT INTO %s (data, timestamp) VALUES (?, ?)", tableName)
		params = []interface{}{data, time.Now()}
	} else {
		insertSQL = fmt.Sprintf("INSERT INTO %s (data) VALUES (?)", tableName)
		params = []interface{}{data}
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	if _, err := stmt.ExecContext(ctx, params...); err != nil {
		return fmt.Errorf("insert: %w", err)
	}

	return tx.Commit()
}
