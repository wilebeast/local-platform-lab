package webhook

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandlerAcceptsValidSignatureAndStoresEventOnce(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := NewMemoryStore()
	handler := NewHandler("local-webhook-secret", store, func() time.Time { return now })
	body := []byte(`{"id":"evt-1","event":"profile.updated","version":1,"occurred_at":1800000000,"data":{"user_id":"local-user","verification_status":"verified"}}`)

	request := signedRequest(t, body, "evt-1", now.Unix(), "local-webhook-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if got := len(store.Events()); got != 1 {
		t.Fatalf("stored events = %d, want 1", got)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, signedRequest(t, body, "evt-1", now.Unix(), "local-webhook-secret"))
	if response.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d, want 200", response.Code)
	}
	if got := len(store.Events()); got != 1 {
		t.Fatalf("stored duplicate events = %d, want 1", got)
	}
}

func TestHandlerRejectsTamperedBody(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	handler := NewHandler("local-webhook-secret", NewMemoryStore(), func() time.Time { return now })
	original := []byte(`{"id":"evt-1","event":"profile.updated"}`)
	request := signedRequest(t, original, "evt-1", now.Unix(), "local-webhook-secret")
	request.Body = http.NoBody
	request.Body = ioNopCloser(bytes.NewReader([]byte(`{"id":"evt-1","event":"profile.deleted"}`)))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestHandlerRejectsExpiredTimestamp(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	handler := NewHandler("local-webhook-secret", NewMemoryStore(), func() time.Time { return now })
	body := []byte(`{"id":"evt-1","event":"profile.updated"}`)
	request := signedRequest(t, body, "evt-1", now.Add(-6*time.Minute).Unix(), "local-webhook-secret")

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func signedRequest(t *testing.T, body []byte, eventID string, timestamp int64, secret string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/webhooks/platform", bytes.NewReader(body))
	request.Header.Set(HeaderEventID, eventID)
	request.Header.Set(HeaderTimestamp, formatTimestamp(timestamp))
	request.Header.Set(HeaderSignatureVersion, SignatureVersion)
	request.Header.Set(HeaderSignature, Sign(secret, timestamp, eventID, body))
	return request
}

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

func ioNopCloser(reader *bytes.Reader) nopCloser { return nopCloser{reader} }
