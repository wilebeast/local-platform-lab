package risk

import (
	"context"
	"fmt"
	"sort"
)

type PolicyAction struct {
	ActionCode string
	Params     map[string]any
}

type StrategyEffect struct {
	Decision DecisionTemplate
	Actions  []PolicyAction
}

type ExecutableStrategyDefinition struct {
	StrategyKey string
	Rule        CELRule
	Priority    int
	Effect      StrategyEffect
}

type ExecutablePolicyDefinition struct {
	PolicyKey       string
	Version         int
	DefaultDecision DecisionTemplate
	Strategies      []ExecutableStrategyDefinition
}

type compiledPolicyStrategy struct {
	strategyKey string
	ruleKey     string
	priority    int
	program     *CompiledCELRule
	effect      StrategyEffect
}

// CompiledPolicySnapshot is immutable and safe to reuse across evaluations.
type CompiledPolicySnapshot struct {
	policyKey       string
	version         int
	factorEngine    *FactorEngine
	strategies      []compiledPolicyStrategy
	defaultDecision DecisionTemplate
}

type StrategyEvaluation struct {
	StrategyKey string
	RuleKey     string
	Priority    int
	Matched     bool
}

type PolicyEvaluation struct {
	PolicyKey          string
	PolicyVersion      int
	Decision           string
	ReasonCodes        []string
	MatchedStrategyKey string
	MatchedRuleKey     string
	Actions            []PolicyAction
	Factors            FactorSnapshot
	StrategyResults    []StrategyEvaluation
}

func CompilePolicySnapshot(
	policy ExecutablePolicyDefinition,
	factorDefinitions []FactorDefinition,
	rpcAdapters *RPCFactorAdapterRegistry,
) (*CompiledPolicySnapshot, error) {
	return CompilePolicySnapshotWithProviders(policy, factorDefinitions, rpcAdapters, nil)
}

func CompilePolicySnapshotWithProviders(
	policy ExecutablePolicyDefinition,
	factorDefinitions []FactorDefinition,
	rpcAdapters *RPCFactorAdapterRegistry,
	additionalProviders []FactorProvider,
) (*CompiledPolicySnapshot, error) {
	if policy.PolicyKey == "" || policy.Version <= 0 {
		return nil, fmt.Errorf("policy_key and positive version are required")
	}
	if !validPolicyDecision(policy.DefaultDecision.Decision) {
		return nil, fmt.Errorf("default decision must be pass or block")
	}
	if len(policy.Strategies) == 0 {
		return nil, fmt.Errorf("policy must contain at least one strategy")
	}

	expressionProvider, err := NewExpressionFactorProvider(factorDefinitions)
	if err != nil {
		return nil, err
	}
	registry := NewFactorRegistry()
	providers := []FactorProvider{
		NewValueFactorProvider(),
		expressionProvider,
		NewRPCFactorProvider(rpcAdapters),
	}
	providers = append(providers, additionalProviders...)
	for _, provider := range providers {
		if err := registry.RegisterProvider(provider); err != nil {
			return nil, err
		}
	}
	factors := make([]Factor, 0, len(factorDefinitions))
	for _, definition := range factorDefinitions {
		factor, err := registry.NewFactor(definition)
		if err != nil {
			return nil, fmt.Errorf("create factor %q: %w", definition.Key, err)
		}
		factors = append(factors, factor)
	}
	factorEngine, err := NewFactorEngine(factors)
	if err != nil {
		return nil, fmt.Errorf("create factor engine: %w", err)
	}

	strategyDefinitions := append([]ExecutableStrategyDefinition(nil), policy.Strategies...)
	sort.SliceStable(strategyDefinitions, func(i, j int) bool {
		if strategyDefinitions[i].Priority != strategyDefinitions[j].Priority {
			return strategyDefinitions[i].Priority > strategyDefinitions[j].Priority
		}
		return strategyDefinitions[i].StrategyKey < strategyDefinitions[j].StrategyKey
	})
	seenStrategies := make(map[string]struct{}, len(strategyDefinitions))
	compiledStrategies := make([]compiledPolicyStrategy, 0, len(strategyDefinitions))
	for _, definition := range strategyDefinitions {
		if definition.StrategyKey == "" || definition.Rule.RuleKey == "" {
			return nil, fmt.Errorf("strategy key, rule key and effect decision are required")
		}
		if !validPolicyDecision(definition.Effect.Decision.Decision) {
			return nil, fmt.Errorf("strategy %q decision must be pass or block", definition.StrategyKey)
		}
		for _, action := range definition.Effect.Actions {
			if action.ActionCode == "" {
				return nil, fmt.Errorf("strategy %q action code is required", definition.StrategyKey)
			}
		}
		if _, exists := seenStrategies[definition.StrategyKey]; exists {
			return nil, fmt.Errorf("strategy %q is declared more than once", definition.StrategyKey)
		}
		seenStrategies[definition.StrategyKey] = struct{}{}
		program, err := CompileCELRule(definition.Rule, factorDefinitions)
		if err != nil {
			return nil, err
		}
		compiledStrategies = append(compiledStrategies, compiledPolicyStrategy{
			strategyKey: definition.StrategyKey,
			ruleKey:     definition.Rule.RuleKey,
			priority:    definition.Priority,
			program:     program,
			effect:      cloneStrategyEffect(definition.Effect),
		})
	}

	return &CompiledPolicySnapshot{
		policyKey:       policy.PolicyKey,
		version:         policy.Version,
		factorEngine:    factorEngine,
		strategies:      compiledStrategies,
		defaultDecision: cloneDecisionTemplate(policy.DefaultDecision),
	}, nil
}

func (p *CompiledPolicySnapshot) Evaluate(ctx context.Context, params map[string]any) (PolicyEvaluation, error) {
	factors, err := p.factorEngine.Evaluate(ctx, params)
	if err != nil {
		return PolicyEvaluation{}, err
	}
	for key, result := range factors.Results() {
		if result.Status != FactorStatusSuccess {
			return PolicyEvaluation{}, fmt.Errorf("factor %q failed with %s: %s", key, result.ErrorCode, result.ErrorMessage)
		}
	}

	evaluation := PolicyEvaluation{
		PolicyKey:       p.policyKey,
		PolicyVersion:   p.version,
		Factors:         factors,
		Actions:         []PolicyAction{},
		StrategyResults: make([]StrategyEvaluation, 0, len(p.strategies)),
	}
	values := factors.Values()
	for _, strategy := range p.strategies {
		matched, err := strategy.program.Evaluate(values)
		if err != nil {
			return PolicyEvaluation{}, err
		}
		evaluation.StrategyResults = append(evaluation.StrategyResults, StrategyEvaluation{
			StrategyKey: strategy.strategyKey, RuleKey: strategy.ruleKey,
			Priority: strategy.priority, Matched: matched,
		})
		if matched {
			evaluation.Decision = strategy.effect.Decision.Decision
			evaluation.ReasonCodes = append([]string(nil), strategy.effect.Decision.ReasonCodes...)
			evaluation.MatchedStrategyKey = strategy.strategyKey
			evaluation.MatchedRuleKey = strategy.ruleKey
			evaluation.Actions = clonePolicyActions(strategy.effect.Actions)
			return evaluation, nil
		}
	}
	evaluation.Decision = p.defaultDecision.Decision
	evaluation.ReasonCodes = append([]string(nil), p.defaultDecision.ReasonCodes...)
	return evaluation, nil
}

func validPolicyDecision(decision string) bool {
	return decision == "pass" || decision == "block"
}

func cloneStrategyEffect(effect StrategyEffect) StrategyEffect {
	return StrategyEffect{
		Decision: cloneDecisionTemplate(effect.Decision),
		Actions:  clonePolicyActions(effect.Actions),
	}
}

func clonePolicyActions(actions []PolicyAction) []PolicyAction {
	cloned := make([]PolicyAction, len(actions))
	for i, action := range actions {
		cloned[i] = PolicyAction{ActionCode: action.ActionCode, Params: clonePolicyActionParams(action.Params)}
	}
	return cloned
}

func clonePolicyActionParams(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	cloned := make(map[string]any, len(params))
	for key, value := range params {
		switch typed := value.(type) {
		case map[string]any:
			cloned[key] = clonePolicyActionParams(typed)
		case []string:
			cloned[key] = append([]string(nil), typed...)
		case []any:
			items := make([]any, len(typed))
			for i, item := range typed {
				items[i] = clonePolicyActionParams(map[string]any{"value": item})["value"]
			}
			cloned[key] = items
		default:
			cloned[key] = value
		}
	}
	return cloned
}

func cloneDecisionTemplate(template DecisionTemplate) DecisionTemplate {
	return DecisionTemplate{
		Decision:    template.Decision,
		ReasonCodes: append([]string(nil), template.ReasonCodes...),
	}
}
