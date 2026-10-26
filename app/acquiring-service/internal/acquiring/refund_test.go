package acquiring

import (
	"context"
	"testing"
)

func TestFullRefundPostsBalancedJournalExactlyOnce(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	created, err := store.CreatePayment(context.Background(), postgresPaymentCommand("refund-payment", 10000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Receive(context.Background(), ProviderEvent{Provider: "demo-card", ID: "evt-refund-payment", Type: "payment.succeeded", ProviderRequestID: created.Operation.ProviderReqID, OccurredAt: 1800000000, RawBody: []byte(`{"id":"evt-refund-payment"}`)}); err != nil {
		t.Fatal(err)
	}
	if processed, err := store.ProcessNextProviderEvent(context.Background()); err != nil || !processed {
		t.Fatalf("capture payment: %v %v", processed, err)
	}
	command := RefundPaymentCommand{IdempotencyKey: "refund-key", MerchantID: created.Order.MerchantID, PaymentID: created.Order.ID}
	first, err := store.RefundPayment(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.RefundPayment(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if first.RefundID != second.RefundID || !second.Replayed {
		t.Fatalf("refund replay first=%#v second=%#v", first, second)
	}
	var orderStatus, journalStatus string
	var refunds, journals, postings int
	var debits, credits int64
	if err := pool.QueryRow(context.Background(), `SELECT o.status,j.status,
		count(DISTINCT r.refund_id),
		count(DISTINCT j.id) FILTER(WHERE j.business_type='merchant_refund' AND j.business_id=r.refund_id AND j.event_type='refund'),
		count(p.id),COALESCE(SUM(p.amount_minor) FILTER(WHERE p.side='debit'),0),COALESCE(SUM(p.amount_minor) FILTER(WHERE p.side='credit'),0)
	FROM acquiring_payment_orders o
	JOIN acquiring_refunds r ON r.payment_id=o.payment_id
	JOIN ledger_journals j ON j.id=r.refund_journal_id
	JOIN ledger_postings p ON p.journal_id=j.id
	WHERE o.payment_id=$1 GROUP BY o.status,j.status,o.payment_id,r.refund_id`, created.Order.ID).Scan(&orderStatus, &journalStatus, &refunds, &journals, &postings, &debits, &credits); err != nil {
		t.Fatal(err)
	}
	if orderStatus != "refunded" || journalStatus != "posted" || refunds != 1 || journals != 1 || postings != 3 || debits != 10000 || credits != 10000 {
		t.Fatalf("refund evidence status=%s/%s rows=%d/%d/%d totals=%d/%d", orderStatus, journalStatus, refunds, journals, postings, debits, credits)
	}
}

func TestChangedRefundReplayConflictsWithoutSecondJournal(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	firstPayment := captureRefundTestPayment(t, store, "refund-conflict-a")
	secondPayment := captureRefundTestPayment(t, store, "refund-conflict-b")
	command := RefundPaymentCommand{IdempotencyKey: "same-refund-key", MerchantID: firstPayment.Order.MerchantID, PaymentID: firstPayment.Order.ID}
	if _, err := store.RefundPayment(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	command.PaymentID = secondPayment.Order.ID
	if _, err := store.RefundPayment(context.Background(), command); err != ErrPaymentIdempotencyConflict {
		t.Fatalf("changed refund replay error=%v", err)
	}
	var refunds, journals int
	if err := pool.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM acquiring_refunds WHERE merchant_id=$1 AND idempotency_key=$2),
		(SELECT count(*) FROM ledger_journals WHERE business_type='merchant_refund')`, command.MerchantID, command.IdempotencyKey).Scan(&refunds, &journals); err != nil {
		t.Fatal(err)
	}
	if refunds != 1 || journals != 1 {
		t.Fatalf("conflicting replay created effects refunds=%d journals=%d", refunds, journals)
	}
}

func captureRefundTestPayment(t *testing.T, store *PostgresPaymentStore, key string) CreatePaymentResult {
	t.Helper()
	created, err := store.CreatePayment(context.Background(), postgresPaymentCommand(key, 10000))
	if err != nil {
		t.Fatal(err)
	}
	event := ProviderEvent{Provider: "demo-card", ID: "evt-" + key, Type: "payment.succeeded", ProviderRequestID: created.Operation.ProviderReqID, OccurredAt: 1800000000, RawBody: []byte(`{"id":"evt"}`)}
	if _, err := store.Receive(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if processed, err := store.ProcessNextProviderEvent(context.Background()); err != nil || !processed {
		t.Fatalf("capture payment: %v %v", processed, err)
	}
	return created
}
