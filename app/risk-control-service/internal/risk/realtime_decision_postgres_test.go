package risk

import (
	"context"
	"testing"
)

func TestRealtimeDecisionServicePersistsOneEvaluationAndActionForExactRetries(t *testing.T) {
	ctx := context.Background()
	pool := newPolicyPublisherTestPool(t, ctx)
	runtime, err := NewPolicySnapshotRuntime(compileRuntimeTestSnapshotWithAction(t, 1, "block"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewRealtimeDecisionService(pool, runtime)
	command := RealtimeDecisionCommand{
		EvaluationID: "decision-1",
		SubjectType:  "organization",
		SubjectID:    "org-risky",
		Params:       map[string]any{"amount_minor": int64(150_000)},
	}

	first, replay, err := service.Decide(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if replay || first.Decision != "block" || len(first.ActionJobs) != 1 {
		t.Fatalf("first decision: replay=%t result=%+v", replay, first)
	}
	second, replay, err := service.Decide(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if !replay || second.EvaluationID != first.EvaluationID || second.ActionJobs[0].ActionID != first.ActionJobs[0].ActionID {
		t.Fatalf("exact retry did not replay original result: replay=%t result=%+v", replay, second)
	}

	var evaluations, actions int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM risk_realtime_evaluations WHERE evaluation_id='decision-1'),
		(SELECT count(*) FROM risk_policy_action_jobs WHERE evaluation_id='decision-1')`).
		Scan(&evaluations, &actions); err != nil {
		t.Fatal(err)
	}
	if evaluations != 1 || actions != 1 {
		t.Fatalf("evaluations=%d actions=%d, want one of each", evaluations, actions)
	}
}

func compileRuntimeTestSnapshotWithAction(t *testing.T, version int, decision string) *CompiledPolicySnapshot {
	t.Helper()
	factors := []FactorDefinition{{
		Key: "amount_minor", Type: FactorTypeValue,
		ValueType: FactorValueTypeInt, ParamKey: "amount_minor",
	}}
	snapshot, err := CompilePolicySnapshot(ExecutablePolicyDefinition{
		PolicyKey: "withdrawal_risk",
		Version:   version,
		DefaultDecision: DecisionTemplate{
			Decision: "pass", ReasonCodes: []string{"default_pass"},
		},
		Strategies: []ExecutableStrategyDefinition{{
			StrategyKey: "amount_positive",
			Priority:    100,
			Rule: CELRule{
				RuleKey: "amount_positive", Version: version,
				Expression: "amount_minor > 0",
			},
			Effect: StrategyEffect{
				Decision: DecisionTemplate{Decision: decision, ReasonCodes: []string{"amount_positive"}},
				Actions:  []PolicyAction{{ActionCode: "activate_restriction", Params: map[string]any{"capability": "withdrawal"}}},
			},
		}},
	}, factors, NewRPCFactorAdapterRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
