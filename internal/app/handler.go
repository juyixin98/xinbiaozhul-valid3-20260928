// Package app implements the small business layer exercised over the
// strict HTTP/1.1 parser: a JSON record CRUD surface plus health. It
// deliberately contains no framing code.
package app

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"http11subset/internal/httpx"
	"http11subset/internal/storage"
)

// Handler is the application HTTP handler.
type Handler struct {
	Store *storage.Store
}

// New constructs a Handler.
func New(s *storage.Store) *Handler { return &Handler{Store: s} }

type createRequest struct {
	Title   string `json:"title"`
	Payload string `json:"payload"`
}

type recordResponse struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Payload string `json:"payload"`
}

// ServeHTTP implements httpx.Handler.
func (h *Handler) ServeHTTP(w httpx.ResponseWriter, r *httpx.Request) {
	switch {
	case r.Target == "/healthz" || strings.HasPrefix(r.Target, "/healthz?"):
		h.health(w, r)
	case r.Target == "/v1/records" || strings.HasPrefix(r.Target, "/v1/records?"):
		if r.Method == "POST" {
			h.create(w, r)
			return
		}
		writeError(w, 405, "method_not_allowed", "use POST /v1/records")
	case strings.HasPrefix(r.Target, "/v1/records/"):
		if r.Method == "GET" {
			h.get(w, r)
			return
		}
		writeError(w, 405, "method_not_allowed", "use GET /v1/records/{id}")
	default:
		writeError(w, 404, "not_found", "unknown route")
	}
}

func (h *Handler) health(w httpx.ResponseWriter, r *httpx.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method_not_allowed", "use GET /healthz")
		return
	}
	n, err := h.Store.Count()
	if err != nil {
		writeError(w, 500, "storage_error", err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "records": n})
}

func (h *Handler) create(w httpx.ResponseWriter, r *httpx.Request) {
	if ct, _ := r.Header.Get("Content-Type"); ct != "" &&
		!strings.Contains(strings.ToLower(ct), "application/json") {
		writeError(w, 415, "unsupported_media_type", "send application/json")
		return
	}
	var in createRequest
	if err := json.Unmarshal(r.Body, &in); err != nil {
		writeError(w, 400, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		writeError(w, 400, "invalid_request", "title is required")
		return
	}
	id, err := h.Store.Create(in.Title, in.Payload)
	if err != nil {
		writeError(w, 500, "storage_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(201)
	w.Header().Set("Location", "/v1/records/"+strconv.FormatInt(id, 10))
	writeJSON(w, 201, map[string]any{"id": id, "title": in.Title})
}

func (h *Handler) get(w httpx.ResponseWriter, r *httpx.Request) {
	idStr := strings.TrimPrefix(strings.SplitN(r.Target, "?", 2)[0], "/v1/records/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "invalid_id", "id must be a positive integer")
		return
	}
	rec, err := h.Store.Get(id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, 404, "not_found", "no record with id "+idStr)
			return
		}
		writeError(w, 500, "storage_error", err.Error())
		return
	}
	writeJSON(w, 200, recordResponse{ID: rec.ID, Title: rec.Title, Payload: rec.Payload})
}

func writeJSON(w httpx.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status = 500
		b = []byte(`{"error":"json_error"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(b)
}

func writeError(w httpx.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": code, "message": msg})
}
