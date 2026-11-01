package riskwarehouse_test

import (
	"context"
	"testing"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/risk"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/riskwarehouse"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func TestWarehouseFeatureFactorProviderConvertsSnapshotToTypedFactor(t *testing.T) {
	asOf := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	reader := &snapshotReaderStub{snapshot: warehouse.SubjectFundActivitySnapshot{
		FeatureSetKey: "subject_fund_activity_24h", FeatureSetVersion: 1,
		SubjectType: "organization", SubjectID: "org_warehouse_coach", Currency: "USD",
		AsOfMillis: asOf.UnixMilli(), InflowMinor: 100000, OutflowMinor: 95000,
		OutflowRatio: 0.95, OutflowRatioDefined: true,
	}}
	adapters := riskwarehouse.NewFeatureAdapterRegistry()
	if err := adapters.Register(riskwarehouse.NewFundActivityOutflowRatioAdapter(1, time.Hour)); err != nil {
		t.Fatal(err)
	}
	provider := riskwarehouse.NewWarehouseFeatureFactorProvider(reader, adapters)
	factor, err := provider.NewFactor(risk.FactorDefinition{
		Key: "outflow_ratio_24h", Type: risk.FactorTypeWarehouseFeature,
		ValueType:   risk.FactorValueTypeDouble,
		ResolverKey: "subject_fund_activity_24h/outflow_ratio",
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := risk.NewFactorEngine([]risk.Factor{factor})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := engine.Evaluate(context.Background(), map[string]any{
		"subject_type": "organization", "subject_id": "org_warehouse_coach",
		"currency": "USD", "as_of": asOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, found := snapshot.Result("outflow_ratio_24h")
	if !found || result.Status != risk.FactorStatusSuccess || result.Value != 0.95 {
		t.Fatalf("unexpected factor result: %+v", result)
	}
	if reader.calls != 1 || reader.query.FeatureSetVersion != 1 || !reader.query.AsOf.Equal(asOf) {
		t.Fatalf("unexpected reader request: calls=%d query=%+v", reader.calls, reader.query)
	}
}

func TestWarehouseFeaturesDriveCELPolicyDecision(t *testing.T) {
	asOf := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	reader := &snapshotReaderStub{snapshot: warehouse.SubjectFundActivitySnapshot{
		FeatureSetKey: "subject_fund_activity_24h", FeatureSetVersion: 1,
		SubjectType: "organization", SubjectID: "org_warehouse_coach", Currency: "USD",
		AsOfMillis: asOf.UnixMilli(), InflowMinor: 100000, OutflowMinor: 95000,
		OutflowRatio: 0.95, OutflowRatioDefined: true,
	}}
	adapters := riskwarehouse.NewFeatureAdapterRegistry()
	for _, adapter := range []riskwarehouse.FeatureAdapter{
		riskwarehouse.NewFundActivityInflowMinorAdapter(1, time.Hour),
		riskwarehouse.NewFundActivityOutflowRatioAdapter(1, time.Hour),
	} {
		if err := adapters.Register(adapter); err != nil {
			t.Fatal(err)
		}
	}
	provider := riskwarehouse.NewWarehouseFeatureFactorProvider(reader, adapters)
	factors := []risk.FactorDefinition{
		{Key: "inflow_minor_24h", Type: risk.FactorTypeWarehouseFeature, ValueType: risk.FactorValueTypeInt, ResolverKey: "subject_fund_activity_24h/inflow_minor"},
		{Key: "outflow_ratio_24h", Type: risk.FactorTypeWarehouseFeature, ValueType: risk.FactorValueTypeDouble, ResolverKey: "subject_fund_activity_24h/outflow_ratio"},
	}
	policy, err := risk.CompilePolicySnapshotWithProviders(
		risk.ExecutablePolicyDefinition{
			PolicyKey: "offline_rapid_outflow", Version: 1,
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
	evaluation, err := policy.Evaluate(context.Background(), map[string]any{
		"subject_type": "organization", "subject_id": "org_warehouse_coach",
		"currency": "USD", "as_of": asOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Decision != "block" || evaluation.MatchedRuleKey != "rapid_outflow" || len(evaluation.Actions) != 1 || evaluation.Actions[0].ActionCode != "activate_restriction" {
		t.Fatalf("unexpected policy evaluation: %+v", evaluation)
	}
}

func TestWarehouseFeatureFactorRejectsStaleSnapshot(t *testing.T) {
	asOf := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	reader := &snapshotReaderStub{snapshot: warehouse.SubjectFundActivitySnapshot{
		FeatureSetKey: "subject_fund_activity_24h", FeatureSetVersion: 1,
		SubjectType: "organization", SubjectID: "org_001", Currency: "USD",
		AsOfMillis: asOf.Add(-2 * time.Hour).UnixMilli(), OutflowRatio: 0.95, OutflowRatioDefined: true,
	}}
	result := evaluateRatioFactor(t, reader, asOf, time.Hour)
	if result.Status != risk.FactorStatusError || result.ErrorCode != "stale_feature" {
		t.Fatalf("unexpected stale feature result: %+v", result)
	}
}

func TestWarehouseFeatureFactorDoesNotConvertUndefinedRatioToZero(t *testing.T) {
	asOf := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	reader := &snapshotReaderStub{snapshot: warehouse.SubjectFundActivitySnapshot{
		FeatureSetKey: "subject_fund_activity_24h", FeatureSetVersion: 1,
		SubjectType: "organization", SubjectID: "org_001", Currency: "USD",
		AsOfMillis: asOf.UnixMilli(), OutflowRatio: 0, OutflowRatioDefined: false,
	}}
	result := evaluateRatioFactor(t, reader, asOf, time.Hour)
	if result.Status != risk.FactorStatusMissing || result.ErrorCode != "feature_undefined" {
		t.Fatalf("unexpected undefined feature result: %+v", result)
	}
}

func evaluateRatioFactor(
	t *testing.T,
	reader riskwarehouse.FundActivitySnapshotReader,
	asOf time.Time,
	maxAge time.Duration,
) risk.FactorResult {
	t.Helper()
	adapters := riskwarehouse.NewFeatureAdapterRegistry()
	if err := adapters.Register(riskwarehouse.NewFundActivityOutflowRatioAdapter(1, maxAge)); err != nil {
		t.Fatal(err)
	}
	factor, err := riskwarehouse.NewWarehouseFeatureFactorProvider(reader, adapters).NewFactor(risk.FactorDefinition{
		Key: "outflow_ratio_24h", Type: risk.FactorTypeWarehouseFeature,
		ValueType: risk.FactorValueTypeDouble, ResolverKey: "subject_fund_activity_24h/outflow_ratio",
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := risk.NewFactorEngine([]risk.Factor{factor})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := engine.Evaluate(context.Background(), map[string]any{
		"subject_type": "organization", "subject_id": "org_001", "currency": "USD", "as_of": asOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := snapshot.Result("outflow_ratio_24h")
	return result
}

type snapshotReaderStub struct {
	snapshot warehouse.SubjectFundActivitySnapshot
	found    bool
	err      error
	calls    int
	query    riskwarehouse.FeatureSnapshotQuery
}

func (s *snapshotReaderStub) ReadFundActivitySnapshot(
	_ context.Context,
	query riskwarehouse.FeatureSnapshotQuery,
) (warehouse.SubjectFundActivitySnapshot, bool, error) {
	s.calls++
	s.query = query
	if s.err != nil {
		return warehouse.SubjectFundActivitySnapshot{}, false, s.err
	}
	if !s.found && s.snapshot.FeatureSetKey == "" {
		return warehouse.SubjectFundActivitySnapshot{}, false, nil
	}
	return s.snapshot, true, nil
}
