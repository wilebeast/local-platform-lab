package acquiring

import (
	"context"
	"testing"
)

func TestCashSettledPaymentCanBePaidOutToMerchantExactlyOnce(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	created := captureRefundTestPayment(t, store, "merchant-payout-payment")
	if _, err := store.SettleChannelCash(context.Background(), SettleChannelCashCommand{IdempotencyKey: "payout-cash-settlement", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID}); err != nil {
		t.Fatal(err)
	}
	command := PayMerchantCommand{IdempotencyKey: "merchant-payout-key", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID}
	first, err := store.PayMerchant(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.PayMerchant(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if first.PayoutID != second.PayoutID || !second.Replayed {
		t.Fatalf("payout replay first=%#v second=%#v", first, second)
	}
	var payouts, journals, postings int
	var debits, credits, cashBalance, merchantPayable, feeRevenue int64
	err = pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM acquiring_merchant_payouts WHERE payment_id=$1),
		(SELECT count(*) FROM ledger_journals WHERE id=$2 AND business_type='merchant_payout' AND event_type='merchant_payout'),
		count(p.id),COALESCE(sum(p.amount_minor) FILTER(WHERE p.side='debit'),0),COALESCE(sum(p.amount_minor) FILTER(WHERE p.side='credit'),0)
	FROM ledger_postings p WHERE p.journal_id=$2`, created.Order.ID, first.PayoutJournalID).
		Scan(&payouts, &journals, &postings, &debits, &credits)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(context.Background(), `SELECT
		COALESCE(max(CASE WHEN a.account_key='platform.USD.cash' THEN b.posted_debits_minor-b.posted_credits_minor END),-1),
		COALESCE(max(CASE WHEN a.account_key LIKE 'merchant.%.payable' THEN b.posted_credits_minor-b.posted_debits_minor END),-1),
		COALESCE(max(CASE WHEN a.account_key='platform.USD.fee-revenue' THEN b.posted_credits_minor-b.posted_debits_minor END),-1)
	FROM ledger_accounts a JOIN ledger_balances b ON b.account_id=a.id WHERE a.book_id='book-acquiring-local'`).Scan(&cashBalance, &merchantPayable, &feeRevenue)
	if err != nil {
		t.Fatal(err)
	}
	if payouts != 1 || journals != 1 || postings != 2 || debits != 9800 || credits != 9800 || cashBalance != 200 || merchantPayable != 0 || feeRevenue != 200 {
		t.Fatalf("payout evidence rows=%d/%d/%d totals=%d/%d balances cash=%d merchant=%d fee=%d", payouts, journals, postings, debits, credits, cashBalance, merchantPayable, feeRevenue)
	}
}

func TestMerchantPayoutRejectsChangedReplayPrematurePayoutAndLaterRefund(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	paid := captureRefundTestPayment(t, store, "payout-conflict-paid")
	if _, err := store.SettleChannelCash(context.Background(), SettleChannelCashCommand{IdempotencyKey: "payout-conflict-cash", MerchantID: paid.Order.MerchantID, PaymentID: paid.Order.ID}); err != nil {
		t.Fatal(err)
	}
	unsettled := captureRefundTestPayment(t, store, "payout-conflict-unsettled")
	command := PayMerchantCommand{IdempotencyKey: "same-payout-key", MerchantID: paid.Order.MerchantID, PaymentID: paid.Order.ID}
	if _, err := store.PayMerchant(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	command.PaymentID = unsettled.Order.ID
	if _, err := store.PayMerchant(context.Background(), command); err != ErrPaymentIdempotencyConflict {
		t.Fatalf("changed payout replay error=%v", err)
	}
	if _, err := store.PayMerchant(context.Background(), PayMerchantCommand{IdempotencyKey: "premature-payout", MerchantID: unsettled.Order.MerchantID, PaymentID: unsettled.Order.ID}); err != ErrPaymentNotPayable {
		t.Fatalf("premature payout error=%v", err)
	}
	if _, err := store.RefundPayment(context.Background(), RefundPaymentCommand{IdempotencyKey: "refund-after-payout", MerchantID: paid.Order.MerchantID, PaymentID: paid.Order.ID}); err != ErrPaymentNotRefundable {
		t.Fatalf("refund after payout error=%v", err)
	}
	var rows int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM acquiring_merchant_payouts`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("payout rows=%d", rows)
	}
}
