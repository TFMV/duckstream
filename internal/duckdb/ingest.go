package duckdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/duckstream/duckstream/internal/config"
)

type IngestHandler struct {
	client *Client
	cfg    *config.Config

	mu        sync.Mutex
	buffer    []Event
	lastFlush time.Time
}

func NewIngestHandler(client *Client, cfg *config.Config) *IngestHandler {
	return &IngestHandler{
		client:    client,
		cfg:       cfg,
		buffer:    make([]Event, 0, cfg.BatchSize),
		lastFlush: time.Now(),
	}
}

type IngestRequest struct {
	Data string `json:"data"`
}

func (h *IngestHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/ingest")
	if path == "" || path == "/" {
		// Default events endpoint
		var req IngestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		event := Event{
			Data:      req.Data,
			Timestamp: time.Now(),
		}

		h.mu.Lock()
		h.buffer = append(h.buffer, event)
		shouldFlush := len(h.buffer) >= h.cfg.BatchSize || time.Since(h.lastFlush) >= h.cfg.BatchTimeout
		h.mu.Unlock()

		if shouldFlush {
			h.flush()
		}

		// Log ingestion event
		if err := h.client.LogIngestionEvent(r.Context(), "events", req.Data); err != nil {
			// Log error but don't fail the request
			fmt.Printf("Failed to log ingestion event: %v\n", err)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}

	// Table-targeted ingest: /ingest/<table>
	table := strings.TrimPrefix(path, "/")
	if table == "" {
		http.Error(w, "table name required", http.StatusBadRequest)
		return
	}

	var req IngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.client.InsertRow(ctx, table, req.Data); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Log ingestion event
	if err := h.client.LogIngestionEvent(ctx, table, req.Data); err != nil {
		// Log error but don't fail the request
		fmt.Printf("Failed to log ingestion event: %v\n", err)
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (h *IngestHandler) flush() {
	h.mu.Lock()
	buffer := h.buffer
	h.buffer = make([]Event, 0, h.cfg.BatchSize)
	h.lastFlush = time.Now()
	h.mu.Unlock()

	if len(buffer) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := h.client.InsertEvents(ctx, buffer); err != nil {
		println("flush error:", err.Error())
	}
}

func (h *IngestHandler) Flush() {
	h.flush()
}
