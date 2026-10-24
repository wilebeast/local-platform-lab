package card

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type Handler struct {
	store Store
	now   func() time.Time
	newID func() string
}

func NewHandler(store Store, now func() time.Time, newID func() string) http.Handler {
	return &Handler{store: store, now: now, newID: newID}
}

type applyRequest struct {
	OrganizationID string `json:"organization_id"`
	HolderID       string `json:"holder_id"`
	Alias          string `json:"alias"`
}

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	switch {
	case request.Method == http.MethodPost && isCollectionPath(request.URL.Path):
		h.apply(response, request)
	case request.Method == http.MethodGet && isCollectionPath(request.URL.Path):
		h.list(response, request)
	default:
		http.NotFound(response, request)
	}
}

func isCollectionPath(path string) bool {
	return path == "/api/v1/enterprise-cards" || path == "/api/v3/card-lab/cards"
}

func (h *Handler) apply(response http.ResponseWriter, request *http.Request) {
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	var input applyRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if idempotencyKey == "" || decoder.Decode(&input) != nil || input.OrganizationID == "" || input.HolderID == "" || input.Alias == "" {
		writeError(response, http.StatusBadRequest, "idempotency key, organization_id, holder_id and alias are required")
		return
	}
	card, err := h.store.Apply(request.Context(), ApplyParams{
		IdempotencyKey: idempotencyKey,
		Card: Card{
			ID:             h.newID(),
			OrganizationID: input.OrganizationID,
			HolderID:       input.HolderID,
			Alias:          input.Alias,
			Status:         "pending",
			Currency:       "USD",
			CreatedAt:      h.now().Unix(),
		},
	})
	if errors.Is(err, ErrIdempotencyConflict) {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "card store unavailable")
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]any{"card_id": card.ID, "status": card.Status})
}

func (h *Handler) list(response http.ResponseWriter, request *http.Request) {
	organizationID := strings.TrimSpace(request.URL.Query().Get("organization_id"))
	if organizationID == "" {
		writeError(response, http.StatusBadRequest, "organization_id is required")
		return
	}
	cards, err := h.store.ListByOrganization(request.Context(), organizationID)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "card store unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"cards": cards})
}

func writeJSON(response http.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}

func writeError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]string{"error": message})
}
