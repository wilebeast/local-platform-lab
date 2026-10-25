package risk

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type Handler struct{ store Store }

func NewHandler(store Store) http.Handler { return &Handler{store: store} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	switch {
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/customers/") && strings.HasSuffix(path, "/facts"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/customers/"), "/facts")
		var facts CustomerFacts
		if !decode(w, r, &facts) {
			return
		}
		facts.CustomerID = id
		result, err := h.store.UpsertFacts(r.Context(), facts)
		h.write(w, result, err, http.StatusOK)
	case r.Method == http.MethodPost && path == "/evaluations":
		var cmd EvaluationCommand
		if !decode(w, r, &cmd) {
			return
		}
		result, replay, err := h.store.Evaluate(r.Context(), cmd)
		if replay {
			w.Header().Set("Idempotent-Replay", "true")
		}
		h.write(w, result, err, http.StatusOK)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/evaluations/"):
		result, err := h.store.GetEvaluation(r.Context(), strings.TrimPrefix(path, "/evaluations/"))
		h.write(w, result, err, http.StatusOK)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/actions/") && strings.HasSuffix(path, "/complete"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/actions/"), "/complete")
		var cmd CompleteActionCommand
		if !decode(w, r, &cmd) {
			return
		}
		result, err := h.store.CompleteAction(r.Context(), id, cmd)
		h.write(w, result, err, http.StatusOK)
	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return false
	}
	return true
}

func (h *Handler) write(w http.ResponseWriter, result any, err error, success int) {
	if err != nil {
		status := http.StatusInternalServerError
		var conflict ConflictError
		var missing NotFoundError
		switch {
		case errors.As(err, &conflict):
			status = http.StatusConflict
		case errors.As(err, &missing):
			status = http.StatusNotFound
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(success)
	_ = json.NewEncoder(w).Encode(result)
}
