package featureplatform_test

import (
	"context"
	"math"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/featureplatform"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/risk"
)

func TestKindFeatureFactorsDriveCELDecisionWithSnapshotEvidence(t *testing.T) {
	endpoint := os.Getenv("TEST_CLICKHOUSE_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_CLICKHOUSE_ENDPOINT is required for Kind Feature Factor integration")
	}
	ctx := context.Background()
	pool, schema := newRegistryTestPool(t, ctx)
	registry := featureplatform.NewPostgresRegistry(pool, schema)
	registerFundActivityPrerequisites(t, ctx, registry)
	draft := fundActivityDraft()
	if err := registry.SaveDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Publish(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
	factors := []risk.FactorDefinition{
		{Key: "inflow_minor_24h", Type: risk.FactorTypeFeature, ValueType: risk.FactorValueTypeInt, FeatureSetKey: draft.Key, FeatureSetVersion: 1, FeatureKey: "inflow_minor"},
		{Key: "outflow_ratio_24h", Type: risk.FactorTypeFeature, ValueType: risk.FactorValueTypeDouble, FeatureSetKey: draft.Key, FeatureSetVersion: 1, FeatureKey: "outflow_ratio"},
	}
	plan, err := featureplatform.LoadFeatureFetchPlan(ctx, registry, factors)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := featureplatform.NewFeatureFactorProvider(plan)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := risk.CompilePolicySnapshotWithProviders(
		risk.ExecutablePolicyDefinition{
			PolicyKey: "feature_factor_shadow", Version: 1,
			DefaultDecision: risk.DecisionTemplate{Decision: "pass"},
			Strategies: []risk.ExecutableStrategyDefinition{{
				StrategyKey: "block_high_outflow", Priority: 100,
				Rule: risk.CELRule{RuleKey: "high_outflow", Version: 1, Expression: "inflow_minor_24h >= 50000 && outflow_ratio_24h >= 0.80"},
				Effect: risk.StrategyEffect{
					Decision: risk.DecisionTemplate{Decision: "block", ReasonCodes: []string{"high_outflow"}},
					Actions:  []risk.PolicyAction{{ActionCode: "activate_restriction", Params: map[string]any{"capability": "withdrawal"}}},
				},
			}},
		}, factors, risk.NewRPCFactorAdapterRegistry(), []risk.FactorProvider{provider},
	)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := featureplatform.NewClickHouseFeatureReader(
		endpoint, os.Getenv("TEST_CLICKHOUSE_USER"), os.Getenv("TEST_CLICKHOUSE_PASSWORD"),
		&http.Client{Timeout: 15 * time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	serving := featureplatform.NewServingService(
		registry, reader,
		featureplatform.NewAllowListFeatureAuthorizer(map[string][]string{"risk-control-service": {draft.Key}}),
	)
	runtime, err := featureplatform.NewFeaturePolicyRuntime(policy, plan, serving)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.UnixMilli(1794096000000).UTC()
	evaluation, err := runtime.EvaluateLatestBefore(ctx, featureplatform.FeaturePolicyEvaluationRequest{
		Principal: "risk-control-service", RequestID: "kind-feature-factor-shadow",
		Subject: featureplatform.Subject{Type: "organization", Keys: map[string]string{
			"subject_id": "organization-risk-demo-001", "currency": "USD",
		}},
		Cutoff: cutoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Decision != "block" || evaluation.MatchedRuleKey != "high_outflow" || len(evaluation.Actions) != 1 {
		t.Fatalf("unexpected decision: %+v", evaluation)
	}
	for _, factorKey := range []string{"inflow_minor_24h", "outflow_ratio_24h"} {
		factor, found := evaluation.Factors.Result(factorKey)
		if !found || factor.Evidence == nil || factor.Evidence.SourceRunID != "risk-domain-demo-20261108-fund" || !factor.Evidence.SnapshotAsOf.Equal(cutoff) {
			t.Fatalf("factor %s has incomplete evidence: %+v", factorKey, factor)
		}
	}
}

func TestKindServingReadsPublishedDefinitionAndClickHouseSnapshot(t *testing.T) {
	endpoint := os.Getenv("TEST_CLICKHOUSE_ENDPOINT")
	if endpoint == "" {
		t.Skip("TEST_CLICKHOUSE_ENDPOINT is required for Kind Feature Serving integration")
	}
	ctx := context.Background()
	pool, schema := newRegistryTestPool(t, ctx)
	registry := featureplatform.NewPostgresRegistry(pool, schema)
	registerFundActivityPrerequisites(t, ctx, registry)
	draft := fundActivityDraft()
	if err := registry.SaveDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Publish(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
	reader, err := featureplatform.NewClickHouseFeatureReader(
		endpoint,
		os.Getenv("TEST_CLICKHOUSE_USER"),
		os.Getenv("TEST_CLICKHOUSE_PASSWORD"),
		&http.Client{Timeout: 15 * time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := featureplatform.NewServingService(
		registry,
		reader,
		featureplatform.NewAllowListFeatureAuthorizer(map[string][]string{
			"risk-control-service": {"subject_fund_activity"},
		}),
	)
	cutoff := time.UnixMilli(1794096000000).UTC()
	response, err := service.BatchGetLatestBefore(ctx, featureplatform.BatchGetLatestBeforeRequest{
		Principal: "risk-control-service",
		RequestID: "kind-serving-integration",
		Subjects: []featureplatform.Subject{{Type: "organization", Keys: map[string]string{
			"subject_id": "organization-risk-demo-001",
			"currency":   "USD",
		}}},
		Features: []featureplatform.FeatureRef{
			{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "inflow_minor"},
			{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "outflow_minor"},
			{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "outflow_ratio"},
		},
		Cutoff: cutoff,
		MaxAge: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Values) != 3 {
		t.Fatalf("values=%d, want 3", len(response.Values))
	}
	if response.Values[0].Status != featureplatform.ValueStatusSuccess || response.Values[0].Value != int64(200_000) {
		t.Fatalf("unexpected inflow: %+v", response.Values[0])
	}
	if response.Values[1].Status != featureplatform.ValueStatusSuccess || response.Values[1].Value != int64(175_000) {
		t.Fatalf("unexpected outflow: %+v", response.Values[1])
	}
	ratio, ok := response.Values[2].Value.(float64)
	if response.Values[2].Status != featureplatform.ValueStatusSuccess || !ok || math.Abs(ratio-0.875) > 0.000001 {
		t.Fatalf("unexpected ratio: %+v", response.Values[2])
	}
	if response.Values[0].SourceRunID != "risk-domain-demo-20261108-fund" || !response.Values[0].SnapshotAsOf.Equal(cutoff) {
		t.Fatalf("unexpected evidence: %+v", response.Values[0])
	}
	historical, err := service.BatchGetAt(ctx, featureplatform.BatchGetAtRequest{
		Principal: "risk-control-service",
		RequestID: "kind-serving-history-integration",
		Subjects: []featureplatform.Subject{{Type: "organization", Keys: map[string]string{
			"subject_id": "organization-risk-demo-001",
			"currency":   "USD",
		}}},
		Features: []featureplatform.FeatureRef{
			{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "outflow_ratio"},
		},
		AsOf: cutoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(historical.Values) != 1 || historical.Values[0].Status != featureplatform.ValueStatusSuccess {
		t.Fatalf("unexpected historical response: %+v", historical)
	}
	historicalRatio, ok := historical.Values[0].Value.(float64)
	if !ok || math.Abs(historicalRatio-0.875) > 0.000001 || !historical.Values[0].SnapshotAsOf.Equal(cutoff) {
		t.Fatalf("unexpected historical ratio: %+v", historical.Values[0])
	}
}
