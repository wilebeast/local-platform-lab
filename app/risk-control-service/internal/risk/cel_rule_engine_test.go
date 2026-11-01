package risk

import (
	"testing"
	"time"
)

func TestCELRuleCompilesFromFactorDefinitionsAndEvaluatesValues(t *testing.T) {
	compiled, err := CompileCELRule(CELRule{
		RuleKey:    "rapid_fund_movement",
		Version:    1,
		Expression: "inflow_minor_24h >= 50000 && outflow_ratio_24h >= 0.90",
	}, []FactorDefinition{
		{Key: "inflow_minor_24h", ValueType: "int"},
		{Key: "outflow_ratio_24h", ValueType: "double"},
	})
	if err != nil {
		t.Fatal(err)
	}

	matched, err := compiled.Evaluate(map[string]any{
		"inflow_minor_24h":  int64(100_000),
		"outflow_ratio_24h": 0.95,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("rapid fund movement rule must match")
	}

	notMatched, err := compiled.Evaluate(map[string]any{
		"inflow_minor_24h":  int64(100_000),
		"outflow_ratio_24h": 0.50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if notMatched {
		t.Fatal("rule must not match when outflow ratio is below threshold")
	}
	t.Logf("same compiled rule: ratio=0.95 -> %t, ratio=0.50 -> %t", matched, notMatched)
}

func TestCELRuleRejectsInvalidExpressionsBeforePublication(t *testing.T) {
	factors := []FactorDefinition{
		{Key: "inflow_minor_24h", ValueType: "int"},
		{Key: "account_status", ValueType: "string"},
	}
	tests := []struct {
		name       string
		expression string
	}{
		{name: "unknown factor", expression: "missing_factor > 0"},
		{name: "factor type mismatch", expression: "account_status >= 100"},
		{name: "non boolean result", expression: "inflow_minor_24h + 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CompileCELRule(CELRule{
				RuleKey: tt.name, Version: 1, Expression: tt.expression,
			}, factors)
			if err == nil {
				t.Fatalf("expression %q must be rejected before publication", tt.expression)
			}
			t.Logf("rejected %q: %v", tt.expression, err)
		})
	}
}

func TestCELProgramRegistryReusesPublishedProgramAndRejectsConflictingVersion(t *testing.T) {
	registry := NewCELProgramRegistry()
	factors := []FactorDefinition{{Key: "risk_score", ValueType: "double"}}
	rule := CELRule{RuleKey: "high_risk", Version: 3, Expression: "risk_score >= 0.80"}

	first, err := registry.Publish(rule, 7, factors)
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Publish(rule, 7, factors)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("exact publication replay must reuse the compiled program")
	}

	_, err = registry.Publish(CELRule{
		RuleKey: "high_risk", Version: 3, Expression: "risk_score >= 0.95",
	}, 7, factors)
	if err == nil {
		t.Fatal("same rule and schema version must not silently replace another expression")
	}
	t.Logf("published program reused=%t; conflicting version rejected=%v", first == second, err != nil)
}

func TestCELProgramRegistryRejectsFactorResolverChangeForPublishedSchema(t *testing.T) {
	registry := NewCELProgramRegistry()
	rule := CELRule{RuleKey: "active_customer", Version: 1, Expression: `customer_status == "active"`}
	first := []FactorDefinition{{
		Key:         "customer_status",
		Type:        FactorTypeRPC,
		ValueType:   FactorValueTypeString,
		ResolverKey: "customer-service/GetCustomerStatus",
		Timeout:     100 * time.Millisecond,
	}}
	if _, err := registry.Publish(rule, 1, first); err != nil {
		t.Fatal(err)
	}

	changed := append([]FactorDefinition(nil), first...)
	changed[0].ResolverKey = "legacy-customer-service/GetCustomerStatus"
	if _, err := registry.Publish(rule, 1, changed); err == nil {
		t.Fatal("published factor schema must reject a resolver change under the same version")
	}
}
