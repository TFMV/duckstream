package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/duckstream/duckstream/internal/duckdb"
	"github.com/duckstream/duckstream/internal/metrics"
	"github.com/duckstream/duckstream/internal/query"
)

type Handler struct {
	manager   *query.Manager
	client    *duckdb.Client
	quicStats interface{ ActiveClients() int }

	eventSubs  map[*eventSub]bool
	eventSubMu sync.RWMutex
	querySubs  map[string]map[*querySub]bool
	querySubMu sync.RWMutex
}

type eventSub struct {
	ch chan string
}

type querySub struct {
	id string
	ch chan string
}

func NewHandler(manager *query.Manager, client *duckdb.Client, quicStats interface{ ActiveClients() int }) *Handler {
	h := &Handler{
		manager:   manager,
		client:    client,
		quicStats: quicStats,
		eventSubs: make(map[*eventSub]bool),
		querySubs: make(map[string]map[*querySub]bool),
	}
	go h.broadcastLoop()
	return h
}

func (h *Handler) broadcastLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		queries := h.manager.List()
		for _, q := range queries {
			h.querySubMu.RLock()
			subs := h.querySubs[q.ID]
			h.querySubMu.RUnlock()

			for sub := range subs {
				select {
				case sub.ch <- "ping":
				default:
				}
			}
		}
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /queries", h.listQueries)
	mux.HandleFunc("POST /queries", h.registerQuery)
	mux.HandleFunc("DELETE /queries/{id}", h.unregisterQuery)
	mux.HandleFunc("GET /metrics", h.metrics)
	mux.HandleFunc("GET /ingestion/events", h.ingestionEvents)
	mux.HandleFunc("GET /stream/events", h.streamEvents)
	mux.HandleFunc("GET /stream/queries/{id}", h.streamQuery)
}

func (h *Handler) listQueries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	queries := h.manager.List()
	resp := make([]map[string]interface{}, 0, len(queries))
	for _, q := range queries {
		resp = append(resp, map[string]interface{}{
			"id":     q.ID,
			"sql":    q.SQL,
			"active": q.Active,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"queries": resp,
	})
}

type RegisterRequest struct {
	ID     string `json:"id"`
	SQL    string `json:"sql"`
	Cursor string `json:"cursor,omitempty"`
	Mode   string `json:"mode,omitempty"` // "BEGINNING" or "NOW"
}

func (h *Handler) registerQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.ID == "" || req.SQL == "" {
		http.Error(w, "id and sql are required", http.StatusBadRequest)
		return
	}

	mode := query.StartModeBeginning
	if strings.EqualFold(req.Mode, "NOW") || strings.Contains(strings.ToUpper(req.SQL), "FROM NOW") {
		mode = query.StartModeNow
	}
	cursorHint := (*string)(nil)
	if req.Cursor != "" {
		cursorHint = &req.Cursor
	}
	if err := h.manager.Register(r.Context(), req.ID, req.SQL, cursorHint, mode); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"id":     req.ID,
	})
}

func (h *Handler) unregisterQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	if err := h.manager.Unregister(context.Background(), id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"id":     id,
	})
}

func (h *Handler) metrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	m := metrics.Get()
	if h.quicStats != nil {
		m["active_clients"] = h.quicStats.ActiveClients()
	}
	m["active_queries"] = len(h.manager.List())

	// Per-query streaming metrics
	queryStates := h.manager.GetAllQueryStates()
	queryMetrics := make([]map[string]interface{}, 0, len(queryStates))
	now := time.Now()
	for id, st := range queryStates {
		var lagMs interface{}
		if ts, ok := st.CursorValue.(time.Time); ok && !ts.IsZero() {
			lagMs = now.Sub(ts).Milliseconds()
		} else {
			lagMs = nil
		}

		queryMetrics = append(queryMetrics, map[string]interface{}{
			"id":            id,
			"last_cursor":   st.CursorValue,
			"rows_streamed": st.RowsSent,
			"lag_ms":        lagMs,
		})
	}

	m["query_metrics"] = queryMetrics

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(m)
}

func (h *Handler) ingestionEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.client == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":  "client is nil",
			"events": []map[string]interface{}{},
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Query recent ingestion events (last 50)
	rows, err := h.client.DB().QueryContext(ctx, `
		SELECT table_name, data, timestamp
		FROM ingestion_events
		ORDER BY timestamp DESC
		LIMIT 50
	`)
	if err != nil {
		// For debugging, return the error
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":  err.Error(),
			"events": []map[string]interface{}{},
		})
		return
	}
	defer rows.Close()

	var events = make([]map[string]interface{}, 0)
	for rows.Next() {
		var tableName string
		var data string
		var timestamp time.Time
		if err := rows.Scan(&tableName, &data, &timestamp); err != nil {
			continue
		}
		events = append(events, map[string]interface{}{
			"table":     tableName,
			"data":      data,
			"timestamp": timestamp,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"events": events,
	})
}

func (h *Handler) streamEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Connection", "keep-alive")

	sub := &eventSub{ch: make(chan string, 100)}
	h.eventSubMu.Lock()
	h.eventSubs[sub] = true
	h.eventSubMu.Unlock()

	defer func() {
		h.eventSubMu.Lock()
		delete(h.eventSubs, sub)
		h.eventSubMu.Unlock()
	}()

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			queries := h.manager.List()
			if len(queries) > 0 {
				data := map[string]interface{}{
					"ingestion_rate": len(queries) * 10,
					"timestamp":      time.Now().Format(time.RFC3339),
				}
				payload, _ := json.Marshal(data)
				fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}
	}
}

func (h *Handler) streamQuery(w http.ResponseWriter, r *http.Request) {
	queryID := r.PathValue("id")
	if queryID == "" {
		http.Error(w, "query id required", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Connection", "keep-alive")

	sub := &querySub{id: queryID, ch: make(chan string, 100)}

	h.querySubMu.Lock()
	if h.querySubs[queryID] == nil {
		h.querySubs[queryID] = make(map[*querySub]bool)
	}
	h.querySubs[queryID][sub] = true
	h.querySubMu.Unlock()

	defer func() {
		h.querySubMu.Lock()
		delete(h.querySubs[queryID], sub)
		h.querySubMu.Unlock()
	}()

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	queries := h.manager.List()
	for _, q := range queries {
		if q.ID == queryID {
			data := map[string]interface{}{
				"id":     q.ID,
				"sql":    q.SQL,
				"active": q.Active,
				"status": "running",
			}
			payload, _ := json.Marshal(data)
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
			break
		}
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, "Not found: %s %s", r.Method, r.URL.Path)
}
