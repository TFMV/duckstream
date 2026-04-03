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
