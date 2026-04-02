package duckdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/duckdb/duckdb-go/v2"
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

type Event struct {
	ID        int64
	Data      string
	Timestamp time.Time
}

func (c *Client) InsertEvents(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}

	conn, err := c.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("get conn: %w", err)
	}
	defer conn.Close()

	var appendErr error
	err = conn.Raw(func(driverConn any) error {
		dc, ok := driverConn.(driver.Conn)
		if !ok {
			return fmt.Errorf("not a driver.Conn")
		}
		appender, err := duckdb.NewAppenderFromConn(dc, "", "events")
		if err != nil {
			return fmt.Errorf("create appender: %w", err)
		}
		defer appender.Close()

		for _, e := range events {
			if err := appender.AppendRow(e.Data, e.Timestamp); err != nil {
				return fmt.Errorf("append row: %w", err)
			}
		}

		if err := appender.Flush(); err != nil {
			return fmt.Errorf("flush: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("raw: %w", err)
	}

	return appendErr
}

func (c *Client) MaxEventID(ctx context.Context) (int64, error) {
	var id int64
	err := c.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM events").Scan(&id)
	return id, err
}
