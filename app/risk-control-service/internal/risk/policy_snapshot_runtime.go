package risk

import (
	"context"
	"fmt"
	"sync/atomic"
)

// PolicySnapshotRuntime is the execution-plane handle for one policy. Every
// evaluation captures exactly one immutable snapshot before it starts.
type PolicySnapshotRuntime struct {
	current atomic.Pointer[CompiledPolicySnapshot]
}

type PublishedPolicySnapshotLoader interface {
	LoadPublished(context.Context, string, int) (*CompiledPolicySnapshot, error)
}

func LoadPolicySnapshotRuntime(
	ctx context.Context,
	loader PublishedPolicySnapshotLoader,
	policyKey string,
	version int,
) (*PolicySnapshotRuntime, error) {
	if loader == nil {
		return nil, fmt.Errorf("published policy snapshot loader is required")
	}
	snapshot, err := loader.LoadPublished(ctx, policyKey, version)
	if err != nil {
		return nil, fmt.Errorf("load initial published policy snapshot: %w", err)
	}
	return NewPolicySnapshotRuntime(snapshot)
}

func NewPolicySnapshotRuntime(initial *CompiledPolicySnapshot) (*PolicySnapshotRuntime, error) {
	if initial == nil {
		return nil, fmt.Errorf("initial policy snapshot is required")
	}
	runtime := &PolicySnapshotRuntime{}
	runtime.current.Store(initial)
	return runtime, nil
}

func (r *PolicySnapshotRuntime) Evaluate(
	ctx context.Context,
	params map[string]any,
) (PolicyEvaluation, error) {
	snapshot := r.current.Load()
	if snapshot == nil {
		return PolicyEvaluation{}, fmt.Errorf("active policy snapshot is unavailable")
	}
	return snapshot.Evaluate(ctx, params)
}

func (r *PolicySnapshotRuntime) Activate(snapshot *CompiledPolicySnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("policy snapshot is required")
	}
	for {
		current := r.current.Load()
		if current == nil {
			return fmt.Errorf("active policy snapshot is unavailable")
		}
		if snapshot.policyKey != current.policyKey {
			return fmt.Errorf("cannot activate policy %q in runtime for %q", snapshot.policyKey, current.policyKey)
		}
		if snapshot.version <= current.version {
			return fmt.Errorf(
				"policy %q version must increase: current=%d candidate=%d",
				current.policyKey, current.version, snapshot.version,
			)
		}
		if r.current.CompareAndSwap(current, snapshot) {
			return nil
		}
	}
}

func (r *PolicySnapshotRuntime) Reload(
	ctx context.Context,
	loader PublishedPolicySnapshotLoader,
	policyKey string,
	version int,
) error {
	if loader == nil {
		return fmt.Errorf("published policy snapshot loader is required")
	}
	snapshot, err := loader.LoadPublished(ctx, policyKey, version)
	if err != nil {
		return fmt.Errorf("load published policy snapshot: %w", err)
	}
	return r.Activate(snapshot)
}
