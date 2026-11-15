package featureplatform_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/featureplatform"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/risk"
)

func TestKindRedisOnlineStoreMatchesClickHouseAndPreservesDecision(t *testing.T) {
	clickHouseEndpoint := os.Getenv("TEST_CLICKHOUSE_ENDPOINT")
	redisAddress := os.Getenv("TEST_REDIS_ADDR")
	if clickHouseEndpoint == "" || redisAddress == "" {
		t.Skip("TEST_CLICKHOUSE_ENDPOINT and TEST_REDIS_ADDR are required for Kind Online Store integration")
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

	clickHouse, err := featureplatform.NewClickHouseFeatureReader(
		clickHouseEndpoint, os.Getenv("TEST_CLICKHOUSE_USER"), os.Getenv("TEST_CLICKHOUSE_PASSWORD"),
		&http.Client{Timeout: 15 * time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.UnixMilli(1794096000000).UTC()
	subject := featureplatform.Subject{Type: "organization", Keys: map[string]string{
		"subject_id": "organization-risk-demo-001", "currency": "USD",
	}}
	featureKeys := []string{"inflow_minor", "outflow_ratio"}
	referenceSnapshots, err := clickHouse.ReadLatestBefore(ctx, featureplatform.SnapshotReadRequest{
		RequestID: "kind-online-materialize", FeatureSetKey: draft.Key,
		FeatureSetVersion: draft.Version, FeatureKeys: featureKeys,
		Subjects: []featureplatform.Subject{subject}, Cutoff: cutoff,
	})
	if err != nil || len(referenceSnapshots) != 1 {
		t.Fatalf("read ClickHouse reference snapshots=%d err=%v", len(referenceSnapshots), err)
	}

	redisClient := redis.NewClient(&redis.Options{Addr: redisAddress})
	t.Cleanup(func() { _ = redisClient.Close() })
	prefix := fmt.Sprintf("test:feature-shadow:%d", time.Now().UnixNano())
	t.Cleanup(func() {
		keys, _ := redisClient.Keys(context.Background(), prefix+":*").Result()
		if len(keys) > 0 {
			_ = redisClient.Del(context.Background(), keys...).Err()
		}
	})
	onlineStore, err := featureplatform.NewRedisCurrentFeatureStore(redisClient, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := onlineStore.PutCurrent(ctx, referenceSnapshots[0]); err != nil || result != featureplatform.CurrentWriteApplied {
		t.Fatalf("materialize Redis current result=%q err=%v", result, err)
	}
	recorder := featureplatform.NewPostgresShadowComparisonRecorder(pool, schema)
	shadowReader := featureplatform.NewShadowFeatureSnapshotReader(onlineStore, clickHouse, recorder)
	serving := featureplatform.NewServingService(
		registry, shadowReader,
		featureplatform.NewAllowListFeatureAuthorizer(map[string][]string{"risk-control-service": {draft.Key}}),
	)
	factors := []risk.FactorDefinition{
		{Key: "inflow_minor_24h", Type: risk.FactorTypeFeature, ValueType: risk.FactorValueTypeInt, FeatureSetKey: draft.Key, FeatureSetVersion: draft.Version, FeatureKey: "inflow_minor"},
		{Key: "outflow_ratio_24h", Type: risk.FactorTypeFeature, ValueType: risk.FactorValueTypeDouble, FeatureSetKey: draft.Key, FeatureSetVersion: draft.Version, FeatureKey: "outflow_ratio"},
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
			PolicyKey: "online_store_shadow", Version: 1,
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
	runtime, err := featureplatform.NewFeaturePolicyRuntime(policy, plan, serving)
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := runtime.EvaluateLatestBefore(ctx, featureplatform.FeaturePolicyEvaluationRequest{
		Principal: "risk-control-service", RequestID: "kind-online-shadow-decision",
		Subject: subject, Cutoff: cutoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Decision != "block" || len(evaluation.Actions) != 1 {
		t.Fatalf("unexpected shadow decision: %+v", evaluation)
	}
	var matched int
	if err := pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT count(*) FROM %s.feature_serving_shadow_comparisons
		WHERE request_id='kind-online-shadow-decision' AND comparison_status='matched'`, schema)).Scan(&matched); err != nil {
		t.Fatal(err)
	}
	if matched != 1 {
		t.Fatalf("matched comparisons=%d, want one", matched)
	}

	// Now deliberately materialize a wrong Online Store value. Shadow mode must
	// record the drift while preserving the ClickHouse-backed decision.
	mismatchStore, err := featureplatform.NewRedisCurrentFeatureStore(redisClient, prefix+":mismatch", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mismatchSnapshot := referenceSnapshots[0]
	mismatchSnapshot.ContentFingerprint = "deliberately-different-online-fingerprint"
	mismatchSnapshot.Values = map[string]any{
		"inflow_minor":  referenceSnapshots[0].Values["inflow_minor"],
		"outflow_ratio": 0.10,
	}
	if result, err := mismatchStore.PutCurrent(ctx, mismatchSnapshot); err != nil || result != featureplatform.CurrentWriteApplied {
		t.Fatalf("materialize mismatched Redis current result=%q err=%v", result, err)
	}
	mismatchReader := featureplatform.NewShadowFeatureSnapshotReader(mismatchStore, clickHouse, recorder)
	mismatchServing := featureplatform.NewServingService(
		registry, mismatchReader,
		featureplatform.NewAllowListFeatureAuthorizer(map[string][]string{"risk-control-service": {draft.Key}}),
	)
	mismatchRuntime, err := featureplatform.NewFeaturePolicyRuntime(policy, plan, mismatchServing)
	if err != nil {
		t.Fatal(err)
	}
	mismatchEvaluation, err := mismatchRuntime.EvaluateLatestBefore(ctx, featureplatform.FeaturePolicyEvaluationRequest{
		Principal: "risk-control-service", RequestID: "kind-online-shadow-mismatch",
		Subject: subject, Cutoff: cutoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if mismatchEvaluation.Decision != "block" || len(mismatchEvaluation.Actions) != 1 {
		t.Fatalf("mismatched candidate changed reference decision: %+v", mismatchEvaluation)
	}
	var mismatched int
	if err := pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT count(*) FROM %s.feature_serving_shadow_comparisons
		WHERE request_id='kind-online-shadow-mismatch'
		  AND comparison_status='mismatch'
		  AND differences @> '["value:outflow_ratio"]'::jsonb`, schema)).Scan(&mismatched); err != nil {
		t.Fatal(err)
	}
	if mismatched != 1 {
		t.Fatalf("mismatched comparisons=%d, want one", mismatched)
	}
}
