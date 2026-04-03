package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	DuckDBPath          string
	QUICAddr            string
	IngestAddr          string
	BatchSize           int
	BatchTimeout        time.Duration
	PollInterval        time.Duration
	MaxClients          int
	MaxStreamsPerClient int
	MaxQueries          int
}

func Default() *Config {
	return &Config{
		DuckDBPath:          getEnv("DUCKSTREAM_DUCKDB_PATH", "duckstream.db"),
		QUICAddr:            getEnv("DUCKSTREAM_QUIC_ADDR", "localhost:4242"),
		IngestAddr:          getEnv("DUCKSTREAM_INGEST_ADDR", "localhost:8080"),
		BatchSize:           getEnvInt("DUCKSTREAM_BATCH_SIZE", 100),
		BatchTimeout:        getEnvDuration("DUCKSTREAM_BATCH_TIMEOUT", time.Second),
		PollInterval:        getEnvDuration("DUCKSTREAM_POLL_INTERVAL", 100*time.Millisecond),
		MaxClients:          getEnvInt("DUCKSTREAM_MAX_CLIENTS", 100),
		MaxStreamsPerClient: getEnvInt("DUCKSTREAM_MAX_STREAMS", 10),
		MaxQueries:          getEnvInt("DUCKSTREAM_MAX_QUERIES", 50),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		var intValue int
		if _, err := fmt.Sscanf(value, "%d", &intValue); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}
