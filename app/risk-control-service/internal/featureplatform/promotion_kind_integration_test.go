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

func TestKindPromotionSwitchesToRedisAndRollbackRestoresClickHouseDecision(t *testing.T) {
	clickHouseEndpoint := os.Getenv("TEST_CLICKHOUSE_ENDPOINT")
	redisAddress := os.Getenv("TEST_REDIS_ADDR")
	if clickHouseEndpoint == "" || redisAddress == "" {
		t.Skip("TEST_CLICKHOUSE_ENDPOINT and TEST_REDIS_ADDR are required for Kind promotion integration")
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
	subject := featureplatform.Subject{Type: "organization", Keys: map[string]string{
		"subject_id": "organization-risk-demo-001", "currency": "USD",
	}}
	referenceCutoff := time.UnixMilli(1794096000000).UTC()
	referenceRows, err := clickHouse.ReadLatestBefore(ctx, featureplatform.SnapshotReadRequest{
		RequestID: "s5-reference", FeatureSetKey: draft.Key, FeatureSetVersion: draft.Version,
		FeatureKeys: []string{"inflow_minor", "outflow_ratio"}, Subjects: []featureplatform.Subject{subject},
		Cutoff: referenceCutoff,
	})
	if err != nil || len(referenceRows) != 1 {
		t.Fatalf("reference rows=%d err=%v", len(referenceRows), err)
	}

	redisClient := redis.NewClient(&redis.Options{Addr: redisAddress})
	t.Cleanup(func() { _ = redisClient.Close() })
	prefix := fmt.Sprintf("test:feature-promotion:%d", time.Now().UnixNano())
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
	materializer := featureplatform.NewCurrentMaterializer(registry, onlineStore)
	if result, err := materializer.MaterializeCurrent(ctx, referenceRows[0]); err != nil || result != featureplatform.CurrentWriteApplied {
		t.Fatalf("initial materialization result=%q err=%v", result, err)
	}

	recorder := featureplatform.NewPostgresShadowComparisonRecorder(pool, schema)
	evidenceStart := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	for index := 0; index < 3; index++ {
		comparedAt := evidenceStart.Add(time.Duration(index)*time.Hour + 5*time.Minute)
		cutoff := referenceRows[0].AsOf.Add(30 * time.Second)
		if err := recorder.Record(ctx, []featureplatform.FeatureShadowComparison{{
			RequestID: fmt.Sprintf("s5-clean-%d", index), FeatureSetKey: draft.Key,
			FeatureSetVersion: draft.Version, Subject: subject, Cutoff: cutoff,
			CandidateAsOf: referenceRows[0].AsOf, ReferenceAsOf: referenceRows[0].AsOf,
			CandidateReadDuration: 5 * time.Millisecond, ReferenceReadDuration: 25 * time.Millisecond,
			Status: featureplatform.ShadowComparisonMatched, ComparedAt: comparedAt,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	repository := featureplatform.NewPostgresPromotionRepository(pool, schema)
	controller := featureplatform.NewPromotionController(repository, repository)
	promotion, err := controller.AssessAndPromote(ctx, featureplatform.PromotionRequest{
		RequestID: "s5-promote", FeatureSetKey: draft.Key, FeatureSetVersion: draft.Version,
		EvidenceStart: evidenceStart, EvidenceEnd: evidenceStart.Add(3 * time.Hour), BucketDuration: time.Hour,
		Criteria: featureplatform.PromotionCriteria{
			MinimumComparisons: 3, MinimumDistinctBuckets: 3,
			MaximumMismatchRate: 0, MaximumUnavailableRate: 0,
			MaximumCandidateFreshnessLag:    time.Minute,
			MaximumP95CandidateReadDuration: 20 * time.Millisecond,
		},
		RequestedBy: "feature-operator", Reason: "kind clean evidence",
	})
	if err != nil || !promotion.Assessment.Eligible || promotion.Route.Mode != featureplatform.ServingModeOnline {
		t.Fatalf("promotion=%+v err=%v", promotion, err)
	}

	routedReader := featureplatform.NewRoutedFeatureSnapshotReader(repository, onlineStore, clickHouse, recorder)
	runtime := newPromotionPolicyRuntime(t, ctx, registry, routedReader, draft)
	onlineDecision, err := runtime.EvaluateLatestBefore(ctx, featureplatform.FeaturePolicyEvaluationRequest{
		Principal: "risk-control-service", RequestID: "s5-online-good", Subject: subject, Cutoff: referenceCutoff,
	})
	if err != nil || onlineDecision.Decision != "block" {
		t.Fatalf("online decision=%+v err=%v", onlineDecision, err)
	}

	drifted := referenceRows[0]
	drifted.AsOf = referenceRows[0].AsOf.Add(time.Minute)
	drifted.ComputedAt = drifted.AsOf.Add(time.Second)
	drifted.SourceRunID = "s5-deliberate-drift"
	drifted.ContentFingerprint = "deliberately-drifted-online-snapshot"
	drifted.Values = map[string]any{
		"inflow_minor":  referenceRows[0].Values["inflow_minor"],
		"outflow_ratio": 0.10,
	}
	if result, err := materializer.MaterializeCurrent(ctx, drifted); err != nil || result != featureplatform.CurrentWriteApplied {
		t.Fatalf("drift materialization result=%q err=%v", result, err)
	}
	driftDecision, err := runtime.EvaluateLatestBefore(ctx, featureplatform.FeaturePolicyEvaluationRequest{
		Principal: "risk-control-service", RequestID: "s5-online-drift", Subject: subject, Cutoff: drifted.AsOf,
	})
	if err != nil || driftDecision.Decision != "pass" {
		t.Fatalf("drift decision=%+v err=%v", driftDecision, err)
	}

	rolledBack, err := controller.Rollback(ctx, featureplatform.ServingRollbackRequest{
		RequestID: "s5-rollback", FeatureSetKey: draft.Key, FeatureSetVersion: draft.Version,
		RequestedBy: "feature-operator", Reason: "deliberate drift drill",
	})
	if err != nil || rolledBack.Mode != featureplatform.ServingModeShadow {
		t.Fatalf("rollback=%+v err=%v", rolledBack, err)
	}
	recoveredDecision, err := runtime.EvaluateLatestBefore(ctx, featureplatform.FeaturePolicyEvaluationRequest{
		Principal: "risk-control-service", RequestID: "s5-after-rollback", Subject: subject, Cutoff: drifted.AsOf,
	})
	if err != nil || recoveredDecision.Decision != "block" {
		t.Fatalf("recovered decision=%+v err=%v", recoveredDecision, err)
	}
}

func newPromotionPolicyRuntime(
	t *testing.T,
	ctx context.Context,
	registry *featureplatform.PostgresRegistry,
	reader featureplatform.FeatureSnapshotReader,
	draft featureplatform.FeatureSetDraft,
) *featureplatform.FeaturePolicyRuntime {
	t.Helper()
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
			PolicyKey: "promotion_policy", Version: 1,
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
	serving := featureplatform.NewServingService(
		registry, reader,
		featureplatform.NewAllowListFeatureAuthorizer(map[string][]string{"risk-control-service": {draft.Key}}),
	)
	runtime, err := featureplatform.NewFeaturePolicyRuntime(policy, plan, serving)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}
