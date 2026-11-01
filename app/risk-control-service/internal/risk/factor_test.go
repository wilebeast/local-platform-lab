package risk

import (
	"context"
	"testing"
	"time"
)

func TestFactorRegistryCreatesValueFactorThatFetchesFromRuntime(t *testing.T) {
	registry := NewFactorRegistry()
	if err := registry.RegisterProvider(NewValueFactorProvider()); err != nil {
		t.Fatal(err)
	}

	factor, err := registry.NewFactor(FactorDefinition{
		Key:       "withdrawal_amount_minor",
		Type:      FactorTypeValue,
		ValueType: FactorValueTypeInt,
		ParamKey:  "amount_minor",
	})
	if err != nil {
		t.Fatal(err)
	}

	result := factor.Fetch(context.Background(), &factorRuntimeStub{
		params: map[string]any{"amount_minor": int64(25_000)},
	})
	if result.Status != FactorStatusSuccess || result.Value != int64(25_000) {
		t.Fatalf("unexpected factor result: %+v", result)
	}
}

func TestRPCFactorProviderBindsRegisteredAdapterAtCreation(t *testing.T) {
	adapters := NewRPCFactorAdapterRegistry()
	adapter := &rpcFactorAdapterStub{
		key:          "customer-service/GetCustomerStatus",
		dependencies: []string{"subject_id"},
		value:        "active",
	}
	if err := adapters.Register(adapter); err != nil {
		t.Fatal(err)
	}

	registry := NewFactorRegistry()
	if err := registry.RegisterProvider(NewRPCFactorProvider(adapters)); err != nil {
		t.Fatal(err)
	}
	factor, err := registry.NewFactor(FactorDefinition{
		Key:         "customer_status",
		Type:        FactorTypeRPC,
		ValueType:   FactorValueTypeString,
		ResolverKey: "customer-service/GetCustomerStatus",
		Timeout:     100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := factor.DependentFactorKeys(); len(got) != 1 || got[0] != "subject_id" {
		t.Fatalf("unexpected dependencies: %v", got)
	}
	result := factor.Fetch(context.Background(), &factorRuntimeStub{})
	if result.Status != FactorStatusSuccess || result.Value != "active" {
		t.Fatalf("unexpected factor result: %+v", result)
	}
	if adapter.calls != 1 {
		t.Fatalf("adapter calls = %d, want 1", adapter.calls)
	}
}

func TestRPCFactorProviderRejectsUnregisteredAdapterDuringCreation(t *testing.T) {
	registry := NewFactorRegistry()
	if err := registry.RegisterProvider(NewRPCFactorProvider(NewRPCFactorAdapterRegistry())); err != nil {
		t.Fatal(err)
	}

	_, err := registry.NewFactor(FactorDefinition{
		Key:         "customer_status",
		Type:        FactorTypeRPC,
		ValueType:   FactorValueTypeString,
		ResolverKey: "customer-service/GetCustomerStatus",
		Timeout:     100 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("unregistered RPC adapter must be rejected while creating the factor")
	}
}

func TestRPCFactorEnforcesConfiguredTimeout(t *testing.T) {
	adapters := NewRPCFactorAdapterRegistry()
	if err := adapters.Register(&rpcFactorAdapterStub{
		key:                 "device-service/GetDeviceRisk",
		waitForCancellation: true,
	}); err != nil {
		t.Fatal(err)
	}
	provider := NewRPCFactorProvider(adapters)
	factor, err := provider.NewFactor(FactorDefinition{
		Key:         "device_risk",
		Type:        FactorTypeRPC,
		ValueType:   FactorValueTypeString,
		ResolverKey: "device-service/GetDeviceRisk",
		Timeout:     time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	result := factor.Fetch(context.Background(), &factorRuntimeStub{})
	if result.Status != FactorStatusError || result.ErrorCode != "rpc_failed" {
		t.Fatalf("unexpected timeout result: %+v", result)
	}
}

func TestExpressionFactorProviderCompilesAtCreationAndUsesRuntimeDependencies(t *testing.T) {
	definitions := []FactorDefinition{
		{Key: "inflow_minor_24h", Type: FactorTypeValue, ValueType: FactorValueTypeInt, ParamKey: "inflow_minor_24h"},
		{Key: "outflow_minor_24h", Type: FactorTypeValue, ValueType: FactorValueTypeInt, ParamKey: "outflow_minor_24h"},
		{Key: "outflow_ratio_24h", Type: FactorTypeExpression, ValueType: FactorValueTypeDouble, Expression: "double(outflow_minor_24h) / double(inflow_minor_24h)"},
	}
	provider, err := NewExpressionFactorProvider(definitions)
	if err != nil {
		t.Fatal(err)
	}
	factor, err := provider.NewFactor(definitions[2])
	if err != nil {
		t.Fatal(err)
	}
	dependencies := factor.DependentFactorKeys()
	if len(dependencies) != 2 || dependencies[0] != "inflow_minor_24h" || dependencies[1] != "outflow_minor_24h" {
		t.Fatalf("unexpected dependencies: %v", dependencies)
	}

	result := factor.Fetch(context.Background(), &factorRuntimeStub{factors: map[string]FactorResult{
		"inflow_minor_24h":  {Key: "inflow_minor_24h", Status: FactorStatusSuccess, Value: int64(100_000)},
		"outflow_minor_24h": {Key: "outflow_minor_24h", Status: FactorStatusSuccess, Value: int64(95_000)},
	}})
	if result.Status != FactorStatusSuccess || result.Value != 0.95 {
		t.Fatalf("unexpected expression factor result: %+v", result)
	}
}

type factorRuntimeStub struct {
	params  map[string]any
	factors map[string]FactorResult
}

func (s *factorRuntimeStub) ParamValue(key string) (any, bool) {
	value, found := s.params[key]
	return value, found
}

func (s *factorRuntimeStub) GetFactor(key string) (FactorResult, bool) {
	value, found := s.factors[key]
	return value, found
}

type rpcFactorAdapterStub struct {
	key                 string
	dependencies        []string
	value               any
	err                 error
	calls               int
	waitForCancellation bool
}

func (s *rpcFactorAdapterStub) Key() string { return s.key }

func (s *rpcFactorAdapterStub) DependentFactorKeys() []string {
	return append([]string(nil), s.dependencies...)
}

func (s *rpcFactorAdapterStub) Fetch(ctx context.Context, _ FactorRuntime) (any, error) {
	s.calls++
	if s.waitForCancellation {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.value, s.err
}
