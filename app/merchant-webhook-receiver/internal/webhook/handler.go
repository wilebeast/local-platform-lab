package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	HeaderEventID          = "X-Webhook-Event-Id"
	HeaderTimestamp        = "X-Webhook-Timestamp"
	HeaderSignatureVersion = "X-Webhook-Signature-Version"
	HeaderSignature        = "X-Webhook-Signature"
	SignatureVersion       = "v1"
	maximumClockSkew       = 5 * time.Minute
)

type Event struct {
	ID         string          `json:"id"`
	Name       string          `json:"event"`
	Version    int             `json:"version"`
	OccurredAt int64           `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

type MemoryStore struct {
	mu     sync.RWMutex
	events map[string]Event
	order  []string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{events: make(map[string]Event)}
}

func (s *MemoryStore) Put(event Event) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.events[event.ID]; exists {
		return false
	}
	s.events[event.ID] = event
	s.order = append(s.order, event.ID)
	return true
}

func (s *MemoryStore) Events() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	events := make([]Event, 0, len(s.order))
	for _, id := range s.order {
		events = append(events, s.events[id])
	}
	return events
}

type Handler struct {
	secret string
	store  *MemoryStore
	now    func() time.Time
}

func NewHandler(secret string, store *MemoryStore, now func() time.Time) http.Handler {
	return &Handler{secret: secret, store: store, now: now}
}

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(response, request.Body, 1<<20))
	if err != nil {
		http.Error(response, "invalid body", http.StatusBadRequest)
		return
	}
	eventID := request.Header.Get(HeaderEventID)
	timestamp, err := strconv.ParseInt(request.Header.Get(HeaderTimestamp), 10, 64)
	if err != nil || eventID == "" || request.Header.Get(HeaderSignatureVersion) != SignatureVersion {
		http.Error(response, "invalid signature metadata", http.StatusUnauthorized)
		return
	}
	requestTime := time.Unix(timestamp, 0)
	if delta := h.now().Sub(requestTime); delta > maximumClockSkew || delta < -maximumClockSkew {
		http.Error(response, "expired signature", http.StatusUnauthorized)
		return
	}
	expected := Sign(h.secret, timestamp, eventID, body)
	provided, err := hex.DecodeString(strings.TrimSpace(request.Header.Get(HeaderSignature)))
	if err != nil || !hmac.Equal([]byte(expected), []byte(hex.EncodeToString(provided))) {
		http.Error(response, "invalid signature", http.StatusUnauthorized)
		return
	}
	var event Event
	if err := json.Unmarshal(body, &event); err != nil || event.ID == "" || event.ID != eventID || event.Name == "" {
		http.Error(response, "invalid event", http.StatusBadRequest)
		return
	}
	h.store.Put(event)
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte(`{"received":true}`))
}

func Sign(secret string, timestamp int64, eventID string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s.", timestamp, eventID)
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func formatTimestamp(timestamp int64) string {
	return strconv.FormatInt(timestamp, 10)
}
