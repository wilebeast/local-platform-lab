package acquiring

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type ProviderEvent struct {
	Provider          string `json:"-"`
	ID                string `json:"id"`
	Type              string `json:"type"`
	ProviderRequestID string `json:"provider_request_id"`
	OccurredAt        int64  `json:"occurred_at"`
	RawBody           []byte `json:"-"`
}

type ProviderInbox interface {
	Receive(context.Context, ProviderEvent) (duplicate bool, err error)
}

type MemoryProviderInbox struct {
	mu     sync.Mutex
	events map[string]ProviderEvent
}

func NewMemoryProviderInbox() *MemoryProviderInbox {
	return &MemoryProviderInbox{events: make(map[string]ProviderEvent)}
}

func (s *MemoryProviderInbox) Receive(_ context.Context, event ProviderEvent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := event.Provider + ":" + event.ID
	if _, ok := s.events[key]; ok {
		return true, nil
	}
	s.events[key] = event
	return false, nil
}

func (s *MemoryProviderInbox) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

func SignProviderEvent(secret, timestamp, eventID string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%s.%s.", timestamp, eventID)
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func NewProviderEventHandler(store ProviderInbox, secrets map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider := r.PathValue("provider")
		secret, ok := secrets[provider]
		if !ok {
			writePaymentJSON(w, http.StatusNotFound, map[string]string{"error": "unknown provider"})
			return
		}
		timestamp := r.Header.Get("X-Provider-Timestamp")
		eventID := r.Header.Get("X-Provider-Event-Id")
		signature := r.Header.Get("X-Provider-Signature")
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil || !validProviderTimestamp(timestamp, time.Now()) || !verifyProviderEvent(secret, timestamp, eventID, body, signature) {
			writePaymentJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid signature"})
			return
		}
		var event ProviderEvent
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&event); err != nil || event.ID != eventID || event.ProviderRequestID == "" || event.OccurredAt <= 0 ||
			(event.Type != "payment.succeeded" && event.Type != "payment.failed") {
			writePaymentJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid event"})
			return
		}
		event.Provider, event.RawBody = provider, body
		duplicate, err := store.Receive(r.Context(), event)
		if err != nil {
			writePaymentJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "receive provider event"})
			return
		}
		writePaymentJSON(w, http.StatusOK, map[string]any{"received": true, "duplicate": duplicate})
	})
}

func verifyProviderEvent(secret, timestamp, eventID string, body []byte, signature string) bool {
	want, err := hex.DecodeString(SignProviderEvent(secret, timestamp, eventID, body))
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(signature)
	return err == nil && hmac.Equal(want, got)
}

func validProviderTimestamp(raw string, now time.Time) bool {
	timestamp, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return false
	}
	delta := now.Unix() - timestamp
	return delta >= -60 && delta <= 600
}
