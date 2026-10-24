package card

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

type ProviderEvent struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Version    int    `json:"version"`
	OccurredAt int64  `json:"occurred_at"`
	Data       struct {
		ProviderCardID string `json:"provider_card_id"`
		Status         string `json:"status"`
	} `json:"data"`
	RawBody          []byte `json:"-"`
	ProcessingStatus string `json:"-"`
	AttemptCount     int    `json:"-"`
	ProcessingResult string `json:"-"`
}

type ProviderEventStore interface {
	RecordProviderEvent(context.Context, ProviderEvent) (bool, error)
}

type ProviderEventProcessorStore interface {
	ClaimProviderEvent(context.Context, time.Time) (*ProviderEvent, error)
	CompleteProviderEvent(context.Context, ProviderEvent, time.Time) error
}

type ProviderEventProcessor struct {
	store ProviderEventProcessorStore
	now   func() time.Time
}

func NewProviderEventProcessor(store ProviderEventProcessorStore, now func() time.Time) *ProviderEventProcessor {
	return &ProviderEventProcessor{store: store, now: now}
}

func (p *ProviderEventProcessor) ProcessNext(ctx context.Context) (bool, error) {
	event, err := p.store.ClaimProviderEvent(ctx, p.now())
	if err != nil || event == nil {
		return false, err
	}
	return true, p.store.CompleteProviderEvent(ctx, *event, p.now())
}

type WebhookHandler struct {
	store  ProviderEventStore
	secret []byte
	now    func() time.Time
}

func NewWebhookHandler(store ProviderEventStore, secret []byte, now func() time.Time) http.Handler {
	return &WebhookHandler{store: store, secret: secret, now: now}
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	eventID := r.Header.Get("X-Provider-Event-Id")
	timestampText := r.Header.Get("X-Provider-Timestamp")
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil || eventID == "" || h.now().Sub(time.Unix(timestamp, 0)).Abs() > 5*time.Minute {
		writeError(w, http.StatusUnauthorized, "invalid signature metadata")
		return
	}
	provided, err := hex.DecodeString(r.Header.Get("X-Provider-Signature"))
	mac := hmac.New(sha256.New, h.secret)
	_, _ = fmt.Fprintf(mac, "%d.%s.%s", timestamp, eventID, body)
	if err != nil || !hmac.Equal(provided, mac.Sum(nil)) {
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}
	var event ProviderEvent
	if json.Unmarshal(body, &event) != nil || event.ID != eventID || event.Type != "card.status.changed" || event.Version != 1 || event.Data.ProviderCardID == "" || (event.Data.Status != "frozen" && event.Data.Status != "active") {
		writeError(w, http.StatusBadRequest, "invalid event")
		return
	}
	event.RawBody = body
	duplicate, err := h.store.RecordProviderEvent(r.Context(), event)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "event store unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"received": true, "duplicate": duplicate})
}
