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

func TestPolicySnapshotLoaderLoadsPublishedPolicyFromPostgres(t *testing.T) {
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

	schemaName := fmt.Sprintf("policy_loader_test_%d", time.Now().UnixNano())
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

	_, err = pool.Exec(ctx, `
		INSERT INTO risk_decision_policy_versions(
			policy_key, policy_version, status, default_decision, default_reason_codes)
		VALUES ('withdrawal_risk', 1, 'draft', 'pass', '["policy_not_matched"]');

		INSERT INTO risk_decision_factor_definitions(
			policy_key, policy_version, factor_key, factor_type, value_type, param_key, expression, resolver_key, timeout_ms)
		VALUES
			('withdrawal_risk', 1, 'inflow_minor_24h', 'value', 'int', 'inflow_minor_24h', '', '', 0),
			('withdrawal_risk', 1, 'outflow_minor_24h', 'value', 'int', 'outflow_minor_24h', '', '', 0),
			('withdrawal_risk', 1, 'outflow_ratio_24h', 'expression', 'double', '', 'double(outflow_minor_24h) / double(inflow_minor_24h)', '', 0);

		INSERT INTO risk_decision_rules(
			policy_key, policy_version, rule_key, rule_version, expression)
		VALUES (
			'withdrawal_risk', 1, 'rapid_outflow', 1,
			'inflow_minor_24h >= 50000 && outflow_ratio_24h >= 0.90');

		INSERT INTO risk_decision_strategies(
			policy_key, policy_version, strategy_key, rule_key, priority, decision, reason_codes)
		VALUES (
			'withdrawal_risk', 1, 'block_rapid_outflow', 'rapid_outflow', 100,
			'block', '["rapid_outflow_detected"]');

		INSERT INTO risk_decision_strategy_actions(
			policy_key, policy_version, strategy_key, sequence_no, action_code, params)
		VALUES (
			'withdrawal_risk', 1, 'block_rapid_outflow', 1,
			'activate_restriction', '{"capability":"withdrawal"}');

		UPDATE risk_decision_policy_versions
		SET status='published', published_at=NOW()
		WHERE policy_key='withdrawal_risk' AND policy_version=1;`)
	if err != nil {
		t.Fatal(err)
	}

	loader := NewPolicySnapshotLoader(NewPostgresStore(pool), NewRPCFactorAdapterRegistry())
	snapshot, err := loader.LoadPublished(ctx, "withdrawal_risk", 1)
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := snapshot.Evaluate(ctx, map[string]any{
		"inflow_minor_24h":  int64(100_000),
		"outflow_minor_24h": int64(95_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Decision != "block" || evaluation.MatchedStrategyKey != "block_rapid_outflow" || evaluation.MatchedRuleKey != "rapid_outflow" {
		t.Fatalf("unexpected evaluation: %+v", evaluation)
	}
	if len(evaluation.Actions) != 1 || evaluation.Actions[0].ActionCode != "activate_restriction" || evaluation.Actions[0].Params["capability"] != "withdrawal" {
		t.Fatalf("unexpected actions: %+v", evaluation.Actions)
	}
}
