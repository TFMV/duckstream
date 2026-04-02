package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/duckstream/duckstream/internal/metrics"
	"github.com/duckstream/duckstream/internal/query"
)

type Handler struct {
	manager *query.Manager
}

func NewHandler(manager *query.Manager) *Handler {
	return &Handler{manager: manager}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /queries", h.listQueries)
	mux.HandleFunc("POST /queries", h.registerQuery)
	mux.HandleFunc("DELETE /queries/{id}", h.unregisterQuery)
	mux.HandleFunc("GET /metrics", h.metrics)
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
	ID  string `json:"id"`
	SQL string `json:"sql"`
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

	if err := h.manager.Register(r.Context(), req.ID, req.SQL); err != nil {
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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metrics.Get())
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprintf(w, "Not found: %s %s", r.Method, r.URL.Path)
}
