package card

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProviderWebhookVerifiesSignatureAndIsIdempotent(t *testing.T) {
	store := NewMemoryStore()
	now := time.Unix(1_800_000_000, 0)
	handler := NewWebhookHandler(store, []byte("test-provider-secret"), func() time.Time { return now })
	body := []byte(`{"id":"evt-1","type":"card.status.changed","version":1,"occurred_at":1800000000,"data":{"provider_card_id":"provider-1","status":"frozen"}}`)

	first := sendProviderEvent(t, handler, now.Unix(), "evt-1", body, "test-provider-secret")
	second := sendProviderEvent(t, handler, now.Unix(), "evt-1", body, "test-provider-secret")
	if first.Code != http.StatusOK || webhookDuplicate(t, first) {
		t.Fatalf("first response = %d %s", first.Code, first.Body.String())
	}
	if second.Code != http.StatusOK || !webhookDuplicate(t, second) {
		t.Fatalf("second response = %d %s", second.Code, second.Body.String())
	}

	tampered := sendProviderEvent(t, handler, now.Unix(), "evt-2", body, "wrong-secret")
	if tampered.Code != http.StatusUnauthorized {
		t.Fatalf("tampered response = %d %s", tampered.Code, tampered.Body.String())
	}
}

func webhookDuplicate(t *testing.T, response *httptest.ResponseRecorder) bool {
	t.Helper()
	var payload struct {
		Duplicate bool `json:"duplicate"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Duplicate
}

func sendProviderEvent(t *testing.T, handler http.Handler, timestamp int64, eventID string, body []byte, secret string) *httptest.ResponseRecorder {
	t.Helper()
	canonical := []byte(fmt.Sprintf("%d.%s.%s", timestamp, eventID, body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(canonical)
	req := httptest.NewRequest(http.MethodPost, "/api/v3/card-lab/provider-webhooks", bytes.NewReader(body))
	req.Header.Set("X-Provider-Event-Id", eventID)
	req.Header.Set("X-Provider-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-Provider-Signature", hex.EncodeToString(mac.Sum(nil)))
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}
