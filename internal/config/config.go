package config

import "time"

type Config struct {
	DuckDBPath   string
	QUICAddr     string
	IngestAddr   string
	BatchSize    int
	BatchTimeout time.Duration
	PollInterval time.Duration
}

func Default() *Config {
	return &Config{
		DuckDBPath:   "duckstream.db",
		QUICAddr:     "localhost:4242",
		IngestAddr:   "localhost:8080",
		BatchSize:    100,
		BatchTimeout: time.Second,
		PollInterval: 100 * time.Millisecond,
	}
}
