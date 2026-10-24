package card

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestApplyCardIsIdempotentThroughHTTP(t *testing.T) {
	store := NewMemoryStore()
	handler := NewHandler(store, func() time.Time {
		return time.Unix(1_800_000_000, 0)
	}, func() string {
		return "card-local-001"
	})

	first := applyCard(t, handler, "apply-org-demo-001")
	second := applyCard(t, handler, "apply-org-demo-001")

	if first.StatusCode != http.StatusAccepted || second.StatusCode != http.StatusAccepted {
		t.Fatalf("apply statuses = %d, %d; want 202, 202", first.StatusCode, second.StatusCode)
	}
	if first.CardID != "card-local-001" || second.CardID != first.CardID {
		t.Fatalf("card ids = %q, %q; want the same deterministic id", first.CardID, second.CardID)
	}
	if first.Status != "pending" || second.Status != "pending" {
		t.Fatalf("statuses = %q, %q; want pending", first.Status, second.Status)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/enterprise-cards?organization_id=org-demo", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", response.Code, response.Body.String())
	}
	var list struct {
		Cards []Card `json:"cards"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Cards) != 1 {
		t.Fatalf("card count = %d; want 1", len(list.Cards))
	}
}

type applyResponse struct {
	StatusCode int
	CardID     string
	Status     string
}

func applyCard(t *testing.T, handler http.Handler, idempotencyKey string) applyResponse {
	t.Helper()
	body := []byte(`{"organization_id":"org-demo","holder_id":"user-demo","alias":"Engineering"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/enterprise-cards", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var payload struct {
		CardID string `json:"card_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v body=%s", err, response.Body.String())
	}
	return applyResponse{StatusCode: response.Code, CardID: payload.CardID, Status: payload.Status}
}
