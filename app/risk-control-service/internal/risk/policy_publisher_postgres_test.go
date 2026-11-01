package risk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPolicyPublisherAtomicallyPublishesValidDraft(t *testing.T) {
	ctx := context.Background()
	pool := newPolicyPublisherTestPool(t, ctx)
	insertPolicyPublisherFixture(t, ctx, pool, "withdrawal_risk", "draft", "inflow_minor_24h >= 50000 && outflow_ratio_24h >= 0.90")

	publisher := NewPolicyPublisher(NewPostgresStore(pool), NewRPCFactorAdapterRegistry())
	publication, err := publisher.Publish(ctx, "withdrawal_risk", 1)
	if err != nil {
		t.Fatal(err)
	}
	if publication.ContentFingerprint == "" || publication.Snapshot == nil {
		t.Fatalf("unexpected publication: %+v", publication)
	}

	evaluation, err := publication.Snapshot.Evaluate(ctx, map[string]any{
		"inflow_minor_24h":  int64(100_000),
		"outflow_minor_24h": int64(95_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Decision != "block" || len(evaluation.Actions) != 1 || evaluation.Actions[0].ActionCode != "activate_restriction" {
		t.Fatalf("unexpected published evaluation: %+v", evaluation)
	}

	var status, fingerprint string
	var publishedAt *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT status, content_fingerprint, published_at
		FROM risk_decision_policy_versions
		WHERE policy_key='withdrawal_risk' AND policy_version=1`).
		Scan(&status, &fingerprint, &publishedAt); err != nil {
		t.Fatal(err)
	}
	if status != "published" || fingerprint != publication.ContentFingerprint || publishedAt == nil {
		t.Fatalf("status=%s fingerprint=%q published_at=%v", status, fingerprint, publishedAt)
	}
}

func TestPolicyPublisherLeavesInvalidDraftUnpublished(t *testing.T) {
	ctx := context.Background()
	pool := newPolicyPublisherTestPool(t, ctx)
	insertPolicyPublisherFixture(t, ctx, pool, "invalid_policy", "draft", "missing_factor > 0")

	publisher := NewPolicyPublisher(NewPostgresStore(pool), NewRPCFactorAdapterRegistry())
	if _, err := publisher.Publish(ctx, "invalid_policy", 1); err == nil {
		t.Fatal("expected invalid CEL rule to prevent publication")
	}

	var status, fingerprint string
	var publishedAt *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT status, content_fingerprint, published_at
		FROM risk_decision_policy_versions
		WHERE policy_key='invalid_policy' AND policy_version=1`).
		Scan(&status, &fingerprint, &publishedAt); err != nil {
		t.Fatal(err)
	}
	if status != "draft" || fingerprint != "" || publishedAt != nil {
		t.Fatalf("invalid policy changed: status=%s fingerprint=%q published_at=%v", status, fingerprint, publishedAt)
	}
}

func TestPublishedPolicyDefinitionIsImmutable(t *testing.T) {
	ctx := context.Background()
	pool := newPolicyPublisherTestPool(t, ctx)
	insertPolicyPublisherFixture(t, ctx, pool, "immutable_policy", "draft", "inflow_minor_24h >= 50000")

	publisher := NewPolicyPublisher(NewPostgresStore(pool), NewRPCFactorAdapterRegistry())
	if _, err := publisher.Publish(ctx, "immutable_policy", 1); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE risk_decision_rules
		SET expression='true'
		WHERE policy_key='immutable_policy' AND policy_version=1 AND rule_key='rapid_outflow'`); err == nil {
		t.Fatal("expected database to reject mutation of a published rule")
	}

	var expression string
	if err := pool.QueryRow(ctx, `
		SELECT expression
		FROM risk_decision_rules
		WHERE policy_key='immutable_policy' AND policy_version=1 AND rule_key='rapid_outflow'`).
		Scan(&expression); err != nil {
		t.Fatal(err)
	}
	if expression != "inflow_minor_24h >= 50000" {
		t.Fatalf("published rule was mutated: %q", expression)
	}
}

func newPolicyPublisherTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is required for PostgreSQL integration tests")
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)

	schemaName := fmt.Sprintf("policy_publisher_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schemaName+" CASCADE") })

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	schemaSQL, err := os.ReadFile(filepath.Join("..", "..", "cmd", "risk-control", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(schemaSQL)); err != nil {
		t.Fatal(err)
	}
	return pool
}

func insertPolicyPublisherFixture(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	policyKey string,
	status string,
	ruleExpression string,
) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`
		INSERT INTO risk_decision_policy_versions(
			policy_key, policy_version, status, default_decision, default_reason_codes)
		VALUES ($1, 1, $2, 'pass', '["policy_not_matched"]')`, []any{policyKey, status}},
		{`
		INSERT INTO risk_decision_factor_definitions(
			policy_key, policy_version, factor_key, factor_type, value_type, param_key, expression, resolver_key, timeout_ms)
		VALUES
			($1, 1, 'inflow_minor_24h', 'value', 'int', 'inflow_minor_24h', '', '', 0),
			($1, 1, 'outflow_minor_24h', 'value', 'int', 'outflow_minor_24h', '', '', 0),
			($1, 1, 'outflow_ratio_24h', 'expression', 'double', '', 'double(outflow_minor_24h) / double(inflow_minor_24h)', '', 0)`, []any{policyKey}},
		{`
		INSERT INTO risk_decision_rules(
			policy_key, policy_version, rule_key, rule_version, expression)
		VALUES ($1, 1, 'rapid_outflow', 1, $2)`, []any{policyKey, ruleExpression}},
		{`
		INSERT INTO risk_decision_strategies(
			policy_key, policy_version, strategy_key, rule_key, priority, decision, reason_codes)
		VALUES ($1, 1, 'block_rapid_outflow', 'rapid_outflow', 100, 'block', '["rapid_outflow_detected"]')`, []any{policyKey}},
		{`
		INSERT INTO risk_decision_strategy_actions(
			policy_key, policy_version, strategy_key, sequence_no, action_code, params)
		VALUES ($1, 1, 'block_rapid_outflow', 1, 'activate_restriction', '{"capability":"withdrawal"}')`, []any{policyKey}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}
