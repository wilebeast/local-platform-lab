package acquiring

import (
	"context"
	"testing"
)

func TestReconciliationDetectsMissingProviderPayoutWithoutMutatingLedger(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	created := captureRefundTestPayment(t, store, "reconciliation-missing-payout")
	if _, err := store.SettleChannelCash(context.Background(), SettleChannelCashCommand{IdempotencyKey: "reconciliation-cash", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID}); err != nil {
		t.Fatal(err)
	}
	payout, err := store.PayMerchant(context.Background(), PayMerchantCommand{IdempotencyKey: "reconciliation-payout", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID})
	if err != nil {
		t.Fatal(err)
	}
	var journalsBefore int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM ledger_journals`).Scan(&journalsBefore); err != nil {
		t.Fatal(err)
	}
	command := ReconcilePayoutsCommand{
		IdempotencyKey: "reconciliation-run-key", Provider: "demo-card", StatementID: "statement-missing-payout",
		PeriodStart: 1, PeriodEnd: 4102444800, ProviderPayoutIDs: []string{},
	}
	first, err := store.ReconcilePayouts(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ReconcilePayouts(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if first.RunID != second.RunID || !second.Replayed || first.IssueCount != 1 || first.MatchedCount != 0 {
		t.Fatalf("reconciliation first=%#v second=%#v", first, second)
	}
	var runs, issues, journalsAfter int
	var issueType, businessID, status string
	var expectedAmount int64
	err = pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM acquiring_reconciliation_runs),
		(SELECT count(*) FROM acquiring_reconciliation_issues),
		i.issue_type,i.business_id,i.expected_amount_minor,i.status,
		(SELECT count(*) FROM ledger_journals)
	FROM acquiring_reconciliation_issues i WHERE i.run_id=$1`, first.RunID).
		Scan(&runs, &issues, &issueType, &businessID, &expectedAmount, &status, &journalsAfter)
	if err != nil {
		t.Fatal(err)
	}
	if runs != 1 || issues != 1 || issueType != "missing_provider_payout" || businessID != payout.PayoutID || expectedAmount != 9800 || status != "open" || journalsAfter != journalsBefore {
		t.Fatalf("evidence runs=%d issues=%d type=%s business=%s amount=%d status=%s journals=%d/%d", runs, issues, issueType, businessID, expectedAmount, status, journalsBefore, journalsAfter)
	}
}

func TestReconciliationMatchesProviderPayoutAndRejectsChangedReplay(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	created := captureRefundTestPayment(t, store, "reconciliation-matched-payout")
	if _, err := store.SettleChannelCash(context.Background(), SettleChannelCashCommand{IdempotencyKey: "matched-cash", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID}); err != nil {
		t.Fatal(err)
	}
	payout, err := store.PayMerchant(context.Background(), PayMerchantCommand{IdempotencyKey: "matched-payout", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID})
	if err != nil {
		t.Fatal(err)
	}
	command := ReconcilePayoutsCommand{IdempotencyKey: "matched-run", Provider: "demo-card", StatementID: "statement-matched", PeriodStart: 1, PeriodEnd: 4102444800, ProviderPayoutIDs: []string{payout.PayoutID}}
	result, err := store.ReconcilePayouts(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.MatchedCount != 1 || result.IssueCount != 0 {
		t.Fatalf("matched result=%#v", result)
	}
	command.ProviderPayoutIDs = []string{}
	if _, err := store.ReconcilePayouts(context.Background(), command); err != ErrPaymentIdempotencyConflict {
		t.Fatalf("changed reconciliation replay error=%v", err)
	}
	var runs, issues int
	if err := pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM acquiring_reconciliation_runs),(SELECT count(*) FROM acquiring_reconciliation_issues)`).Scan(&runs, &issues); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || issues != 0 {
		t.Fatalf("reconciliation rows runs=%d issues=%d", runs, issues)
	}
}
