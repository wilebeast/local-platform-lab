package card

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type recordingProvider struct {
	calls int
}

func (p *recordingProvider) ApplyCard(_ context.Context, input ProviderApplyRequest) (ProviderApplyResult, error) {
	p.calls++
	return ProviderApplyResult{ProviderCardID: "provider-" + input.CardID}, nil
}

func TestWorkerActivatesPendingCardThroughProvider(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := NewMemoryStore()
	handler := NewHandler(store, func() time.Time { return now }, func() string { return "card-local-002" })
	applyCard(t, handler, "apply-org-demo-002")
	provider := &recordingProvider{}
	processor := NewProcessor(store, provider, func() time.Time { return now }, 3)

	processed, err := processor.ProcessNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessNext() processed=%t err=%v", processed, err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d; want 1", provider.calls)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/enterprise-cards?organization_id=org-demo", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", response.Code, response.Body.String())
	}
	if got := response.Body.String(); got == "" || !containsAll(got, `"status":"active"`, `"provider_card_id":"provider-card-local-002"`) {
		t.Fatalf("active card response = %s", got)
	}

	processed, err = processor.ProcessNext(context.Background())
	if err != nil || processed {
		t.Fatalf("second ProcessNext() processed=%t err=%v; want no work", processed, err)
	}
}

func TestWorkerAppliesProviderCardStatusEvent(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := NewMemoryStore()
	_, err := store.Apply(context.Background(), ApplyParams{
		IdempotencyKey: "apply-status-001",
		Card:           Card{ID: "card-status-001", OrganizationID: "org-demo", HolderID: "user-demo", Alias: "Ops", Status: "pending", Currency: "USD", CreatedAt: now.Unix()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteOperation(context.Background(), "operation-card-status-001", "card-status-001", "provider-status-001", now); err != nil {
		t.Fatal(err)
	}

	event := ProviderEvent{ID: "evt-freeze-001", Type: "card.status.changed", Version: 1, OccurredAt: now.Unix()}
	event.Data.ProviderCardID = "provider-status-001"
	event.Data.Status = "frozen"
	if _, err := store.RecordProviderEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}

	processor := NewProviderEventProcessor(store, func() time.Time { return now })
	processed, err := processor.ProcessNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessNext() processed=%t err=%v", processed, err)
	}
	cards, err := store.ListByOrganization(context.Background(), "org-demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].Status != "frozen" {
		t.Fatalf("cards = %#v; want one frozen card", cards)
	}
	processed, err = processor.ProcessNext(context.Background())
	if err != nil || processed {
		t.Fatalf("second ProcessNext() processed=%t err=%v; want no work", processed, err)
	}
}

func TestWorkerIgnoresProviderCardStatusEventOlderThanAppliedState(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := NewMemoryStore()
	_, err := store.Apply(context.Background(), ApplyParams{
		IdempotencyKey: "apply-order-001",
		Card:           Card{ID: "card-order-001", OrganizationID: "org-demo", HolderID: "user-demo", Alias: "Ops", Status: "pending", Currency: "USD", CreatedAt: now.Unix()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteOperation(context.Background(), "operation-card-order-001", "card-order-001", "provider-order-001", now); err != nil {
		t.Fatal(err)
	}
	processor := NewProviderEventProcessor(store, func() time.Time { return now })

	newer := ProviderEvent{ID: "evt-newer", Type: "card.status.changed", Version: 1, OccurredAt: now.Add(time.Minute).Unix()}
	newer.Data.ProviderCardID, newer.Data.Status = "provider-order-001", "frozen"
	_, _ = store.RecordProviderEvent(context.Background(), newer)
	if processed, err := processor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("newer event processed=%t err=%v", processed, err)
	}

	older := ProviderEvent{ID: "evt-older", Type: "card.status.changed", Version: 1, OccurredAt: now.Unix()}
	older.Data.ProviderCardID, older.Data.Status = "provider-order-001", "active"
	_, _ = store.RecordProviderEvent(context.Background(), older)
	if processed, err := processor.ProcessNext(context.Background()); err != nil || !processed {
		t.Fatalf("older event processed=%t err=%v", processed, err)
	}

	cards, err := store.ListByOrganization(context.Background(), "org-demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].Status != "frozen" {
		t.Fatalf("cards = %#v; want newer frozen state preserved", cards)
	}
}

func containsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}
