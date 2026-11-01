package risk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOfflineScannerPersistsOneIdempotentRun(t *testing.T) {
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
	schemaName := fmt.Sprintf("offline_risk_test_%d", time.Now().UnixNano())
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
	schemaSQL, err := os.ReadFile(filepath.Join("..", "..", "cmd", "risk-control", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(schemaSQL)); err != nil {
		t.Fatal(err)
	}

	asOf := time.Date(2026, 10, 29, 2, 0, 0, 0, time.UTC)
	_, err = pool.Exec(ctx, `
		INSERT INTO risk_activity_events(event_id,subject_type,subject_id,event_type,amount_minor,currency,occurred_at)
		VALUES
		('in-1','organization','org-risky','funds_in',100000,'USD',$1),
		('out-1','organization','org-risky','funds_out',95000,'USD',$2)`, asOf.Add(-4*time.Hour), asOf.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	scanner := NewOfflineScanner(NewPostgresStore(pool))
	cmd := OfflineRunCommand{RunID: "offline-run-1", AsOf: asOf, PolicyKey: "rapid_fund_movement", PolicyVersion: 1}
	first, replay, err := scanner.Run(ctx, cmd)
	if err != nil || replay || first.FindingsCreated != 1 {
		t.Fatalf("first run: result=%+v replay=%v err=%v", first, replay, err)
	}
	var matchedStrategyKey, matchedRuleKey string
	if err := pool.QueryRow(ctx, `
		SELECT matched_strategy_key, matched_rule_key
		FROM risk_offline_evaluations
		WHERE run_id=$1 AND subject_type='organization' AND subject_id='org-risky' AND currency='USD'`,
		cmd.RunID,
	).Scan(&matchedStrategyKey, &matchedRuleKey); err != nil {
		t.Fatal(err)
	}
	if matchedStrategyKey != "block_rapid_fund_movement" {
		t.Fatalf("persisted matched strategy=%q, want block_rapid_fund_movement", matchedStrategyKey)
	}
	if matchedRuleKey != "rapid_fund_movement" {
		t.Fatalf("persisted matched rule=%q, want rapid_fund_movement", matchedRuleKey)
	}
	second, replay, err := scanner.Run(ctx, cmd)
	if err != nil || !replay || second.RunID != first.RunID {
		t.Fatalf("exact replay: result=%+v replay=%v err=%v", second, replay, err)
	}
	_, _, err = scanner.Run(ctx, OfflineRunCommand{RunID: cmd.RunID, AsOf: asOf.Add(time.Hour), PolicyKey: cmd.PolicyKey, PolicyVersion: cmd.PolicyVersion})
	var conflict ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("changed reuse must conflict, got %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE risk_restrictions SET restriction_id='legacy-restriction-id'`); err != nil {
		t.Fatal(err)
	}
	recomputed, replay, err := scanner.Run(ctx, OfflineRunCommand{
		RunID: "offline-run-2", AsOf: asOf, PolicyKey: cmd.PolicyKey, PolicyVersion: cmd.PolicyVersion,
	})
	if err != nil || replay {
		t.Fatalf("recomputation must preserve a new finding without duplicating the restriction: result=%+v replay=%v err=%v", recomputed, replay, err)
	}
	if recomputed.FindingsCreated != 1 || recomputed.RestrictionsActivated != 0 {
		t.Fatalf("recomputed counts=%+v, want one new finding and no new restriction", recomputed)
	}

	var runs, findings, restrictions int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM risk_offline_runs),
		(SELECT count(*) FROM risk_findings),
		(SELECT count(*) FROM risk_restrictions)`).Scan(&runs, &findings, &restrictions); err != nil {
		t.Fatal(err)
	}
	if runs != 2 || findings != 2 || restrictions != 1 {
		t.Fatalf("runs=%d findings=%d restrictions=%d", runs, findings, restrictions)
	}
}
