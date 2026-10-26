package acquiring

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresCreatePaymentExactReplayPersistsOneResourceSet(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	command := postgresPaymentCommand("postgres-exact", 10000)

	first, err := store.CreatePayment(context.Background(), command)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, err := store.CreatePayment(context.Background(), command)
	if err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	if first.Order.ID != second.Order.ID || !second.Replayed {
		t.Fatalf("replay result: first=%#v second=%#v", first, second)
	}
	assertPaymentRowCounts(t, pool, 1, 1, 1)
}

func TestPostgresCreatePaymentChangedReplayConflicts(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	if _, err := store.CreatePayment(context.Background(), postgresPaymentCommand("postgres-conflict", 10000)); err != nil {
		t.Fatal(err)
	}
	_, err := store.CreatePayment(context.Background(), postgresPaymentCommand("postgres-conflict", 20000))
	if err != ErrPaymentIdempotencyConflict {
		t.Fatalf("changed replay error = %v, want conflict", err)
	}
	assertPaymentRowCounts(t, pool, 1, 1, 1)
}

func TestPostgresCreatePaymentConcurrentExactReplayPersistsOneResourceSet(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	command := postgresPaymentCommand("postgres-concurrent", 10000)
	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			_, err := store.CreatePayment(context.Background(), command)
			errorsCh <- err
		}()
	}
	ready.Wait()
	close(start)
	for range 2 {
		if err := <-errorsCh; err != nil {
			t.Fatalf("concurrent create: %v", err)
		}
	}
	assertPaymentRowCounts(t, pool, 1, 1, 1)
}

func TestPostgresProviderInboxDeduplicatesWithinProvider(t *testing.T) {
	store, pool := newAcquiringPostgresStore(t)
	event := ProviderEvent{Provider: "demo-card", ID: "evt-postgres-card", Type: "payment.succeeded",
		ProviderRequestID: "provider-request-postgres", OccurredAt: 1800000000,
		RawBody: []byte(`{"id":"evt-postgres-card"}`)}
	firstDuplicate, err := store.Receive(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	secondDuplicate, err := store.Receive(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM acquiring_provider_inbox WHERE provider=$1 AND event_id=$2`, event.Provider, event.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if firstDuplicate || !secondDuplicate || rows != 1 {
		t.Fatalf("duplicate flags=%v/%v rows=%d, want false/true/1", firstDuplicate, secondDuplicate, rows)
	}
}

func postgresPaymentCommand(key string, amount int64) CreatePaymentCommand {
	return CreatePaymentCommand{IdempotencyKey: key, MerchantID: "merchant-postgres", MerchantOrder: "order-" + key,
		Currency: "USD", AmountMinor: amount, PaymentMethod: "card"}
}

func assertPaymentRowCounts(t *testing.T, pool *pgxpool.Pool, orders, attempts, operations int) {
	t.Helper()
	var gotOrders, gotAttempts, gotOperations int
	if err := pool.QueryRow(context.Background(), `SELECT
		(SELECT COUNT(*) FROM acquiring_payment_orders),
		(SELECT COUNT(*) FROM acquiring_payment_attempts),
		(SELECT COUNT(*) FROM acquiring_channel_operations)`).Scan(&gotOrders, &gotAttempts, &gotOperations); err != nil {
		t.Fatal(err)
	}
	if gotOrders != orders || gotAttempts != attempts || gotOperations != operations {
		t.Fatalf("row counts=%d/%d/%d, want %d/%d/%d", gotOrders, gotAttempts, gotOperations, orders, attempts, operations)
	}
}

func newAcquiringPostgresStore(t *testing.T) (*PostgresPaymentStore, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schemaName := fmt.Sprintf("acquiring_contract_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schemaName}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	ledgerSchemaPath := filepath.Join("..", "..", "..", "enterprise-card-service", "cmd", "enterprise-card", "schema.sql")
	ledgerSchema, err := os.ReadFile(ledgerSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(ledgerSchema)); err != nil {
		t.Fatal(err)
	}
	if err := EnsureAcquiringSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	})
	return NewPostgresPaymentStore(pool), pool
}
