package card

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestFundingEntryIsIdempotentThroughHTTP(t *testing.T) {
	store := NewMemoryFinanceStore()
	handler := NewFinanceHandler(store)
	body := []byte(`{"entry_id":"fund-demo-001","card_id":"card-demo","currency":"USD","amount_minor":100000}`)

	first := postFinance(t, handler, "/api/v3/card-lab/accounts/fund", body)
	second := postFinance(t, handler, "/api/v3/card-lab/accounts/fund", body)
	if first.PostedBalanceMinor != 100000 || first.HeldAmountMinor != 0 || first.AvailableBalanceMinor != 100000 {
		t.Fatalf("first balance = %#v", first)
	}
	if second != first {
		t.Fatalf("duplicate funding changed balance: first=%#v second=%#v", first, second)
	}
}

func TestProviderAuthorizationHoldsFundsOnceAndDeclinesInsufficientBalance(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := NewMemoryFinanceStore()
	store.SetProviderCard("provider-card-demo", "card-demo", "active")
	_, err := store.Fund(context.Background(), FundParams{"fund-auth-001", "card-demo", "USD", 100000})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAuthorizationHandler(store, []byte("authorization-secret"), func() time.Time { return now })

	body := []byte(`{"authorization_id":"auth-001","provider_card_id":"provider-card-demo","currency":"USD","amount_minor":2500}`)
	first := postSignedAuthorization(t, handler, now.Unix(), "auth-001", body, "authorization-secret")
	second := postSignedAuthorization(t, handler, now.Unix(), "auth-001", body, "authorization-secret")
	if first.Decision != "approved" || first.HeldAmountMinor != 2500 || first.AvailableBalanceMinor != 97500 {
		t.Fatalf("first=%#v", first)
	}
	if second != first {
		t.Fatalf("duplicate changed result: first=%#v second=%#v", first, second)
	}

	insufficientBody := []byte(`{"authorization_id":"auth-002","provider_card_id":"provider-card-demo","currency":"USD","amount_minor":200000}`)
	declined := postSignedAuthorization(t, handler, now.Unix(), "auth-002", insufficientBody, "authorization-secret")
	if declined.Decision != "declined" || declined.Reason != "insufficient_funds" || declined.HeldAmountMinor != 2500 {
		t.Fatalf("declined=%#v", declined)
	}
}

func TestProviderAuthorizationReversalReleasesFundsOnce(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := NewMemoryFinanceStore()
	store.SetProviderCard("provider-card-demo", "card-demo", "active")
	if _, err := store.Fund(context.Background(), FundParams{"fund-reversal-001", "card-demo", "USD", 100000}); err != nil {
		t.Fatal(err)
	}
	authorizationHandler := NewAuthorizationHandler(store, []byte("authorization-secret"), func() time.Time { return now })
	authorizationBody := []byte(`{"authorization_id":"auth-reversal-001","provider_card_id":"provider-card-demo","currency":"USD","amount_minor":2500}`)
	approved := postSignedAuthorization(t, authorizationHandler, now.Unix(), "auth-reversal-001", authorizationBody, "authorization-secret")
	if approved.Decision != "approved" || approved.HeldAmountMinor != 2500 {
		t.Fatalf("approved=%#v", approved)
	}

	reversalHandler := NewAuthorizationReversalHandler(store, []byte("authorization-secret"), func() time.Time { return now })
	reversalBody := []byte(`{"reversal_id":"reversal-001","authorization_id":"auth-reversal-001"}`)
	first := postSignedReversal(t, reversalHandler, now.Unix(), "reversal-001", reversalBody, "authorization-secret")
	second := postSignedReversal(t, reversalHandler, now.Unix(), "reversal-001", reversalBody, "authorization-secret")
	if first.Status != "reversed" || first.ReleasedAmountMinor != 2500 || first.PostedBalanceMinor != 100000 || first.HeldAmountMinor != 0 || first.AvailableBalanceMinor != 100000 {
		t.Fatalf("first=%#v", first)
	}
	if second != first {
		t.Fatalf("duplicate reversal changed result: first=%#v second=%#v", first, second)
	}
}

func TestProviderAuthorizationReversalRejectsConflictsAndDeclinedAuthorizations(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := NewMemoryFinanceStore()
	store.SetProviderCard("provider-card-demo", "card-demo", "active")
	if _, err := store.Fund(context.Background(), FundParams{"fund-reversal-conflict", "card-demo", "USD", 100000}); err != nil {
		t.Fatal(err)
	}
	authorizationHandler := NewAuthorizationHandler(store, []byte("authorization-secret"), func() time.Time { return now })
	approvedBody := []byte(`{"authorization_id":"auth-approved","provider_card_id":"provider-card-demo","currency":"USD","amount_minor":2500}`)
	postSignedAuthorization(t, authorizationHandler, now.Unix(), "auth-approved", approvedBody, "authorization-secret")
	declinedBody := []byte(`{"authorization_id":"auth-declined","provider_card_id":"provider-card-demo","currency":"USD","amount_minor":200000}`)
	postSignedAuthorization(t, authorizationHandler, now.Unix(), "auth-declined", declinedBody, "authorization-secret")

	handler := NewAuthorizationReversalHandler(store, []byte("authorization-secret"), func() time.Time { return now })
	firstBody := []byte(`{"reversal_id":"reversal-first","authorization_id":"auth-approved"}`)
	postSignedReversal(t, handler, now.Unix(), "reversal-first", firstBody, "authorization-secret")

	secondBody := []byte(`{"reversal_id":"reversal-second","authorization_id":"auth-approved"}`)
	if status := postSignedReversalStatus(t, handler, now.Unix(), "reversal-second", secondBody, "authorization-secret"); status != http.StatusConflict {
		t.Fatalf("second reversal status=%d", status)
	}
	declinedReversalBody := []byte(`{"reversal_id":"reversal-declined","authorization_id":"auth-declined"}`)
	if status := postSignedReversalStatus(t, handler, now.Unix(), "reversal-declined", declinedReversalBody, "authorization-secret"); status != http.StatusConflict {
		t.Fatalf("declined reversal status=%d", status)
	}
}

func TestConcurrentAuthorizationReversalsReleaseFundsOnlyOnce(t *testing.T) {
	store := NewMemoryFinanceStore()
	store.SetProviderCard("provider-card-demo", "card-demo", "active")
	if _, err := store.Fund(context.Background(), FundParams{"fund-concurrent-reversal", "card-demo", "USD", 100000}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(context.Background(), AuthorizationParams{"auth-concurrent", "provider-card-demo", "USD", 2500}); err != nil {
		t.Fatal(err)
	}

	results := make(chan AuthorizationReversalResult, 2)
	errors := make(chan error, 2)
	var wait sync.WaitGroup
	for _, reversalID := range []string{"reversal-concurrent-a", "reversal-concurrent-b"} {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			result, err := store.ReverseAuthorization(context.Background(), AuthorizationReversalParams{id, "auth-concurrent"})
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}(reversalID)
	}
	wait.Wait()
	close(results)
	close(errors)
	if len(results) != 1 || len(errors) != 1 {
		t.Fatalf("successes=%d errors=%d", len(results), len(errors))
	}
	result := <-results
	if result.ReleasedAmountMinor != 2500 || result.HeldAmountMinor != 0 || result.AvailableBalanceMinor != 100000 {
		t.Fatalf("result=%#v", result)
	}
	if err := <-errors; err != ErrAuthorizationNotReversible {
		t.Fatalf("error=%v", err)
	}
}

func TestProviderAuthorizationSettlementConvertsHoldIntoPostedDebitOnce(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := NewMemoryFinanceStore()
	store.SetProviderCard("provider-card-demo", "card-demo", "active")
	if _, err := store.Fund(context.Background(), FundParams{"fund-settlement-001", "card-demo", "USD", 100000}); err != nil {
		t.Fatal(err)
	}
	authorizationHandler := NewAuthorizationHandler(store, []byte("authorization-secret"), func() time.Time { return now })
	authorizationBody := []byte(`{"authorization_id":"auth-settlement-001","provider_card_id":"provider-card-demo","currency":"USD","amount_minor":2500}`)
	postSignedAuthorization(t, authorizationHandler, now.Unix(), "auth-settlement-001", authorizationBody, "authorization-secret")

	settlementHandler := NewAuthorizationSettlementHandler(store, []byte("authorization-secret"), func() time.Time { return now })
	settlementBody := []byte(`{"settlement_id":"settlement-001","authorization_id":"auth-settlement-001"}`)
	first := postSignedSettlement(t, settlementHandler, now.Unix(), "settlement-001", settlementBody, "authorization-secret")
	second := postSignedSettlement(t, settlementHandler, now.Unix(), "settlement-001", settlementBody, "authorization-secret")
	if first.Status != "settled" || first.SettledAmountMinor != 2500 || first.PostedBalanceMinor != 97500 || first.HeldAmountMinor != 0 || first.AvailableBalanceMinor != 97500 {
		t.Fatalf("first=%#v", first)
	}
	if second != first {
		t.Fatalf("duplicate settlement changed result: first=%#v second=%#v", first, second)
	}
}

func TestAuthorizationCannotBeSettledTwiceOrAfterReversal(t *testing.T) {
	store := NewMemoryFinanceStore()
	store.SetProviderCard("provider-card-demo", "card-demo", "active")
	if _, err := store.Fund(context.Background(), FundParams{"fund-settlement-conflict", "card-demo", "USD", 100000}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(context.Background(), AuthorizationParams{"auth-settle-once", "provider-card-demo", "USD", 2500}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SettleAuthorization(context.Background(), AuthorizationSettlementParams{"settlement-first", "auth-settle-once"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SettleAuthorization(context.Background(), AuthorizationSettlementParams{"settlement-second", "auth-settle-once"}); err != ErrAuthorizationNotSettleable {
		t.Fatalf("second settlement error=%v", err)
	}

	if _, err := store.Authorize(context.Background(), AuthorizationParams{"auth-reversed-before-settlement", "provider-card-demo", "USD", 2500}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReverseAuthorization(context.Background(), AuthorizationReversalParams{"reversal-before-settlement", "auth-reversed-before-settlement"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SettleAuthorization(context.Background(), AuthorizationSettlementParams{"settlement-after-reversal", "auth-reversed-before-settlement"}); err != ErrAuthorizationNotSettleable {
		t.Fatalf("settlement after reversal error=%v", err)
	}
}

func TestConcurrentAuthorizationSettlementsDebitFundsOnlyOnce(t *testing.T) {
	store := NewMemoryFinanceStore()
	store.SetProviderCard("provider-card-demo", "card-demo", "active")
	if _, err := store.Fund(context.Background(), FundParams{"fund-concurrent-settlement", "card-demo", "USD", 100000}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(context.Background(), AuthorizationParams{"auth-concurrent-settlement", "provider-card-demo", "USD", 2500}); err != nil {
		t.Fatal(err)
	}
	results := make(chan AuthorizationSettlementResult, 2)
	errors := make(chan error, 2)
	var wait sync.WaitGroup
	for _, settlementID := range []string{"settlement-concurrent-a", "settlement-concurrent-b"} {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			result, err := store.SettleAuthorization(context.Background(), AuthorizationSettlementParams{id, "auth-concurrent-settlement"})
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}(settlementID)
	}
	wait.Wait()
	close(results)
	close(errors)
	if len(results) != 1 || len(errors) != 1 {
		t.Fatalf("successes=%d errors=%d", len(results), len(errors))
	}
	result := <-results
	if result.PostedBalanceMinor != 97500 || result.HeldAmountMinor != 0 || result.AvailableBalanceMinor != 97500 {
		t.Fatalf("result=%#v", result)
	}
	if err := <-errors; err != ErrAuthorizationNotSettleable {
		t.Fatalf("error=%v", err)
	}
}

func TestSettlementRefundRestoresPostedBalanceOnce(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := NewMemoryFinanceStore()
	store.SetProviderCard("provider-card-demo", "card-demo", "active")
	if _, err := store.Fund(context.Background(), FundParams{"fund-refund-001", "card-demo", "USD", 100000}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(context.Background(), AuthorizationParams{"auth-refund-001", "provider-card-demo", "USD", 3000}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SettleAuthorization(context.Background(), AuthorizationSettlementParams{"settlement-refund-001", "auth-refund-001"}); err != nil {
		t.Fatal(err)
	}

	handler := NewSettlementRefundHandler(store, []byte("authorization-secret"), func() time.Time { return now })
	body := []byte(`{"refund_id":"refund-001","settlement_id":"settlement-refund-001"}`)
	first := postSignedRefund(t, handler, now.Unix(), "refund-001", body, "authorization-secret")
	second := postSignedRefund(t, handler, now.Unix(), "refund-001", body, "authorization-secret")
	if first.Status != "refunded" || first.RefundedAmountMinor != 3000 || first.PostedBalanceMinor != 100000 || first.HeldAmountMinor != 0 || first.AvailableBalanceMinor != 100000 {
		t.Fatalf("first=%#v", first)
	}
	if second != first {
		t.Fatalf("duplicate refund changed result: first=%#v second=%#v", first, second)
	}
}

func TestSettlementCannotBeRefundedByAnotherRefund(t *testing.T) {
	store := NewMemoryFinanceStore()
	store.SetProviderCard("provider-card-demo", "card-demo", "active")
	if _, err := store.Fund(context.Background(), FundParams{"fund-refund-conflict", "card-demo", "USD", 100000}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(context.Background(), AuthorizationParams{"auth-refund-conflict", "provider-card-demo", "USD", 3000}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SettleAuthorization(context.Background(), AuthorizationSettlementParams{"settlement-refund-conflict", "auth-refund-conflict"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefundSettlement(context.Background(), SettlementRefundParams{"refund-first", "settlement-refund-conflict"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefundSettlement(context.Background(), SettlementRefundParams{"refund-second", "settlement-refund-conflict"}); err != ErrSettlementNotRefundable {
		t.Fatalf("second refund error=%v", err)
	}
	if _, err := store.RefundSettlement(context.Background(), SettlementRefundParams{"refund-missing", "missing-settlement"}); err != ErrSettlementNotRefundable {
		t.Fatalf("missing settlement refund error=%v", err)
	}
}

func TestConcurrentSettlementRefundsCreditFundsOnlyOnce(t *testing.T) {
	store := NewMemoryFinanceStore()
	store.SetProviderCard("provider-card-demo", "card-demo", "active")
	if _, err := store.Fund(context.Background(), FundParams{"fund-concurrent-refund", "card-demo", "USD", 100000}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(context.Background(), AuthorizationParams{"auth-concurrent-refund", "provider-card-demo", "USD", 3000}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SettleAuthorization(context.Background(), AuthorizationSettlementParams{"settlement-concurrent-refund", "auth-concurrent-refund"}); err != nil {
		t.Fatal(err)
	}
	results := make(chan SettlementRefundResult, 2)
	errors := make(chan error, 2)
	var wait sync.WaitGroup
	for _, refundID := range []string{"refund-concurrent-a", "refund-concurrent-b"} {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			result, err := store.RefundSettlement(context.Background(), SettlementRefundParams{id, "settlement-concurrent-refund"})
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}(refundID)
	}
	wait.Wait()
	close(results)
	close(errors)
	if len(results) != 1 || len(errors) != 1 {
		t.Fatalf("successes=%d errors=%d", len(results), len(errors))
	}
	result := <-results
	if result.PostedBalanceMinor != 100000 || result.HeldAmountMinor != 0 || result.AvailableBalanceMinor != 100000 {
		t.Fatalf("result=%#v", result)
	}
	if err := <-errors; err != ErrSettlementNotRefundable {
		t.Fatalf("error=%v", err)
	}
}

func postSignedRefund(t *testing.T, handler http.Handler, timestamp int64, refundID string, body []byte, secret string) SettlementRefundResult {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s.%s", timestamp, refundID, body)
	request := httptest.NewRequest(http.MethodPost, "/api/v3/card-lab/provider-settlement-refunds", bytes.NewReader(body))
	request.Header.Set("X-Provider-Event-Id", refundID)
	request.Header.Set("X-Provider-Timestamp", fmt.Sprintf("%d", timestamp))
	request.Header.Set("X-Provider-Signature", hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result SettlementRefundResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func postSignedSettlement(t *testing.T, handler http.Handler, timestamp int64, settlementID string, body []byte, secret string) AuthorizationSettlementResult {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s.%s", timestamp, settlementID, body)
	request := httptest.NewRequest(http.MethodPost, "/api/v3/card-lab/provider-authorization-settlements", bytes.NewReader(body))
	request.Header.Set("X-Provider-Event-Id", settlementID)
	request.Header.Set("X-Provider-Timestamp", fmt.Sprintf("%d", timestamp))
	request.Header.Set("X-Provider-Signature", hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result AuthorizationSettlementResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func postSignedReversal(t *testing.T, handler http.Handler, timestamp int64, reversalID string, body []byte, secret string) AuthorizationReversalResult {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s.%s", timestamp, reversalID, body)
	request := httptest.NewRequest(http.MethodPost, "/api/v3/card-lab/provider-authorization-reversals", bytes.NewReader(body))
	request.Header.Set("X-Provider-Event-Id", reversalID)
	request.Header.Set("X-Provider-Timestamp", fmt.Sprintf("%d", timestamp))
	request.Header.Set("X-Provider-Signature", hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result AuthorizationReversalResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func postSignedReversalStatus(t *testing.T, handler http.Handler, timestamp int64, reversalID string, body []byte, secret string) int {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s.%s", timestamp, reversalID, body)
	request := httptest.NewRequest(http.MethodPost, "/api/v3/card-lab/provider-authorization-reversals", bytes.NewReader(body))
	request.Header.Set("X-Provider-Event-Id", reversalID)
	request.Header.Set("X-Provider-Timestamp", fmt.Sprintf("%d", timestamp))
	request.Header.Set("X-Provider-Signature", hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Code
}

func postSignedAuthorization(t *testing.T, handler http.Handler, timestamp int64, authorizationID string, body []byte, secret string) AuthorizationResult {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s.%s", timestamp, authorizationID, body)
	request := httptest.NewRequest(http.MethodPost, "/api/v3/card-lab/provider-authorizations", bytes.NewReader(body))
	request.Header.Set("X-Provider-Event-Id", authorizationID)
	request.Header.Set("X-Provider-Timestamp", fmt.Sprintf("%d", timestamp))
	request.Header.Set("X-Provider-Signature", hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result AuthorizationResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func postFinance(t *testing.T, handler http.Handler, path string, body []byte) AccountBalance {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result AccountBalance
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
