package risk

import "testing"

func TestEvaluateDecisionPolicyMatchesAllNumericConditions(t *testing.T) {
	minInflow := 50_000.0
	minRatio := 0.90
	policy := DecisionPolicy{
		PolicyKey: "rapid_fund_movement",
		Version:   1,
		When: ConditionNode{All: []ConditionNode{
			{Condition: &FactorCondition{FactorKey: "inflow_minor_24h", Operator: "gte", NumberValue: &minInflow}},
			{Condition: &FactorCondition{FactorKey: "outflow_ratio_24h", Operator: "gte", NumberValue: &minRatio}},
		}},
		Matched:   DecisionTemplate{Decision: "restrict", ReasonCodes: []string{"rapid_fund_movement_detected"}},
		Unmatched: DecisionTemplate{Decision: "clear", ReasonCodes: []string{"policy_not_matched"}},
	}

	decision, err := EvaluateDecisionPolicy(policy, FactorValues{
		"inflow_minor_24h":  NumberFactorValue(100_000),
		"outflow_ratio_24h": NumberFactorValue(0.95),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != "restrict" || len(decision.ReasonCodes) != 1 || decision.ReasonCodes[0] != "rapid_fund_movement_detected" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	t.Logf("factors: inflow=100000 ratio=0.95 -> decision=%s reasons=%v", decision.Decision, decision.ReasonCodes)
}

func TestEvaluateDecisionPolicySupportsAnyAndNot(t *testing.T) {
	largeAmount := 10_000.0
	highVelocity := 5.0
	trustedScore := 0.80
	policy := DecisionPolicy{
		PolicyKey: "suspicious_withdrawal",
		Version:   1,
		When: ConditionNode{All: []ConditionNode{
			{Any: []ConditionNode{
				{Condition: &FactorCondition{FactorKey: "amount_minor", Operator: "gte", NumberValue: &largeAmount}},
				{Condition: &FactorCondition{FactorKey: "withdrawal_count_10m", Operator: "gte", NumberValue: &highVelocity}},
			}},
			{Not: &ConditionNode{Condition: &FactorCondition{FactorKey: "trusted_score", Operator: "gte", NumberValue: &trustedScore}}},
		}},
		Matched:   DecisionTemplate{Decision: "review", ReasonCodes: []string{"suspicious_withdrawal"}},
		Unmatched: DecisionTemplate{Decision: "allow", ReasonCodes: []string{"policy_not_matched"}},
	}

	decision, err := EvaluateDecisionPolicy(policy, FactorValues{
		"amount_minor":         NumberFactorValue(2_500),
		"withdrawal_count_10m": NumberFactorValue(7),
		"trusted_score":        NumberFactorValue(0.20),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != "review" {
		t.Fatalf("decision=%q, want review", decision.Decision)
	}
}

func TestEvaluateDecisionPolicySupportsNumericOperators(t *testing.T) {
	tests := []struct {
		name     string
		operator string
		actual   float64
		expected float64
	}{
		{name: "equal", operator: "eq", actual: 10, expected: 10},
		{name: "not equal", operator: "ne", actual: 10, expected: 11},
		{name: "greater than", operator: "gt", actual: 11, expected: 10},
		{name: "greater than or equal", operator: "gte", actual: 10, expected: 10},
		{name: "less than", operator: "lt", actual: 9, expected: 10},
		{name: "less than or equal", operator: "lte", actual: 10, expected: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := DecisionPolicy{
				PolicyKey: "numeric_operator_contract",
				Version:   1,
				When: ConditionNode{Condition: &FactorCondition{
					FactorKey: "value", Operator: tt.operator, NumberValue: &tt.expected,
				}},
				Matched:   DecisionTemplate{Decision: "matched"},
				Unmatched: DecisionTemplate{Decision: "unmatched"},
			}
			decision, err := EvaluateDecisionPolicy(policy, FactorValues{"value": NumberFactorValue(tt.actual)})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Decision != "matched" {
				t.Fatalf("decision=%q, want matched", decision.Decision)
			}
		})
	}
}

func TestEvaluateDecisionPolicySupportsTypedFactors(t *testing.T) {
	allowedCountries := []string{"SG", "HK"}
	policy := DecisionPolicy{
		PolicyKey: "withdrawal_context",
		Version:   1,
		When: ConditionNode{All: []ConditionNode{
			{Condition: &FactorCondition{FactorKey: "country", Operator: "in", StringValues: allowedCountries}},
			{Condition: &FactorCondition{FactorKey: "account_active", Operator: "eq", BoolValue: boolPointer(true)}},
			{Condition: &FactorCondition{FactorKey: "risk_tags", Operator: "contains", StringValue: stringPointer("trusted")}},
		}},
		Matched:   DecisionTemplate{Decision: "allow", ReasonCodes: []string{"trusted_withdrawal_context"}},
		Unmatched: DecisionTemplate{Decision: "review", ReasonCodes: []string{"context_requires_review"}},
	}

	decision, err := EvaluateDecisionPolicy(policy, FactorValues{
		"country":        StringFactorValue("SG"),
		"account_active": BoolFactorValue(true),
		"risk_tags":      StringListFactorValue([]string{"known_device", "trusted"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != "allow" {
		t.Fatalf("decision=%q, want allow", decision.Decision)
	}
	t.Logf("typed factors: country=SG active=true tags=[known_device trusted] -> decision=%s", decision.Decision)
}

func TestEvaluateDecisionPolicyRejectsAmbiguousConditionNode(t *testing.T) {
	threshold := 10.0
	condition := ConditionNode{Condition: &FactorCondition{
		FactorKey: "amount", Operator: "gte", NumberValue: &threshold,
	}}
	policy := DecisionPolicy{
		PolicyKey: "invalid_policy",
		Version:   1,
		When: ConditionNode{
			Condition: condition.Condition,
			All:       []ConditionNode{condition},
		},
		Matched:   DecisionTemplate{Decision: "restrict"},
		Unmatched: DecisionTemplate{Decision: "allow"},
	}

	_, err := EvaluateDecisionPolicy(policy, FactorValues{"amount": NumberFactorValue(20)})
	if err == nil {
		t.Fatal("ambiguous condition node must be rejected")
	}
}

func TestEvaluateDecisionPolicySupportsFactorExistence(t *testing.T) {
	policy := DecisionPolicy{
		PolicyKey: "missing_device_signal",
		Version:   1,
		When: ConditionNode{Condition: &FactorCondition{
			FactorKey: "device_trust_score", Operator: "not_exists",
		}},
		Matched:   DecisionTemplate{Decision: "review", ReasonCodes: []string{"device_signal_missing"}},
		Unmatched: DecisionTemplate{Decision: "allow", ReasonCodes: []string{"device_signal_present"}},
	}

	decision, err := EvaluateDecisionPolicy(policy, FactorValues{})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != "review" {
		t.Fatalf("decision=%q, want review", decision.Decision)
	}
	t.Logf("device_trust_score is absent -> decision=%s reasons=%v", decision.Decision, decision.ReasonCodes)
}

func boolPointer(value bool) *bool       { return &value }
func stringPointer(value string) *string { return &value }
