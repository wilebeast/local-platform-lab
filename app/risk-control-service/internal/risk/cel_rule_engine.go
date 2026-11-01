package risk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"

	"cel.dev/cel-go/cel"
)

type CELRule struct {
	RuleKey    string
	Version    int
	Expression string
}

type CompiledCELRule struct {
	rule    CELRule
	program cel.Program
}

type CELProgramRegistry struct {
	mu       sync.RWMutex
	programs map[string]celProgramEntry
}

type celProgramEntry struct {
	fingerprint string
	program     *CompiledCELRule
}

func NewCELProgramRegistry() *CELProgramRegistry {
	return &CELProgramRegistry{programs: make(map[string]celProgramEntry)}
}

func (r *CELProgramRegistry) Publish(rule CELRule, factorSchemaVersion int, factors []FactorDefinition) (*CompiledCELRule, error) {
	if factorSchemaVersion <= 0 {
		return nil, fmt.Errorf("positive factor schema version is required")
	}
	key := celProgramKey(rule.RuleKey, rule.Version, factorSchemaVersion)
	fingerprint := celPublicationFingerprint(rule, factors)

	r.mu.RLock()
	existing, found := r.programs[key]
	r.mu.RUnlock()
	if found {
		if existing.fingerprint != fingerprint {
			return nil, fmt.Errorf("rule %q version %d with factor schema version %d is already published with different content", rule.RuleKey, rule.Version, factorSchemaVersion)
		}
		return existing.program, nil
	}

	compiled, err := CompileCELRule(rule, factors)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, found := r.programs[key]; found {
		if existing.fingerprint != fingerprint {
			return nil, fmt.Errorf("rule %q version %d with factor schema version %d is already published with different content", rule.RuleKey, rule.Version, factorSchemaVersion)
		}
		return existing.program, nil
	}
	r.programs[key] = celProgramEntry{fingerprint: fingerprint, program: compiled}
	return compiled, nil
}

func celProgramKey(ruleKey string, ruleVersion, factorSchemaVersion int) string {
	return fmt.Sprintf("%s\x00%d\x00%d", ruleKey, ruleVersion, factorSchemaVersion)
}

func celPublicationFingerprint(rule CELRule, factors []FactorDefinition) string {
	canonicalFactors := append([]FactorDefinition(nil), factors...)
	sort.Slice(canonicalFactors, func(i, j int) bool {
		if canonicalFactors[i].Key != canonicalFactors[j].Key {
			return canonicalFactors[i].Key < canonicalFactors[j].Key
		}
		return canonicalFactors[i].ValueType < canonicalFactors[j].ValueType
	})
	var canonical strings.Builder
	fmt.Fprintf(&canonical, "%s\n%d\n%s\n", rule.RuleKey, rule.Version, rule.Expression)
	for _, factor := range canonicalFactors {
		fmt.Fprintf(
			&canonical,
			"%s:%s:%s:%s:%s:%s:%d\n",
			factor.Key,
			factor.Type,
			factor.ValueType,
			factor.ParamKey,
			factor.Expression,
			factor.ResolverKey,
			factor.Timeout,
		)
	}
	sum := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(sum[:])
}

func CompileCELRule(rule CELRule, factors []FactorDefinition) (*CompiledCELRule, error) {
	if rule.RuleKey == "" || rule.Version <= 0 || rule.Expression == "" {
		return nil, fmt.Errorf("rule_key, positive version and expression are required")
	}
	options := make([]cel.EnvOption, 0, len(factors))
	seen := make(map[string]struct{}, len(factors))
	for _, factor := range factors {
		if factor.Key == "" {
			return nil, fmt.Errorf("factor key is required")
		}
		if _, exists := seen[factor.Key]; exists {
			return nil, fmt.Errorf("factor %q is declared more than once", factor.Key)
		}
		seen[factor.Key] = struct{}{}
		factorType, err := celFactorType(string(factor.ValueType))
		if err != nil {
			return nil, fmt.Errorf("factor %q: %w", factor.Key, err)
		}
		options = append(options, cel.Variable(factor.Key, factorType))
	}
	environment, err := cel.NewEnv(options...)
	if err != nil {
		return nil, fmt.Errorf("create CEL environment: %w", err)
	}
	ast, issues := environment.Compile(rule.Expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("compile rule %q: %w", rule.RuleKey, issues.Err())
	}
	if ast.OutputType().TypeName() != cel.BoolType.TypeName() {
		return nil, fmt.Errorf("rule %q must return bool, got %s", rule.RuleKey, ast.OutputType().TypeName())
	}
	program, err := environment.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("create program for rule %q: %w", rule.RuleKey, err)
	}
	return &CompiledCELRule{rule: rule, program: program}, nil
}

func (r *CompiledCELRule) Evaluate(values map[string]any) (bool, error) {
	output, _, err := r.program.Eval(values)
	if err != nil {
		return false, fmt.Errorf("evaluate rule %q: %w", r.rule.RuleKey, err)
	}
	matched, ok := output.Value().(bool)
	if !ok {
		return false, fmt.Errorf("rule %q returned %T instead of bool", r.rule.RuleKey, output.Value())
	}
	return matched, nil
}

func celFactorType(valueType string) (*cel.Type, error) {
	switch valueType {
	case "int":
		return cel.IntType, nil
	case "double":
		return cel.DoubleType, nil
	case "string":
		return cel.StringType, nil
	case "bool":
		return cel.BoolType, nil
	case "list<string>":
		return cel.ListType(cel.StringType), nil
	default:
		return nil, fmt.Errorf("unsupported value type %q", valueType)
	}
}
