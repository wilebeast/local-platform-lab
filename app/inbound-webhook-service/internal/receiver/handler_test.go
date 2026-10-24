package receiver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wilebeast/local-platform-lab/app/inbound-webhook-service/internal/provider"
)

type memoryInbox struct{ events map[string]InboxEvent }

func newMemoryInbox() *memoryInbox { return &memoryInbox{events: make(map[string]InboxEvent)} }

func (s *memoryInbox) Accept(_ context.Context, event InboxEvent) (bool, error) {
	key := event.IntegrationCode + ":" + event.ExternalEventID
	if _, exists := s.events[key]; exists {
		return false, nil
	}
	s.events[key] = event
	return true, nil
}

func TestHandlerAcceptsValidEventAndAcknowledgesDuplicate(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := newMemoryInbox()
	handler := NewHandler(testIntegrations(), store, func() time.Time { return now })
	body := []byte(`{"id":"evt-pay-1","type":"payment.settled","version":1,"occurred_at":1800000000,"data":{"resource_id":"payment-1","status":"settled"}}`)

	response := serveSigned(t, handler, "/api/v3/provider-webhooks/demo-payments", body, "evt-pay-1", "payments-v1", now.Unix(), "demo-payments-secret")
	if response.Code != http.StatusOK || response.Body.String() != `{"received":true,"duplicate":false}` {
		t.Fatalf("first response = %d %s", response.Code, response.Body.String())
	}
	response = serveSigned(t, handler, "/api/v3/provider-webhooks/demo-payments", body, "evt-pay-1", "payments-v1", now.Unix(), "demo-payments-secret")
	if response.Code != http.StatusOK || response.Body.String() != `{"received":true,"duplicate":true}` {
		t.Fatalf("duplicate response = %d %s", response.Code, response.Body.String())
	}
	if len(store.events) != 1 {
		t.Fatalf("stored events = %d, want 1", len(store.events))
	}
	stored := store.events["demo-payments:evt-pay-1"]
	if stored.Domain != "payment" || stored.EventType != "payment.settled" || string(stored.RawBody) != string(body) {
		t.Fatalf("stored event = %#v", stored)
	}
}

func TestHandlerRejectsTamperedAndExpiredEvents(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	handler := NewHandler(testIntegrations(), newMemoryInbox(), func() time.Time { return now })
	original := []byte(`{"id":"evt-pay-1","type":"payment.settled","version":1,"occurred_at":1800000000,"data":{}}`)

	request := signedRequest(t, "/api/v3/provider-webhooks/demo-payments", original, "evt-pay-1", "payments-v1", now.Unix(), "demo-payments-secret")
	request.Body = http.NoBody
	request.Body = ioNopCloser{bytes.NewReader([]byte(`{"id":"evt-pay-1","type":"payment.failed","version":1,"occurred_at":1800000000,"data":{}}`))}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("tampered status = %d, want 401", response.Code)
	}

	response = serveSigned(t, handler, "/api/v3/provider-webhooks/demo-payments", original, "evt-pay-1", "payments-v1", now.Add(-6*time.Minute).Unix(), "demo-payments-secret")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expired status = %d, want 401", response.Code)
	}
}

func TestHandlerAcceptsBothCredentialsDuringKeyRotation(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	handler := NewHandler(testIntegrations(), newMemoryInbox(), func() time.Time { return now })
	body := []byte(`{"id":"evt-pay-rotation","type":"payment.settled","version":1,"occurred_at":1800000000,"data":{}}`)

	response := serveSigned(t, handler, "/api/v3/provider-webhooks/demo-payments", body, "evt-pay-rotation", "payments-v2", now.Unix(), "demo-payments-next-secret")
	if response.Code != http.StatusOK {
		t.Fatalf("rotated credential status = %d, want 200", response.Code)
	}
}

func testIntegrations() map[string]Integration {
	return map[string]Integration{
		"demo-payments": {
			Code: "demo-payments", Domain: "payment", EventPrefix: "payment.", Adapter: provider.HMACAdapter{},
			Credentials: map[string]provider.Credential{
				"payments-v1": {KeyID: "payments-v1", HMACSecret: []byte("demo-payments-secret")},
				"payments-v2": {KeyID: "payments-v2", HMACSecret: []byte("demo-payments-next-secret")},
			},
		},
	}
}

func serveSigned(t *testing.T, handler http.Handler, path string, body []byte, eventID, keyID string, timestamp int64, secret string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, signedRequest(t, path, body, eventID, keyID, timestamp, secret))
	return response
}

func signedRequest(t *testing.T, path string, body []byte, eventID, keyID string, timestamp int64, secret string) *http.Request {
	t.Helper()
	signed := provider.SignedRequest{Timestamp: timestamp, EventID: eventID, Body: body}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(provider.CanonicalPayload(signed))
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set(HeaderKeyID, keyID)
	request.Header.Set(HeaderEventID, eventID)
	request.Header.Set(HeaderTimestamp, formatTimestamp(timestamp))
	request.Header.Set(HeaderSignatureVersion, "v1")
	request.Header.Set(HeaderSignature, hex.EncodeToString(mac.Sum(nil)))
	return request
}

type ioNopCloser struct{ *bytes.Reader }

func (ioNopCloser) Close() error { return nil }
