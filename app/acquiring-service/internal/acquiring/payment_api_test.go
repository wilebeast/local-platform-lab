package acquiring

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestCreatePaymentPersistsOrderAttemptAndChannelOperation(t *testing.T) {
	store := NewMemoryPaymentStore()
	handler := NewCreatePaymentHandler(store)
	body := []byte(`{"merchant_id":"merchant-demo","merchant_order_id":"order-100","currency":"USD","amount_minor":10000,"payment_method":"card"}`)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/payments", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "payment-create-100")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s, want 201", recorder.Code, recorder.Body.String())
	}
	var response CreatePaymentResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Order.Status != OrderCreated || response.Attempt.Status != AttemptCreated || response.Operation.Status != OperationPending {
		t.Fatalf("unexpected states: %#v", response)
	}
	if response.Order.AmountMinor != 10000 || response.Order.FeeMinor != 200 || response.Order.MerchantNetMinor != 9800 {
		t.Fatalf("unexpected amounts: %#v", response.Order)
	}
	if response.CheckoutURL == "" {
		t.Fatal("checkout URL is empty")
	}
	if store.FinancialEffectCount() != 0 {
		t.Fatalf("financial effects = %d, want 0 before provider confirmation", store.FinancialEffectCount())
	}
}

func TestCreatePaymentExactReplayReturnsOriginalResources(t *testing.T) {
	store := NewMemoryPaymentStore()
	handler := NewCreatePaymentHandler(store)
	body := []byte(`{"merchant_id":"merchant-demo","merchant_order_id":"order-replay","currency":"USD","amount_minor":10000,"payment_method":"card"}`)

	first := createPaymentRequest(t, handler, "payment-replay-key", body)
	second := createPaymentRequest(t, handler, "payment-replay-key", body)

	if first.Code != http.StatusCreated || second.Code != http.StatusOK {
		t.Fatalf("statuses = %d,%d, want 201,200", first.Code, second.Code)
	}
	var firstResult, secondResult CreatePaymentResult
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResult); err != nil {
		t.Fatal(err)
	}
	if firstResult.Order.ID != secondResult.Order.ID || firstResult.Attempt.ID != secondResult.Attempt.ID || firstResult.Operation.ID != secondResult.Operation.ID {
		t.Fatalf("replay created different resources: first=%#v second=%#v", firstResult, secondResult)
	}
	if store.OrderCount() != 1 || store.AttemptCount() != 1 || store.OperationCount() != 1 {
		t.Fatalf("counts = order:%d attempt:%d operation:%d, want 1 each", store.OrderCount(), store.AttemptCount(), store.OperationCount())
	}
}

func TestCreatePaymentChangedReplayConflictsWithoutFinancialEffect(t *testing.T) {
	store := NewMemoryPaymentStore()
	handler := NewCreatePaymentHandler(store)
	firstBody := []byte(`{"merchant_id":"merchant-demo","merchant_order_id":"order-conflict","currency":"USD","amount_minor":10000,"payment_method":"card"}`)
	changedBody := []byte(`{"merchant_id":"merchant-demo","merchant_order_id":"order-conflict","currency":"USD","amount_minor":20000,"payment_method":"card"}`)

	first := createPaymentRequest(t, handler, "payment-conflict-key", firstBody)
	changed := createPaymentRequest(t, handler, "payment-conflict-key", changedBody)

	if first.Code != http.StatusCreated || changed.Code != http.StatusConflict {
		t.Fatalf("statuses = %d,%d, want 201,409; conflict body=%s", first.Code, changed.Code, changed.Body.String())
	}
	if store.OrderCount() != 1 || store.FinancialEffectCount() != 0 {
		t.Fatalf("order count=%d financial effects=%d, want 1 and 0", store.OrderCount(), store.FinancialEffectCount())
	}
}

func TestCreatePaymentConcurrentExactReplayCreatesOneResourceSet(t *testing.T) {
	store := NewMemoryPaymentStore()
	command := CreatePaymentCommand{
		IdempotencyKey: "payment-concurrent-key", MerchantID: "merchant-demo",
		MerchantOrder: "order-concurrent", Currency: "USD", AmountMinor: 10000, PaymentMethod: "card",
	}
	start := make(chan struct{})
	results := make(chan CreatePaymentResult, 2)
	errorsCh := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			result, err := store.CreatePayment(t.Context(), command)
			results <- result
			errorsCh <- err
		}()
	}
	ready.Wait()
	close(start)
	first, second := <-results, <-results
	if err := <-errorsCh; err != nil {
		t.Fatal(err)
	}
	if err := <-errorsCh; err != nil {
		t.Fatal(err)
	}
	if first.Order.ID != second.Order.ID || store.OrderCount() != 1 || store.OperationCount() != 1 {
		t.Fatalf("concurrent results=%#v %#v counts=%d/%d", first, second, store.OrderCount(), store.OperationCount())
	}
}

func createPaymentRequest(t *testing.T, handler http.Handler, key string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/payments", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	handler.ServeHTTP(recorder, request)
	return recorder
}
