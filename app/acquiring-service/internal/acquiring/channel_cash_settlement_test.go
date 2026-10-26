package acquiring

import (
	"context"
	"testing"
)

func TestSucceededPaymentCanBeCashSettledExactlyOnce(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	created := captureRefundTestPayment(t, store, "cash-settlement-payment")
	command := SettleChannelCashCommand{IdempotencyKey: "cash-settlement-key", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID}
	first, err := store.SettleChannelCash(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SettleChannelCash(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if first.SettlementID != second.SettlementID || !second.Replayed {
		t.Fatalf("cash settlement replay first=%#v second=%#v", first, second)
	}
	var settlements, journals, postings int
	var debits, credits, cashBalance, receivableBalance, merchantPayable, feeRevenue int64
	err = pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM acquiring_channel_cash_settlements WHERE payment_id=$1),
		(SELECT count(*) FROM ledger_journals WHERE id=$2 AND business_type='channel_cash_settlement' AND event_type='channel_settlement'),
		count(p.id),COALESCE(sum(p.amount_minor) FILTER(WHERE p.side='debit'),0),COALESCE(sum(p.amount_minor) FILTER(WHERE p.side='credit'),0)
	FROM ledger_postings p WHERE p.journal_id=$2`, created.Order.ID, first.SettlementJournalID).
		Scan(&settlements, &journals, &postings, &debits, &credits)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(context.Background(), `SELECT
		COALESCE(max(CASE WHEN a.account_key='platform.USD.cash' THEN b.posted_debits_minor-b.posted_credits_minor END),-1),
		COALESCE(max(CASE WHEN a.account_key LIKE 'provider.%.receivable' THEN b.posted_debits_minor-b.posted_credits_minor END),-1),
		COALESCE(max(CASE WHEN a.account_key LIKE 'merchant.%.payable' THEN b.posted_credits_minor-b.posted_debits_minor END),-1),
		COALESCE(max(CASE WHEN a.account_key='platform.USD.fee-revenue' THEN b.posted_credits_minor-b.posted_debits_minor END),-1)
	FROM ledger_accounts a JOIN ledger_balances b ON b.account_id=a.id WHERE a.book_id='book-acquiring-local'`).
		Scan(&cashBalance, &receivableBalance, &merchantPayable, &feeRevenue)
	if err != nil {
		t.Fatal(err)
	}
	if settlements != 1 || journals != 1 || postings != 2 || debits != 10000 || credits != 10000 || cashBalance != 10000 || receivableBalance != 0 || merchantPayable != 9800 || feeRevenue != 200 {
		t.Fatalf("cash settlement evidence rows=%d/%d/%d totals=%d/%d balances cash=%d receivable=%d merchant=%d fee=%d", settlements, journals, postings, debits, credits, cashBalance, receivableBalance, merchantPayable, feeRevenue)
	}
}

func TestChannelCashSettlementRejectsChangedReplayAndRefundedPayment(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	first := captureRefundTestPayment(t, store, "cash-conflict-first")
	second := captureRefundTestPayment(t, store, "cash-conflict-second")
	command := SettleChannelCashCommand{IdempotencyKey: "cash-same-key", MerchantID: first.Order.MerchantID, PaymentID: first.Order.ID}
	if _, err := store.SettleChannelCash(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	command.PaymentID = second.Order.ID
	if _, err := store.SettleChannelCash(context.Background(), command); err != ErrPaymentIdempotencyConflict {
		t.Fatalf("changed cash settlement replay error=%v", err)
	}
	if _, err := store.RefundPayment(context.Background(), RefundPaymentCommand{IdempotencyKey: "refund-after-cash", MerchantID: first.Order.MerchantID, PaymentID: first.Order.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SettleChannelNet(context.Background(), SettleChannelNetCommand{IdempotencyKey: "net-after-cash", MerchantID: first.Order.MerchantID, PaymentID: first.Order.ID}); err != ErrPaymentNotNetSettleable {
		t.Fatalf("cash-settled payment entered net path: %v", err)
	}
	if _, err := store.RefundPayment(context.Background(), RefundPaymentCommand{IdempotencyKey: "cash-refunded", MerchantID: second.Order.MerchantID, PaymentID: second.Order.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SettleChannelCash(context.Background(), SettleChannelCashCommand{IdempotencyKey: "cash-after-refund", MerchantID: second.Order.MerchantID, PaymentID: second.Order.ID}); err != ErrPaymentNotCashSettleable {
		t.Fatalf("refunded payment error=%v", err)
	}
	var rows int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM acquiring_channel_cash_settlements`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("cash settlement rows=%d", rows)
	}
}
