package acquiring

import (
	"context"
	"testing"
)

func TestWorkerCompletesPaymentAndCaptureJournalAtomically(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	created, err := store.CreatePayment(context.Background(), postgresPaymentCommand("worker-success", 10000))
	if err != nil {
		t.Fatal(err)
	}
	event := ProviderEvent{Provider: "demo-card", ID: "evt-worker-success", Type: "payment.succeeded",
		ProviderRequestID: created.Operation.ProviderReqID, OccurredAt: 1800000000,
		RawBody: []byte(`{"id":"evt-worker-success"}`)}
	if _, err := store.Receive(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	processed, err := store.ProcessNextProviderEvent(context.Background())
	if err != nil || !processed {
		t.Fatalf("process event: processed=%v err=%v", processed, err)
	}

	var orderStatus, attemptStatus, operationStatus, inboxStatus, journalStatus string
	var postings, debitTotal, creditTotal int64
	err = pool.QueryRow(context.Background(), `SELECT o.status,a.status,c.status,i.processing_status,j.status,
		COUNT(p.id),COALESCE(SUM(p.amount_minor) FILTER(WHERE p.side='debit'),0),COALESCE(SUM(p.amount_minor) FILTER(WHERE p.side='credit'),0)
	FROM acquiring_payment_orders o
	JOIN acquiring_payment_attempts a ON a.payment_id=o.payment_id
	JOIN acquiring_channel_operations c ON c.attempt_id=a.attempt_id
	JOIN acquiring_provider_inbox i ON i.provider_request_id=c.provider_request_id
	JOIN ledger_journals j ON j.business_type='merchant_payment' AND j.business_id=o.payment_id AND j.event_type='capture'
	JOIN ledger_postings p ON p.journal_id=j.id
	WHERE o.payment_id=$1
	GROUP BY o.status,a.status,c.status,i.processing_status,j.status`, created.Order.ID).Scan(
		&orderStatus, &attemptStatus, &operationStatus, &inboxStatus, &journalStatus, &postings, &debitTotal, &creditTotal)
	if err != nil {
		t.Fatal(err)
	}
	if orderStatus != "succeeded" || attemptStatus != "succeeded" || operationStatus != "succeeded" || inboxStatus != "processed" || journalStatus != "posted" {
		t.Fatalf("states=%s/%s/%s/%s/%s", orderStatus, attemptStatus, operationStatus, inboxStatus, journalStatus)
	}
	if postings != 3 || debitTotal != 10000 || creditTotal != 10000 {
		t.Fatalf("journal postings=%d debit=%d credit=%d", postings, debitTotal, creditTotal)
	}
	processed, err = store.ProcessNextProviderEvent(context.Background())
	if err != nil || processed {
		t.Fatalf("second process: processed=%v err=%v", processed, err)
	}
}

func TestWorkerDeadLettersUnknownProviderRequestWithoutJournal(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	event := ProviderEvent{Provider: "demo-card", ID: "evt-worker-unknown", Type: "payment.succeeded",
		ProviderRequestID: "provider-request-missing", OccurredAt: 1800000000, RawBody: []byte(`{"id":"evt-worker-unknown"}`)}
	if _, err := store.Receive(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	processed, err := store.ProcessNextProviderEvent(context.Background())
	if err != nil || !processed {
		t.Fatalf("process unknown: processed=%v err=%v", processed, err)
	}
	var status, lastError string
	var journals int
	if err := pool.QueryRow(context.Background(), `SELECT processing_status,last_error FROM acquiring_provider_inbox WHERE event_id=$1`, event.ID).Scan(&status, &lastError); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM ledger_journals WHERE business_id=$1`, event.ProviderRequestID).Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if status != "dead_letter" || lastError != "channel operation not found" || journals != 0 {
		t.Fatalf("unknown result status=%s error=%s journals=%d", status, lastError, journals)
	}
}
