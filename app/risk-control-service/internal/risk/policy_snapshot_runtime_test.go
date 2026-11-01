package risk

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPolicySnapshotRuntimeAtomicallyActivatesNewVersion(t *testing.T) {
	v1 := compileRuntimeTestSnapshot(t, 1, "pass")
	v2 := compileRuntimeTestSnapshot(t, 2, "block")
	runtime, err := NewPolicySnapshotRuntime(v1)
	if err != nil {
		t.Fatal(err)
	}

	before, err := runtime.Evaluate(context.Background(), map[string]any{"amount_minor": int64(100)})
	if err != nil {
		t.Fatal(err)
	}
	if before.PolicyVersion != 1 || before.Decision != "pass" {
		t.Fatalf("before activation: %+v", before)
	}

	if err := runtime.Activate(v2); err != nil {
		t.Fatal(err)
	}
	after, err := runtime.Evaluate(context.Background(), map[string]any{"amount_minor": int64(100)})
	if err != nil {
		t.Fatal(err)
	}
	if after.PolicyVersion != 2 || after.Decision != "block" {
		t.Fatalf("after activation: %+v", after)
	}
}

func TestPolicySnapshotRuntimeKeepsCurrentVersionWhenReloadFails(t *testing.T) {
	runtime, err := NewPolicySnapshotRuntime(compileRuntimeTestSnapshot(t, 1, "pass"))
	if err != nil {
		t.Fatal(err)
	}
	loader := policySnapshotLoaderFunc(func(context.Context, string, int) (*CompiledPolicySnapshot, error) {
		return nil, errors.New("published policy cannot be compiled")
	})

	if err := runtime.Reload(context.Background(), loader, "withdrawal_risk", 2); err == nil {
		t.Fatal("expected reload failure")
	}
	evaluation, err := runtime.Evaluate(context.Background(), map[string]any{"amount_minor": int64(100)})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.PolicyVersion != 1 || evaluation.Decision != "pass" {
		t.Fatalf("failed reload replaced the current snapshot: %+v", evaluation)
	}
}

func TestPolicySnapshotRuntimeRejectsDifferentPolicyAndVersionRollback(t *testing.T) {
	runtime, err := NewPolicySnapshotRuntime(compileRuntimeTestSnapshot(t, 2, "block"))
	if err != nil {
		t.Fatal(err)
	}

	if err := runtime.Activate(compileRuntimeTestSnapshot(t, 1, "pass")); err == nil {
		t.Fatal("expected version rollback to be rejected")
	}
	other := compileNamedRuntimeTestSnapshot(t, "payment_risk", 3, "pass")
	if err := runtime.Activate(other); err == nil {
		t.Fatal("expected a snapshot for another policy to be rejected")
	}

	evaluation, err := runtime.Evaluate(context.Background(), map[string]any{"amount_minor": int64(100)})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.PolicyKey != "withdrawal_risk" || evaluation.PolicyVersion != 2 || evaluation.Decision != "block" {
		t.Fatalf("invalid activation changed current snapshot: %+v", evaluation)
	}
}

func TestPolicySnapshotRuntimeLetsInFlightEvaluationFinishOnCapturedVersion(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	adapter := &blockingRuntimeTestAdapter{started: started, release: release}
	registry := NewRPCFactorAdapterRegistry()
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	v1 := compileBlockingRuntimeTestSnapshot(t, registry)
	v2 := compileRuntimeTestSnapshot(t, 2, "pass")
	runtime, err := NewPolicySnapshotRuntime(v1)
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan PolicyEvaluation, 1)
	errs := make(chan error, 1)
	go func() {
		evaluation, evaluateErr := runtime.Evaluate(context.Background(), nil)
		if evaluateErr != nil {
			errs <- evaluateErr
			return
		}
		result <- evaluation
	}()
	<-started
	if err := runtime.Activate(v2); err != nil {
		t.Fatal(err)
	}
	close(release)

	select {
	case err := <-errs:
		t.Fatal(err)
	case evaluation := <-result:
		if evaluation.PolicyVersion != 1 || evaluation.Decision != "block" {
			t.Fatalf("in-flight evaluation mixed snapshot versions: %+v", evaluation)
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight evaluation did not finish")
	}

	next, err := runtime.Evaluate(context.Background(), map[string]any{"amount_minor": int64(100)})
	if err != nil {
		t.Fatal(err)
	}
	if next.PolicyVersion != 2 || next.Decision != "pass" {
		t.Fatalf("new evaluation did not use activated version: %+v", next)
	}
}

func TestPolicySnapshotRuntimeConcurrentActivationsKeepHighestVersion(t *testing.T) {
	runtime, err := NewPolicySnapshotRuntime(compileRuntimeTestSnapshot(t, 1, "pass"))
	if err != nil {
		t.Fatal(err)
	}
	v2 := compileRuntimeTestSnapshot(t, 2, "pass")
	v3 := compileRuntimeTestSnapshot(t, 3, "block")
	start := make(chan struct{})
	done := make(chan error, 2)
	for _, snapshot := range []*CompiledPolicySnapshot{v2, v3} {
		go func(candidate *CompiledPolicySnapshot) {
			<-start
			done <- runtime.Activate(candidate)
		}(snapshot)
	}
	close(start)
	for range 2 {
		<-done // v2 may lose the race to v3 and be correctly rejected.
	}

	evaluation, err := runtime.Evaluate(context.Background(), map[string]any{"amount_minor": int64(100)})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.PolicyVersion != 3 || evaluation.Decision != "block" {
		t.Fatalf("concurrent activation downgraded the runtime: %+v", evaluation)
	}
}

func TestLoadPolicySnapshotRuntimeLoadsPublishedVersionBeforeServing(t *testing.T) {
	expected := compileRuntimeTestSnapshot(t, 4, "block")
	loader := policySnapshotLoaderFunc(func(_ context.Context, policyKey string, version int) (*CompiledPolicySnapshot, error) {
		if policyKey != "withdrawal_risk" || version != 4 {
			t.Fatalf("unexpected load: policy=%s version=%d", policyKey, version)
		}
		return expected, nil
	})

	runtime, err := LoadPolicySnapshotRuntime(context.Background(), loader, "withdrawal_risk", 4)
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := runtime.Evaluate(context.Background(), map[string]any{"amount_minor": int64(100)})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.PolicyVersion != 4 || evaluation.Decision != "block" {
		t.Fatalf("unexpected initial runtime evaluation: %+v", evaluation)
	}
}

type policySnapshotLoaderFunc func(context.Context, string, int) (*CompiledPolicySnapshot, error)

func (f policySnapshotLoaderFunc) LoadPublished(
	ctx context.Context,
	policyKey string,
	version int,
) (*CompiledPolicySnapshot, error) {
	return f(ctx, policyKey, version)
}

type blockingRuntimeTestAdapter struct {
	started chan struct{}
	release chan struct{}
}

func (*blockingRuntimeTestAdapter) Key() string                   { return "blocking_signal" }
func (*blockingRuntimeTestAdapter) DependentFactorKeys() []string { return nil }
func (a *blockingRuntimeTestAdapter) Fetch(ctx context.Context, _ FactorRuntime) (any, error) {
	close(a.started)
	select {
	case <-a.release:
		return true, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func compileBlockingRuntimeTestSnapshot(t *testing.T, registry *RPCFactorAdapterRegistry) *CompiledPolicySnapshot {
	t.Helper()
	factors := []FactorDefinition{{
		Key: "remote_allow", Type: FactorTypeRPC, ValueType: FactorValueTypeBool,
		ResolverKey: "blocking_signal", Timeout: time.Second,
	}}
	snapshot, err := CompilePolicySnapshot(ExecutablePolicyDefinition{
		PolicyKey: "withdrawal_risk",
		Version:   1,
		DefaultDecision: DecisionTemplate{
			Decision: "pass", ReasonCodes: []string{"default_pass"},
		},
		Strategies: []ExecutableStrategyDefinition{{
			StrategyKey: "remote_block",
			Priority:    100,
			Rule: CELRule{
				RuleKey: "remote_block", Version: 1, Expression: "remote_allow",
			},
			Effect: StrategyEffect{Decision: DecisionTemplate{
				Decision: "block", ReasonCodes: []string{"remote_block"},
			}},
		}},
	}, factors, registry)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func compileRuntimeTestSnapshot(t *testing.T, version int, decision string) *CompiledPolicySnapshot {
	return compileNamedRuntimeTestSnapshot(t, "withdrawal_risk", version, decision)
}

func compileNamedRuntimeTestSnapshot(t *testing.T, policyKey string, version int, decision string) *CompiledPolicySnapshot {
	t.Helper()
	factors := []FactorDefinition{{
		Key: "amount_minor", Type: FactorTypeValue,
		ValueType: FactorValueTypeInt, ParamKey: "amount_minor",
	}}
	snapshot, err := CompilePolicySnapshot(ExecutablePolicyDefinition{
		PolicyKey: policyKey,
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
			Effect: StrategyEffect{Decision: DecisionTemplate{
				Decision: decision, ReasonCodes: []string{"amount_positive"},
			}},
		}},
	}, factors, NewRPCFactorAdapterRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
