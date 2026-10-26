package acquiring

import (
	"context"
	"testing"
)

func TestResolveReconciliationIssuePersistsEvidenceWithoutMutatingMoney(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	created := captureRefundTestPayment(t, store, "resolution-payment")
	if _, err := store.SettleChannelCash(context.Background(), SettleChannelCashCommand{
		IdempotencyKey: "resolution-cash", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID,
	}); err != nil {
		t.Fatal(err)
	}
	payout, err := store.PayMerchant(context.Background(), PayMerchantCommand{
		IdempotencyKey: "resolution-payout", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.ReconcilePayouts(context.Background(), ReconcilePayoutsCommand{
		IdempotencyKey: "resolution-run", Provider: "demo-card", StatementID: "statement-before-late-evidence",
		PeriodStart: 1, PeriodEnd: 4102444800,
	})
	if err != nil {
		t.Fatal(err)
	}
	var issueID string
	if err := pool.QueryRow(context.Background(), `SELECT issue_id FROM acquiring_reconciliation_issues WHERE run_id=$1`, run.RunID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	var journalsBefore int
	var payoutStatusBefore string
	if err := pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM ledger_journals),
		(SELECT status FROM acquiring_merchant_payouts WHERE payout_id=$1)`, payout.PayoutID).
		Scan(&journalsBefore, &payoutStatusBefore); err != nil {
		t.Fatal(err)
	}

	command := ResolveReconciliationIssueCommand{
		IdempotencyKey: "resolution-key", IssueID: issueID,
		ResolutionType: "provider_confirmed_late", EvidenceReference: "late-statement-demo-card-20261027",
	}
	first, err := store.ResolveReconciliationIssue(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ResolveReconciliationIssue(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if first.ResolutionID != second.ResolutionID || first.Status != "resolved" || !second.Replayed {
		t.Fatalf("resolution first=%#v second=%#v", first, second)
	}

	var issueStatus, resolutionType, evidenceReference, payoutStatusAfter string
	var resolvedAtPresent bool
	var resolutions, journalsAfter int
	err = pool.QueryRow(context.Background(), `SELECT
		i.status, i.resolved_at IS NOT NULL,
		r.resolution_type, r.evidence_reference,
		(SELECT count(*) FROM acquiring_reconciliation_resolutions WHERE issue_id=i.issue_id),
		(SELECT count(*) FROM ledger_journals),
		(SELECT status FROM acquiring_merchant_payouts WHERE payout_id=$2)
	FROM acquiring_reconciliation_issues i
	JOIN acquiring_reconciliation_resolutions r ON r.issue_id=i.issue_id
	WHERE i.issue_id=$1`, issueID, payout.PayoutID).
		Scan(&issueStatus, &resolvedAtPresent, &resolutionType, &evidenceReference, &resolutions, &journalsAfter, &payoutStatusAfter)
	if err != nil {
		t.Fatal(err)
	}
	if issueStatus != "resolved" || !resolvedAtPresent || resolutionType != command.ResolutionType || evidenceReference != command.EvidenceReference || resolutions != 1 {
		t.Fatalf("resolution evidence status=%s resolvedAt=%v type=%s evidence=%s rows=%d", issueStatus, resolvedAtPresent, resolutionType, evidenceReference, resolutions)
	}
	if journalsAfter != journalsBefore || payoutStatusAfter != payoutStatusBefore {
		t.Fatalf("money mutated journals=%d/%d payout=%s/%s", journalsBefore, journalsAfter, payoutStatusBefore, payoutStatusAfter)
	}

	command.EvidenceReference = "different-statement"
	if _, err := store.ResolveReconciliationIssue(context.Background(), command); err != ErrPaymentIdempotencyConflict {
		t.Fatalf("changed replay error=%v", err)
	}
}
