package riskwarehouse

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresFeatureReconciliationStoreIsIdempotentAndPreservesChangedEvidence(t *testing.T) {
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
	schemaName := fmt.Sprintf("feature_reconciliation_test_%d", time.Now().UnixNano())
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
	if _, err := pool.Exec(ctx, featureReconciliationTableDDL); err != nil {
		t.Fatal(err)
	}

	stream := reconciliationFixture("flink-shadow-v1")
	batch := reconciliationFixture("airflow-run-001")
	thresholds := ReconciliationThresholds{AmountMinor: 0, Ratio: 0.000001}
	result, err := NewFeatureReconciler(thresholds).Compare(stream, batch)
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewFeatureReconciliationRecord(stream, batch, thresholds, result)
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresFeatureReconciliationStore(pool)

	replay, err := store.Save(ctx, record)
	if err != nil || replay {
		t.Fatalf("first save replay=%v err=%v", replay, err)
	}
	replay, err = store.Save(ctx, record)
	if err != nil || !replay {
		t.Fatalf("second save replay=%v err=%v", replay, err)
	}

	stream.OutflowMinor++
	stream.OutflowRatio = float64(stream.OutflowMinor) / float64(stream.InflowMinor)
	changedResult, err := NewFeatureReconciler(thresholds).Compare(stream, batch)
	if err != nil {
		t.Fatal(err)
	}
	changedRecord, err := NewFeatureReconciliationRecord(stream, batch, thresholds, changedResult)
	if err != nil {
		t.Fatal(err)
	}
	if changedRecord.ComparisonID == record.ComparisonID {
		t.Fatal("changed stream evidence must create a new comparison id")
	}
	if replay, err = store.Save(ctx, changedRecord); err != nil || replay {
		t.Fatalf("changed evidence replay=%v err=%v", replay, err)
	}

	var rows int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM risk_feature_reconciliations").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("expected two immutable comparisons, got %d", rows)
	}
}
