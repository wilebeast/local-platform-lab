package acquiring

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCorrectMissingProviderPayoutReversesJournalWithoutEditingHistory(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	created := captureRefundTestPayment(t, store, "payout-correction-payment")
	if _, err := store.SettleChannelCash(context.Background(), SettleChannelCashCommand{
		IdempotencyKey: "payout-correction-cash", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID,
	}); err != nil {
		t.Fatal(err)
	}
	payout, err := store.PayMerchant(context.Background(), PayMerchantCommand{
		IdempotencyKey: "payout-correction-paid", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.ReconcilePayouts(context.Background(), ReconcilePayoutsCommand{
		IdempotencyKey: "payout-correction-run", Provider: "demo-card", StatementID: "statement-proves-payout-missing",
		PeriodStart: 1, PeriodEnd: 4102444800,
	})
	if err != nil {
		t.Fatal(err)
	}
	var issueID string
	if err := pool.QueryRow(context.Background(), `SELECT issue_id FROM acquiring_reconciliation_issues WHERE run_id=$1`, run.RunID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	var originalJournalCreatedAt string
	if err := pool.QueryRow(context.Background(), `SELECT created_at::text FROM ledger_journals WHERE id=$1`, payout.PayoutJournalID).Scan(&originalJournalCreatedAt); err != nil {
		t.Fatal(err)
	}

	command := CorrectMissingProviderPayoutCommand{
		IdempotencyKey: "payout-correction-key", IssueID: issueID,
		EvidenceReference: "provider-statement-confirmed-not-sent-20261027",
	}
	first, err := store.CorrectMissingProviderPayout(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CorrectMissingProviderPayout(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if first.ResolutionID != second.ResolutionID || first.ReversalJournalID != second.ReversalJournalID || !second.Replayed {
		t.Fatalf("correction first=%#v second=%#v", first, second)
	}

	var issueStatus, payoutStatus, paymentStatus, reversalOf, originalCreatedAtAfter string
	var reversalPostings, totalJournals int
	err = pool.QueryRow(context.Background(), `SELECT
		i.status,p.status,o.status,j.reversal_of,
		(SELECT count(*) FROM ledger_postings WHERE journal_id=j.id),
		(SELECT count(*) FROM ledger_journals),
		(SELECT created_at::text FROM ledger_journals WHERE id=$2)
	FROM acquiring_reconciliation_issues i
	JOIN acquiring_reconciliation_resolutions r ON r.issue_id=i.issue_id
	JOIN acquiring_merchant_payouts p ON p.payout_id=i.business_id
	JOIN acquiring_payment_orders o ON o.payment_id=p.payment_id
	JOIN ledger_journals j ON j.id=r.reversal_journal_id
	WHERE i.issue_id=$1`, issueID, payout.PayoutJournalID).
		Scan(&issueStatus, &payoutStatus, &paymentStatus, &reversalOf, &reversalPostings, &totalJournals, &originalCreatedAtAfter)
	if err != nil {
		t.Fatal(err)
	}
	if issueStatus != "resolved" || payoutStatus != "reversed" || paymentStatus != "succeeded" || reversalOf != payout.PayoutJournalID || reversalPostings != 2 {
		t.Fatalf("correction state issue=%s payout=%s payment=%s reversalOf=%s postings=%d", issueStatus, payoutStatus, paymentStatus, reversalOf, reversalPostings)
	}
	if originalCreatedAtAfter != originalJournalCreatedAt || totalJournals != 4 {
		t.Fatalf("journal history changed original=%s/%s count=%d", originalJournalCreatedAt, originalCreatedAtAfter, totalJournals)
	}

	balances := acquiringBalanceSnapshot(t, pool, created.Order.MerchantID)
	if balances["platform_cash"] != 10000 || balances["merchant_payable"] != 9800 || balances["fee_revenue"] != 200 || balances["channel_receivable"] != 0 {
		t.Fatalf("balances after correction=%#v", balances)
	}

	command.EvidenceReference = "changed-evidence"
	if _, err := store.CorrectMissingProviderPayout(context.Background(), command); err != ErrPaymentIdempotencyConflict {
		t.Fatalf("changed replay error=%v", err)
	}

	retry, err := store.PayMerchant(context.Background(), PayMerchantCommand{
		IdempotencyKey: "payout-correction-retry", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if retry.PayoutID == payout.PayoutID || retry.Status != "paid" {
		t.Fatalf("retry payout=%#v original=%#v", retry, payout)
	}
}

func acquiringBalanceSnapshot(t *testing.T, pool *pgxpool.Pool, merchantID string) map[string]int64 {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT a.purpose,
		CASE WHEN a.normal_side='debit' THEN b.posted_debits_minor-b.posted_credits_minor
		ELSE b.posted_credits_minor-b.posted_debits_minor END
	FROM ledger_accounts a JOIN ledger_balances b ON b.account_id=a.id
	WHERE a.book_id='book-acquiring-local' AND
		((a.owner_type='organization' AND a.owner_id=$1) OR a.owner_type='platform' OR (a.owner_type='provider' AND a.purpose='receivable'))`, merchantID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]int64{}
	for rows.Next() {
		var purpose string
		var balance int64
		if err := rows.Scan(&purpose, &balance); err != nil {
			t.Fatal(err)
		}
		result[purposeName(purpose)] = balance
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func purposeName(purpose string) string {
	switch purpose {
	case "cash":
		return "platform_cash"
	case "payable":
		return "merchant_payable"
	case "fee":
		return "fee_revenue"
	case "receivable":
		return "channel_receivable"
	default:
		return purpose
	}
}
