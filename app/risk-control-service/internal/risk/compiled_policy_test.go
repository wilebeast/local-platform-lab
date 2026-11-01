package risk

import (
	"context"
	"testing"
)

func TestCompiledPolicySnapshotEvaluatesRulesByPriorityAndReturnsTrace(t *testing.T) {
	factors, definition := withdrawalRiskPolicyFixture()
	policy, err := CompilePolicySnapshot(definition, factors, NewRPCFactorAdapterRegistry())
	if err != nil {
		t.Fatal(err)
	}

	evaluation, err := policy.Evaluate(context.Background(), map[string]any{
		"customer_status":   "blocked",
		"inflow_minor_24h":  int64(100_000),
		"outflow_minor_24h": int64(95_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Decision != "block" || evaluation.MatchedStrategyKey != "block_customer" || evaluation.MatchedRuleKey != "blocked_customer" {
		t.Fatalf("unexpected policy evaluation: %+v", evaluation)
	}
	if len(evaluation.StrategyResults) != 1 || !evaluation.StrategyResults[0].Matched {
		t.Fatalf("first-match policy must stop after the highest-priority match: %+v", evaluation.StrategyResults)
	}
	if ratio := evaluation.Factors.Values()["outflow_ratio_24h"]; ratio != 0.95 {
		t.Fatalf("factor trace ratio = %v, want 0.95", ratio)
	}
}

func TestCompiledPolicySnapshotUsesLowerPriorityRuleAndDefaultDecision(t *testing.T) {
	factors, definition := withdrawalRiskPolicyFixture()
	policy, err := CompilePolicySnapshot(definition, factors, NewRPCFactorAdapterRegistry())
	if err != nil {
		t.Fatal(err)
	}

	restricted, err := policy.Evaluate(context.Background(), map[string]any{
		"customer_status":   "active",
		"inflow_minor_24h":  int64(100_000),
		"outflow_minor_24h": int64(95_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if restricted.Decision != "block" || restricted.MatchedStrategyKey != "block_rapid_outflow" || restricted.MatchedRuleKey != "rapid_outflow" || len(restricted.StrategyResults) != 2 {
		t.Fatalf("unexpected lower-priority match: %+v", restricted)
	}
	if len(restricted.Actions) != 1 || restricted.Actions[0].ActionCode != "activate_restriction" || restricted.Actions[0].Params["capability"] != "withdrawal" {
		t.Fatalf("unexpected policy actions: %+v", restricted.Actions)
	}

	allowed, err := policy.Evaluate(context.Background(), map[string]any{
		"customer_status":   "active",
		"inflow_minor_24h":  int64(100_000),
		"outflow_minor_24h": int64(50_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if allowed.Decision != "pass" || allowed.MatchedStrategyKey != "" || allowed.MatchedRuleKey != "" || len(allowed.StrategyResults) != 2 || len(allowed.Actions) != 0 {
		t.Fatalf("unexpected default decision: %+v", allowed)
	}
}

func TestCompiledPolicySnapshotRejectsUnsupportedDecision(t *testing.T) {
	factors, definition := withdrawalRiskPolicyFixture()
	definition.Strategies[0].Effect.Decision.Decision = "restrict"

	_, err := CompilePolicySnapshot(definition, factors, NewRPCFactorAdapterRegistry())
	if err == nil {
		t.Fatal("strategy decision outside pass/block must be rejected before publication")
	}
}

func withdrawalRiskPolicyFixture() ([]FactorDefinition, ExecutablePolicyDefinition) {
	factors := []FactorDefinition{
		{Key: "customer_status", Type: FactorTypeValue, ValueType: FactorValueTypeString, ParamKey: "customer_status"},
		{Key: "inflow_minor_24h", Type: FactorTypeValue, ValueType: FactorValueTypeInt, ParamKey: "inflow_minor_24h"},
		{Key: "outflow_minor_24h", Type: FactorTypeValue, ValueType: FactorValueTypeInt, ParamKey: "outflow_minor_24h"},
		{Key: "outflow_ratio_24h", Type: FactorTypeExpression, ValueType: FactorValueTypeDouble, Expression: "double(outflow_minor_24h) / double(inflow_minor_24h)"},
	}
	return factors, ExecutablePolicyDefinition{
		PolicyKey:       "withdrawal_risk",
		Version:         1,
		DefaultDecision: DecisionTemplate{Decision: "pass", ReasonCodes: []string{"policy_not_matched"}},
		Strategies: []ExecutableStrategyDefinition{
			{
				StrategyKey: "block_rapid_outflow",
				Rule:        CELRule{RuleKey: "rapid_outflow", Version: 1, Expression: "inflow_minor_24h >= 50000 && outflow_ratio_24h >= 0.90"},
				Priority:    80,
				Effect: StrategyEffect{
					Decision: DecisionTemplate{Decision: "block", ReasonCodes: []string{"rapid_outflow_detected"}},
					Actions:  []PolicyAction{{ActionCode: "activate_restriction", Params: map[string]any{"capability": "withdrawal"}}},
				},
			},
			{
				StrategyKey: "block_customer",
				Rule:        CELRule{RuleKey: "blocked_customer", Version: 1, Expression: `customer_status == "blocked"`},
				Priority:    100,
				Effect:      StrategyEffect{Decision: DecisionTemplate{Decision: "block", ReasonCodes: []string{"customer_blocked"}}},
			},
		},
	}
}
