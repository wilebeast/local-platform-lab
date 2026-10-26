package acquiring

import (
	"context"
	"testing"
)

func TestRefundedPaymentCanBeNetSettledExactlyOnce(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	created := captureRefundTestPayment(t, store, "net-settlement-payment")
	refund, err := store.RefundPayment(context.Background(), RefundPaymentCommand{
		IdempotencyKey: "net-settlement-refund", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := SettleChannelNetCommand{
		IdempotencyKey: "net-settlement-key", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID,
	}
	first, err := store.SettleChannelNet(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SettleChannelNet(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if first.SettlementID != second.SettlementID || !second.Replayed {
		t.Fatalf("settlement replay first=%#v second=%#v", first, second)
	}
	var settlements, journals, postings int
	var debits, credits, receivableBalance, refundPayableBalance int64
	err = pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM acquiring_channel_settlements WHERE payment_id=$1),
		(SELECT count(*) FROM ledger_journals WHERE id=$2 AND business_type='channel_net_settlement' AND event_type='channel_settlement'),
		count(p.id),
		COALESCE(sum(p.amount_minor) FILTER(WHERE p.side='debit'),0),
		COALESCE(sum(p.amount_minor) FILTER(WHERE p.side='credit'),0)
	FROM ledger_postings p WHERE p.journal_id=$2`, created.Order.ID, first.SettlementJournalID).
		Scan(&settlements, &journals, &postings, &debits, &credits)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(context.Background(), `SELECT
		COALESCE(max(CASE WHEN a.account_key LIKE 'provider.%.receivable' THEN b.posted_debits_minor-b.posted_credits_minor END),-1),
		COALESCE(max(CASE WHEN a.account_key LIKE 'provider.%.refund-payable' THEN b.posted_credits_minor-b.posted_debits_minor END),-1)
	FROM ledger_accounts a JOIN ledger_balances b ON b.account_id=a.id
	WHERE a.book_id='book-acquiring-local' AND a.owner_id='demo-card'`).Scan(&receivableBalance, &refundPayableBalance)
	if err != nil {
		t.Fatal(err)
	}
	if settlements != 1 || journals != 1 || postings != 2 || debits != 10000 || credits != 10000 || receivableBalance != 0 || refundPayableBalance != 0 {
		t.Fatalf("settlement evidence rows=%d/%d/%d totals=%d/%d balances=%d/%d refund=%s", settlements, journals, postings, debits, credits, receivableBalance, refundPayableBalance, refund.RefundID)
	}
}

func TestChannelNetSettlementRejectsChangedReplayAndUnrefundedPayment(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	refunded := captureRefundTestPayment(t, store, "settlement-conflict-refunded")
	if _, err := store.RefundPayment(context.Background(), RefundPaymentCommand{IdempotencyKey: "refund-first", MerchantID: refunded.Order.MerchantID, PaymentID: refunded.Order.ID}); err != nil {
		t.Fatal(err)
	}
	unrefunded := captureRefundTestPayment(t, store, "settlement-conflict-unrefunded")
	command := SettleChannelNetCommand{IdempotencyKey: "same-settlement-key", MerchantID: refunded.Order.MerchantID, PaymentID: refunded.Order.ID}
	if _, err := store.SettleChannelNet(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	command.PaymentID = unrefunded.Order.ID
	if _, err := store.SettleChannelNet(context.Background(), command); err != ErrPaymentIdempotencyConflict {
		t.Fatalf("changed settlement replay error=%v", err)
	}
	if _, err := store.SettleChannelNet(context.Background(), SettleChannelNetCommand{IdempotencyKey: "unrefunded-key", MerchantID: unrefunded.Order.MerchantID, PaymentID: unrefunded.Order.ID}); err != ErrPaymentNotNetSettleable {
		t.Fatalf("unrefunded payment error=%v", err)
	}
	var rows int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM acquiring_channel_settlements`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("settlement rows=%d", rows)
	}
}
