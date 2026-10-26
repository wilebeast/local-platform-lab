package acquiring

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestProviderEventValidSignatureCreatesOneInboxFact(t *testing.T) {
	store := NewMemoryProviderInbox()
	secrets := map[string]string{"demo-card": "card-secret", "demo-bank": "bank-secret"}
	handler := NewProviderEventHandler(store, secrets)
	body := []byte(`{"id":"evt-card-1","type":"payment.succeeded","provider_request_id":"provider-request-1","occurred_at":1800000000}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := SignProviderEvent("card-secret", timestamp, "evt-card-1", body)

	first := sendProviderEvent(handler, "demo-card", "evt-card-1", timestamp, signature, body)
	second := sendProviderEvent(handler, "demo-card", "evt-card-1", timestamp, signature, body)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("statuses=%d,%d bodies=%s %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	if store.Count() != 1 || !secondDuplicate(second.Body.Bytes()) {
		t.Fatalf("inbox count=%d second=%s, want one row and duplicate", store.Count(), second.Body.String())
	}
}

func TestProviderEventTamperedBodyIsRejectedBeforeInbox(t *testing.T) {
	store := NewMemoryProviderInbox()
	handler := NewProviderEventHandler(store, map[string]string{"demo-card": "card-secret"})
	original := []byte(`{"id":"evt-card-2","type":"payment.succeeded","provider_request_id":"provider-request-2","occurred_at":1800000000}`)
	tampered := []byte(`{"id":"evt-card-2","type":"payment.failed","provider_request_id":"provider-request-2","occurred_at":1800000000}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := SignProviderEvent("card-secret", timestamp, "evt-card-2", original)

	response := sendProviderEvent(handler, "demo-card", "evt-card-2", timestamp, signature, tampered)
	if response.Code != http.StatusUnauthorized || store.Count() != 0 {
		t.Fatalf("status=%d count=%d body=%s, want 401 and zero inbox rows", response.Code, store.Count(), response.Body.String())
	}
}

func TestProviderEventCannotUseAnotherProvidersSecret(t *testing.T) {
	store := NewMemoryProviderInbox()
	handler := NewProviderEventHandler(store, map[string]string{"demo-card": "card-secret", "demo-bank": "bank-secret"})
	body := []byte(`{"id":"evt-bank-1","type":"payment.succeeded","provider_request_id":"provider-request-bank","occurred_at":1800000000}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	wrongSignature := SignProviderEvent("card-secret", timestamp, "evt-bank-1", body)

	response := sendProviderEvent(handler, "demo-bank", "evt-bank-1", timestamp, wrongSignature, body)
	if response.Code != http.StatusUnauthorized || store.Count() != 0 {
		t.Fatalf("status=%d count=%d, want provider-isolated rejection", response.Code, store.Count())
	}
}

func secondDuplicate(body []byte) bool {
	var response struct {
		Duplicate bool `json:"duplicate"`
	}
	return json.Unmarshal(body, &response) == nil && response.Duplicate
}

func sendProviderEvent(handler http.Handler, provider, eventID, timestamp, signature string, body []byte) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/provider-events/"+provider, bytes.NewReader(body))
	request.SetPathValue("provider", provider)
	request.Header.Set("X-Provider-Event-Id", eventID)
	request.Header.Set("X-Provider-Timestamp", timestamp)
	request.Header.Set("X-Provider-Signature", signature)
	handler.ServeHTTP(recorder, request)
	return recorder
}
