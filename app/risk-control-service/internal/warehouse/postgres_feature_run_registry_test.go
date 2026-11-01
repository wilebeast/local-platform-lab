package warehouse_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func TestPostgresFeatureRunRegistryAtomicallyRejectsConcurrentDifferentContent(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schemaName := fmt.Sprintf("feature_run_registry_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schemaName+" CASCADE")

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `CREATE TABLE risk_warehouse_feature_run_fingerprints (
		source_run_id TEXT PRIMARY KEY,
		content_fingerprint TEXT NOT NULL,
		registered_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		t.Fatal(err)
	}
	registry := warehouse.NewPostgresFeatureRunRegistry(pool)

	start := make(chan struct{})
	errorsByFingerprint := make(map[string]error)
	var mu sync.Mutex
	var wait sync.WaitGroup
	for _, fingerprint := range []string{"fingerprint-a", "fingerprint-b"} {
		fingerprint := fingerprint
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			err := registry.Register(ctx, "run-concurrent", fingerprint)
			mu.Lock()
			errorsByFingerprint[fingerprint] = err
			mu.Unlock()
		}()
	}
	close(start)
	wait.Wait()

	var successes, conflicts int
	var winningFingerprint string
	for fingerprint, err := range errorsByFingerprint {
		if err == nil {
			successes++
			winningFingerprint = fingerprint
			continue
		}
		var conflict warehouse.FeatureRunFingerprintConflictError
		if errors.As(err, &conflict) {
			conflicts++
			continue
		}
		t.Fatalf("unexpected register error: %v", err)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d errors=%v", successes, conflicts, errorsByFingerprint)
	}
	if err := registry.Register(ctx, "run-concurrent", winningFingerprint); err != nil {
		t.Fatalf("winning content must replay: %v", err)
	}
}
