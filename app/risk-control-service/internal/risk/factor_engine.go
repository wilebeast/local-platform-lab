package risk

import (
	"context"
	"fmt"
	"sort"
)

// FactorEngine is an immutable execution plan. Request-specific state is
// created inside Evaluate and is never retained by the engine.
type FactorEngine struct {
	ordered []Factor
}

func NewFactorEngine(factors []Factor) (*FactorEngine, error) {
	factorsByKey := make(map[string]Factor, len(factors))
	for _, factor := range factors {
		if factor == nil || factor.Key() == "" {
			return nil, fmt.Errorf("factor and factor key are required")
		}
		if _, exists := factorsByKey[factor.Key()]; exists {
			return nil, fmt.Errorf("factor %q is declared more than once", factor.Key())
		}
		factorsByKey[factor.Key()] = factor
	}

	keys := make([]string, 0, len(factorsByKey))
	for key := range factorsByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	ordered := make([]Factor, 0, len(factorsByKey))
	visited := make(map[string]bool, len(factorsByKey))
	visiting := make(map[string]bool, len(factorsByKey))
	var visit func(string) error
	visit = func(key string) error {
		if visited[key] {
			return nil
		}
		if visiting[key] {
			return fmt.Errorf("factor dependency cycle at %q", key)
		}
		factor, found := factorsByKey[key]
		if !found {
			return fmt.Errorf("factor dependency %q is not defined", key)
		}
		visiting[key] = true
		dependencies := append([]string(nil), factor.DependentFactorKeys()...)
		sort.Strings(dependencies)
		for _, dependency := range dependencies {
			if _, found := factorsByKey[dependency]; !found {
				return fmt.Errorf("factor %q depends on undefined factor %q", key, dependency)
			}
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[key] = false
		visited[key] = true
		ordered = append(ordered, factor)
		return nil
	}
	for _, key := range keys {
		if err := visit(key); err != nil {
			return nil, err
		}
	}
	return &FactorEngine{ordered: ordered}, nil
}

func (e *FactorEngine) Evaluate(ctx context.Context, params map[string]any) (FactorSnapshot, error) {
	runtime := newFactorRuntimeState(params, e.ordered)
	for _, factor := range e.ordered {
		if err := ctx.Err(); err != nil {
			return runtime.snapshot(), err
		}
		result := factor.Fetch(ctx, runtime)
		if result.Key != factor.Key() {
			return runtime.snapshot(), fmt.Errorf("factor %q returned result for %q", factor.Key(), result.Key)
		}
		if err := runtime.complete(result); err != nil {
			return runtime.snapshot(), err
		}
	}
	return runtime.snapshot(), nil
}

type factorRuntimeState struct {
	params  map[string]any
	results map[string]FactorResult
	known   map[string]struct{}
}

func newFactorRuntimeState(params map[string]any, factors []Factor) *factorRuntimeState {
	paramsCopy := make(map[string]any, len(params))
	for key, value := range params {
		paramsCopy[key] = value
	}
	known := make(map[string]struct{}, len(factors))
	for _, factor := range factors {
		known[factor.Key()] = struct{}{}
	}
	return &factorRuntimeState{
		params:  paramsCopy,
		results: make(map[string]FactorResult, len(factors)),
		known:   known,
	}
}

func (s *factorRuntimeState) ParamValue(key string) (any, bool) {
	value, found := s.params[key]
	return value, found
}

func (s *factorRuntimeState) GetFactor(key string) (FactorResult, bool) {
	result, found := s.results[key]
	return result, found
}

func (s *factorRuntimeState) complete(result FactorResult) error {
	if _, known := s.known[result.Key]; !known {
		return fmt.Errorf("factor %q is not part of this evaluation", result.Key)
	}
	if _, completed := s.results[result.Key]; completed {
		return fmt.Errorf("factor %q is already completed", result.Key)
	}
	s.results[result.Key] = result
	return nil
}

func (s *factorRuntimeState) snapshot() FactorSnapshot {
	results := make(map[string]FactorResult, len(s.results))
	for key, result := range s.results {
		results[key] = result
	}
	return FactorSnapshot{results: results}
}

type FactorSnapshot struct {
	results map[string]FactorResult
}

func (s FactorSnapshot) Result(key string) (FactorResult, bool) {
	result, found := s.results[key]
	return result, found
}

func (s FactorSnapshot) Results() map[string]FactorResult {
	results := make(map[string]FactorResult, len(s.results))
	for key, result := range s.results {
		results[key] = result
	}
	return results
}

func (s FactorSnapshot) Values() map[string]any {
	values := make(map[string]any, len(s.results))
	for key, result := range s.results {
		if result.Status == FactorStatusSuccess {
			values[key] = result.Value
		}
	}
	return values
}
