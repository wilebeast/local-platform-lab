package featureplatform

import (
	"context"
	"testing"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/risk"
)

func TestFeaturePolicyRuntimeFetchesFactorsOnceAndPreservesEvidence(t *testing.T) {
	cutoff := time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
	factors := featurePolicyFactorDefinitions()
	plan, err := CompileFeatureFetchPlan(factors, []ServingFeatureDefinition{
		{Ref: FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "inflow_minor"}, ValueType: "int", MaxAge: time.Hour},
		{Ref: FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "outflow_ratio"}, ValueType: "double", MaxAge: time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewFeatureFactorProvider(plan)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := risk.CompilePolicySnapshotWithProviders(
		risk.ExecutablePolicyDefinition{
			PolicyKey: "withdrawal_risk", Version: 7,
			DefaultDecision: risk.DecisionTemplate{Decision: "pass"},
			Strategies: []risk.ExecutableStrategyDefinition{{
				StrategyKey: "block_rapid_outflow", Priority: 100,
				Rule: risk.CELRule{RuleKey: "rapid_outflow", Version: 1, Expression: "inflow_minor_24h >= 50000 && outflow_ratio_24h >= 0.90"},
				Effect: risk.StrategyEffect{
					Decision: risk.DecisionTemplate{Decision: "block", ReasonCodes: []string{"rapid_outflow"}},
					Actions:  []risk.PolicyAction{{ActionCode: "activate_restriction", Params: map[string]any{"capability": "withdrawal"}}},
				},
			}},
		},
		factors,
		risk.NewRPCFactorAdapterRegistry(),
		[]risk.FactorProvider{provider},
	)
	if err != nil {
		t.Fatal(err)
	}
	refs := plan.featureRefs()
	serving := &recordingFeatureServing{response: BatchGetResponse{
		RequestID: "evaluation-001",
		Values: []FeatureValueResult{
			featureValue(refs[0], "int", int64(100_000), cutoff),
			featureValue(refs[1], "double", float64(0.95), cutoff),
		},
	}}
	runtime, err := NewFeaturePolicyRuntime(policy, plan, serving)
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := runtime.EvaluateLatestBefore(context.Background(), FeaturePolicyEvaluationRequest{
		Principal: "risk-control-service", RequestID: "evaluation-001",
		Subject: Subject{Type: "organization", Keys: map[string]string{"subject_id": "org-001", "currency": "USD"}},
		Cutoff:  cutoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if serving.latestCalls != 1 {
		t.Fatalf("feature serving calls=%d, want one fetch plan read", serving.latestCalls)
	}
	if evaluation.Decision != "block" || evaluation.MatchedRuleKey != "rapid_outflow" || len(evaluation.Actions) != 1 {
		t.Fatalf("unexpected policy evaluation: %+v", evaluation)
	}
	for _, factorKey := range []string{"inflow_minor_24h", "outflow_ratio_24h"} {
		factor, found := evaluation.Factors.Result(factorKey)
		if !found || factor.Status != risk.FactorStatusSuccess || factor.Evidence == nil {
			t.Fatalf("factor %s is missing evidence: %+v", factorKey, factor)
		}
		if factor.Evidence.SourceRunID != "feature-run-001" || !factor.Evidence.SnapshotAsOf.Equal(cutoff.Add(-time.Minute)) {
			t.Fatalf("unexpected factor evidence for %s: %+v", factorKey, factor.Evidence)
		}
	}
}

func TestFeatureFactorProviderRejectsReferenceOutsideFetchPlan(t *testing.T) {
	factors := []risk.FactorDefinition{{
		Key: "known", Type: risk.FactorTypeFeature, ValueType: risk.FactorValueTypeInt,
		FeatureSetKey: "set", FeatureSetVersion: 1, FeatureKey: "value",
	}}
	plan, err := CompileFeatureFetchPlan(factors, []ServingFeatureDefinition{{
		Ref:       FeatureRef{FeatureSetKey: "set", FeatureSetVersion: 1, FeatureKey: "value"},
		ValueType: "int", MaxAge: time.Minute,
	}})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewFeatureFactorProvider(plan)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.NewFactor(risk.FactorDefinition{
		Key: "unknown", Type: risk.FactorTypeFeature, ValueType: risk.FactorValueTypeInt,
		FeatureSetKey: "set", FeatureSetVersion: 1, FeatureKey: "other",
	})
	if err == nil {
		t.Fatal("expected feature reference outside the fetch plan to be rejected")
	}
}

func TestFeatureFetchPlanAllowsMultipleFactorsToShareOneFeature(t *testing.T) {
	sharedRef := FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "outflow_ratio"}
	factors := []risk.FactorDefinition{
		{Key: "outflow_ratio_24h", Type: risk.FactorTypeFeature, ValueType: risk.FactorValueTypeDouble, FeatureSetKey: sharedRef.FeatureSetKey, FeatureSetVersion: sharedRef.FeatureSetVersion, FeatureKey: sharedRef.FeatureKey},
		{Key: "withdrawal_velocity_ratio", Type: risk.FactorTypeFeature, ValueType: risk.FactorValueTypeDouble, FeatureSetKey: sharedRef.FeatureSetKey, FeatureSetVersion: sharedRef.FeatureSetVersion, FeatureKey: sharedRef.FeatureKey},
	}
	plan, err := CompileFeatureFetchPlan(factors, []ServingFeatureDefinition{{
		Ref: sharedRef, ValueType: "double", MaxAge: time.Minute,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries()) != 2 {
		t.Fatalf("plan entries=%d, want two factor mappings", len(plan.Entries()))
	}
	if len(plan.featureRefs()) != 1 {
		t.Fatalf("feature refs=%d, want one deduplicated serving read", len(plan.featureRefs()))
	}
}

func TestFeaturePolicyRuntimeEvaluatesExactHistoricalSnapshotOnce(t *testing.T) {
	asOf := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	runtime, serving := newFeaturePolicyRuntimeForTest(t, BatchGetResponse{
		RequestID: "offline-evaluation-001",
		Values: []FeatureValueResult{
			featureValue(FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "inflow_minor"}, "int", int64(100_000), asOf.Add(time.Minute)),
			featureValue(FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "outflow_ratio"}, "double", float64(0.95), asOf.Add(time.Minute)),
		},
	})
	for i := range serving.response.Values {
		serving.response.Values[i].SnapshotAsOf = asOf
	}

	evaluation, err := runtime.EvaluateAt(context.Background(), FeaturePolicyHistoricalEvaluationRequest{
		Principal: "risk-control-offline", RequestID: "offline-evaluation-001",
		Subject: Subject{Type: "organization", Keys: map[string]string{"subject_id": "org-001", "currency": "USD"}},
		AsOf:    asOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if serving.atCalls != 1 || serving.latestCalls != 0 {
		t.Fatalf("historical calls at=%d latest=%d, want one exact read", serving.atCalls, serving.latestCalls)
	}
	if evaluation.Decision != "block" || evaluation.MatchedRuleKey != "rapid_outflow" {
		t.Fatalf("unexpected historical evaluation: %+v", evaluation)
	}
}

func TestFeaturePolicyRuntimeFailsClosedWhenFeatureIsStale(t *testing.T) {
	cutoff := time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
	stale := featureValue(
		FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "inflow_minor"},
		"int", int64(100_000), cutoff,
	)
	stale.Status = ValueStatusStale
	stale.ErrorCode = "max_age_exceeded"
	runtime, _ := newFeaturePolicyRuntimeForTest(t, BatchGetResponse{
		RequestID: "stale-evaluation-001",
		Values: []FeatureValueResult{
			stale,
			featureValue(FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "outflow_ratio"}, "double", float64(0.95), cutoff),
		},
	})

	_, err := runtime.EvaluateLatestBefore(context.Background(), FeaturePolicyEvaluationRequest{
		Principal: "risk-control-service", RequestID: "stale-evaluation-001",
		Subject: Subject{Type: "organization", Keys: map[string]string{"subject_id": "org-001", "currency": "USD"}},
		Cutoff:  cutoff,
	})
	if err == nil {
		t.Fatal("expected stale feature factor to prevent a policy decision")
	}
}

func newFeaturePolicyRuntimeForTest(t *testing.T, response BatchGetResponse) (*FeaturePolicyRuntime, *recordingFeatureServing) {
	t.Helper()
	factors := featurePolicyFactorDefinitions()
	plan, err := CompileFeatureFetchPlan(factors, []ServingFeatureDefinition{
		{Ref: FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "inflow_minor"}, ValueType: "int", MaxAge: time.Hour},
		{Ref: FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "outflow_ratio"}, ValueType: "double", MaxAge: time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewFeatureFactorProvider(plan)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := risk.CompilePolicySnapshotWithProviders(
		risk.ExecutablePolicyDefinition{
			PolicyKey: "withdrawal_risk", Version: 7,
			DefaultDecision: risk.DecisionTemplate{Decision: "pass"},
			Strategies: []risk.ExecutableStrategyDefinition{{
				StrategyKey: "block_rapid_outflow", Priority: 100,
				Rule:   risk.CELRule{RuleKey: "rapid_outflow", Version: 1, Expression: "inflow_minor_24h >= 50000 && outflow_ratio_24h >= 0.90"},
				Effect: risk.StrategyEffect{Decision: risk.DecisionTemplate{Decision: "block"}},
			}},
		},
		factors, risk.NewRPCFactorAdapterRegistry(), []risk.FactorProvider{provider},
	)
	if err != nil {
		t.Fatal(err)
	}
	serving := &recordingFeatureServing{response: response}
	runtime, err := NewFeaturePolicyRuntime(policy, plan, serving)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, serving
}

func featurePolicyFactorDefinitions() []risk.FactorDefinition {
	return []risk.FactorDefinition{
		{Key: "inflow_minor_24h", Type: risk.FactorTypeFeature, ValueType: risk.FactorValueTypeInt, FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "inflow_minor"},
		{Key: "outflow_ratio_24h", Type: risk.FactorTypeFeature, ValueType: risk.FactorValueTypeDouble, FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "outflow_ratio"},
	}
}

func featureValue(ref FeatureRef, valueType string, value any, cutoff time.Time) FeatureValueResult {
	return FeatureValueResult{
		Subject: Subject{Type: "organization", Keys: map[string]string{"subject_id": "org-001", "currency": "USD"}},
		Ref:     ref, ValueType: valueType, Status: ValueStatusSuccess, Value: value,
		SnapshotAsOf: cutoff.Add(-time.Minute), ComputedAt: cutoff.Add(-30 * time.Second),
		SourceRunID: "feature-run-001", ContentFingerprint: "snapshot-fingerprint",
	}
}

type recordingFeatureServing struct {
	response    BatchGetResponse
	latestCalls int
	atCalls     int
}

func (s *recordingFeatureServing) BatchGetLatestBefore(context.Context, BatchGetLatestBeforeRequest) (BatchGetResponse, error) {
	s.latestCalls++
	return s.response, nil
}

func (s *recordingFeatureServing) BatchGetAt(context.Context, BatchGetAtRequest) (BatchGetResponse, error) {
	s.atCalls++
	return s.response, nil
}
