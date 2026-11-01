package risk

import (
	"context"
	"testing"
)

func TestFactorEngineEvaluatesFactorsInDependencyOrder(t *testing.T) {
	definitions := []FactorDefinition{
		{Key: "inflow_minor_24h", Type: FactorTypeValue, ValueType: FactorValueTypeInt, ParamKey: "inflow_minor_24h"},
		{Key: "outflow_minor_24h", Type: FactorTypeValue, ValueType: FactorValueTypeInt, ParamKey: "outflow_minor_24h"},
		{Key: "outflow_ratio_24h", Type: FactorTypeExpression, ValueType: FactorValueTypeDouble, Expression: "double(outflow_minor_24h) / double(inflow_minor_24h)"},
	}
	valueProvider := NewValueFactorProvider()
	expressionProvider, err := NewExpressionFactorProvider(definitions)
	if err != nil {
		t.Fatal(err)
	}
	inflow, err := valueProvider.NewFactor(definitions[0])
	if err != nil {
		t.Fatal(err)
	}
	outflow, err := valueProvider.NewFactor(definitions[1])
	if err != nil {
		t.Fatal(err)
	}
	ratio, err := expressionProvider.NewFactor(definitions[2])
	if err != nil {
		t.Fatal(err)
	}

	// Deliberately pass the expression before its dependencies. Construction
	// must produce a safe execution plan rather than trusting caller order.
	engine, err := NewFactorEngine([]Factor{ratio, outflow, inflow})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := engine.Evaluate(context.Background(), map[string]any{
		"inflow_minor_24h":  int64(100_000),
		"outflow_minor_24h": int64(95_000),
	})
	if err != nil {
		t.Fatal(err)
	}

	result, found := snapshot.Result("outflow_ratio_24h")
	if !found || result.Status != FactorStatusSuccess || result.Value != 0.95 {
		t.Fatalf("unexpected ratio result: found=%t result=%+v", found, result)
	}
	if got := snapshot.Values()["outflow_ratio_24h"]; got != 0.95 {
		t.Fatalf("materialized ratio = %v, want 0.95", got)
	}
}

func TestFactorEngineRejectsMissingDependencyAndCycleAtCreation(t *testing.T) {
	_, err := NewFactorEngine([]Factor{
		&factorEngineTestFactor{key: "ratio", dependencies: []string{"missing"}},
	})
	if err == nil {
		t.Fatal("undefined dependency must be rejected while creating the engine")
	}

	_, err = NewFactorEngine([]Factor{
		&factorEngineTestFactor{key: "a", dependencies: []string{"b"}},
		&factorEngineTestFactor{key: "b", dependencies: []string{"a"}},
	})
	if err == nil {
		t.Fatal("dependency cycle must be rejected while creating the engine")
	}
}

func TestFactorEngineCreatesIsolatedRuntimeForEveryEvaluation(t *testing.T) {
	factor, err := NewValueFactorProvider().NewFactor(FactorDefinition{
		Key:       "amount_minor",
		Type:      FactorTypeValue,
		ValueType: FactorValueTypeInt,
		ParamKey:  "amount_minor",
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewFactorEngine([]Factor{factor})
	if err != nil {
		t.Fatal(err)
	}

	first, err := engine.Evaluate(context.Background(), map[string]any{"amount_minor": int64(100)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Evaluate(context.Background(), map[string]any{"amount_minor": int64(200)})
	if err != nil {
		t.Fatal(err)
	}
	if first.Values()["amount_minor"] != int64(100) || second.Values()["amount_minor"] != int64(200) {
		t.Fatalf("runtime state leaked: first=%v second=%v", first.Values(), second.Values())
	}

	copyOfResults := first.Results()
	delete(copyOfResults, "amount_minor")
	if _, found := first.Result("amount_minor"); !found {
		t.Fatal("mutating a returned map must not mutate the snapshot")
	}
}

func TestFactorEngineSnapshotFeedsCompiledCELRule(t *testing.T) {
	definitions := []FactorDefinition{
		{Key: "inflow_minor_24h", Type: FactorTypeValue, ValueType: FactorValueTypeInt, ParamKey: "inflow_minor_24h"},
		{Key: "outflow_minor_24h", Type: FactorTypeValue, ValueType: FactorValueTypeInt, ParamKey: "outflow_minor_24h"},
		{Key: "outflow_ratio_24h", Type: FactorTypeExpression, ValueType: FactorValueTypeDouble, Expression: "double(outflow_minor_24h) / double(inflow_minor_24h)"},
	}
	valueProvider := NewValueFactorProvider()
	expressionProvider, err := NewExpressionFactorProvider(definitions)
	if err != nil {
		t.Fatal(err)
	}
	factors := make([]Factor, 0, len(definitions))
	for _, definition := range definitions[:2] {
		factor, createErr := valueProvider.NewFactor(definition)
		if createErr != nil {
			t.Fatal(createErr)
		}
		factors = append(factors, factor)
	}
	ratio, err := expressionProvider.NewFactor(definitions[2])
	if err != nil {
		t.Fatal(err)
	}
	factors = append(factors, ratio)
	engine, err := NewFactorEngine(factors)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := CompileCELRule(CELRule{
		RuleKey:    "rapid_outflow",
		Version:    1,
		Expression: "inflow_minor_24h >= 50000 && outflow_ratio_24h >= 0.90",
	}, definitions)
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := engine.Evaluate(context.Background(), map[string]any{
		"inflow_minor_24h":  int64(100_000),
		"outflow_minor_24h": int64(95_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	matched, err := rule.Evaluate(snapshot.Values())
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("factor snapshot must match the rapid outflow rule")
	}
}

type factorEngineTestFactor struct {
	key          string
	dependencies []string
}

func (f *factorEngineTestFactor) Key() string { return f.key }
func (f *factorEngineTestFactor) DependentFactorKeys() []string {
	return append([]string(nil), f.dependencies...)
}
func (f *factorEngineTestFactor) Fetch(context.Context, FactorRuntime) FactorResult {
	return FactorResult{Key: f.key, Status: FactorStatusSuccess}
}
