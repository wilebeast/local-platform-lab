package risk

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"cel.dev/cel-go/cel"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

type FactorType string

const (
	FactorTypeValue            FactorType = "value"
	FactorTypeExpression       FactorType = "expression"
	FactorTypeRPC              FactorType = "rpc"
	FactorTypeWarehouseFeature FactorType = "warehouse_feature"
)

type FactorValueType string

const (
	FactorValueTypeInt     FactorValueType = "int"
	FactorValueTypeDouble  FactorValueType = "double"
	FactorValueTypeString  FactorValueType = "string"
	FactorValueTypeBool    FactorValueType = "bool"
	FactorValueTypeStrings FactorValueType = "list<string>"
)

// FactorDefinition is consumed while compiling a policy snapshot. Runtime
// evaluation uses the immutable Factor returned by a FactorProvider instead.
type FactorDefinition struct {
	Key         string
	Type        FactorType
	ValueType   FactorValueType
	ParamKey    string
	Expression  string
	ResolverKey string
	Timeout     time.Duration
}

type FactorStatus string

const (
	FactorStatusSuccess FactorStatus = "success"
	FactorStatusMissing FactorStatus = "missing"
	FactorStatusError   FactorStatus = "error"
)

type FactorResult struct {
	Key          string
	Status       FactorStatus
	Value        any
	ErrorCode    string
	ErrorMessage string
}

// FactorRuntime contains mutable values for exactly one policy evaluation.
type FactorRuntime interface {
	ParamValue(key string) (any, bool)
	GetFactor(key string) (FactorResult, bool)
}

// Factor is immutable after creation and may be reused concurrently.
type Factor interface {
	Key() string
	DependentFactorKeys() []string
	Fetch(ctx context.Context, runtime FactorRuntime) FactorResult
}

// FactorProvider creates one concrete Factor type from its static definition.
type FactorProvider interface {
	Type() FactorType
	NewFactor(definition FactorDefinition) (Factor, error)
}

type FactorRegistry struct {
	providers map[FactorType]FactorProvider
}

func NewFactorRegistry() *FactorRegistry {
	return &FactorRegistry{providers: make(map[FactorType]FactorProvider)}
}

func (r *FactorRegistry) RegisterProvider(provider FactorProvider) error {
	if provider == nil {
		return fmt.Errorf("factor provider is required")
	}
	if _, exists := r.providers[provider.Type()]; exists {
		return fmt.Errorf("factor provider %q is already registered", provider.Type())
	}
	r.providers[provider.Type()] = provider
	return nil
}

func (r *FactorRegistry) NewFactor(definition FactorDefinition) (Factor, error) {
	provider, found := r.providers[definition.Type]
	if !found {
		return nil, fmt.Errorf("factor type %q is not registered", definition.Type)
	}
	return provider.NewFactor(definition)
}

type ValueFactorProvider struct{}

func NewValueFactorProvider() *ValueFactorProvider { return &ValueFactorProvider{} }
func (*ValueFactorProvider) Type() FactorType      { return FactorTypeValue }

func (*ValueFactorProvider) NewFactor(definition FactorDefinition) (Factor, error) {
	if err := validateFactorDefinitionBase(definition); err != nil {
		return nil, err
	}
	if definition.ParamKey == "" || definition.Expression != "" || definition.ResolverKey != "" {
		return nil, fmt.Errorf("value factor %q must define only param_key", definition.Key)
	}
	return &valueFactor{key: definition.Key, paramKey: definition.ParamKey, valueType: definition.ValueType}, nil
}

type valueFactor struct {
	key       string
	paramKey  string
	valueType FactorValueType
}

func (f *valueFactor) Key() string                 { return f.key }
func (*valueFactor) DependentFactorKeys() []string { return nil }
func (f *valueFactor) Fetch(_ context.Context, runtime FactorRuntime) FactorResult {
	value, found := runtime.ParamValue(f.paramKey)
	if !found {
		return factorFailure(f.key, FactorStatusMissing, "missing_param", "required parameter is missing")
	}
	converted, err := convertFactorValue(value, f.valueType)
	if err != nil {
		return factorFailure(f.key, FactorStatusError, "type_mismatch", err.Error())
	}
	return FactorResult{Key: f.key, Status: FactorStatusSuccess, Value: converted}
}

type ExpressionFactorProvider struct {
	environment *cel.Env
	factorKeys  map[string]struct{}
}

func NewExpressionFactorProvider(definitions []FactorDefinition) (*ExpressionFactorProvider, error) {
	options := make([]cel.EnvOption, 0, len(definitions))
	factorKeys := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if definition.Key == "" {
			return nil, fmt.Errorf("factor key is required")
		}
		if _, exists := factorKeys[definition.Key]; exists {
			return nil, fmt.Errorf("factor %q is declared more than once", definition.Key)
		}
		factorKeys[definition.Key] = struct{}{}
		valueType, err := celFactorType(string(definition.ValueType))
		if err != nil {
			return nil, fmt.Errorf("factor %q: %w", definition.Key, err)
		}
		options = append(options, cel.Variable(definition.Key, valueType))
	}
	environment, err := cel.NewEnv(options...)
	if err != nil {
		return nil, fmt.Errorf("create expression factor environment: %w", err)
	}
	return &ExpressionFactorProvider{environment: environment, factorKeys: factorKeys}, nil
}

func (*ExpressionFactorProvider) Type() FactorType { return FactorTypeExpression }

func (p *ExpressionFactorProvider) NewFactor(definition FactorDefinition) (Factor, error) {
	if err := validateFactorDefinitionBase(definition); err != nil {
		return nil, err
	}
	if definition.Expression == "" || definition.ParamKey != "" || definition.ResolverKey != "" {
		return nil, fmt.Errorf("expression factor %q must define only expression", definition.Key)
	}
	ast, issues := p.environment.Compile(definition.Expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("compile expression factor %q: %w", definition.Key, issues.Err())
	}
	wantType, _ := celFactorType(string(definition.ValueType))
	if ast.OutputType().TypeName() != wantType.TypeName() {
		return nil, fmt.Errorf("expression factor %q must return %s, got %s", definition.Key, wantType.TypeName(), ast.OutputType().TypeName())
	}
	program, err := p.environment.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("create expression factor program %q: %w", definition.Key, err)
	}
	dependencies := make(map[string]struct{})
	collectFactorIdentifiers(ast.Expr(), p.factorKeys, dependencies)
	orderedDependencies := make([]string, 0, len(dependencies))
	for dependency := range dependencies {
		orderedDependencies = append(orderedDependencies, dependency)
	}
	sort.Strings(orderedDependencies)
	return &expressionFactor{
		key:          definition.Key,
		valueType:    definition.ValueType,
		dependencies: orderedDependencies,
		program:      program,
	}, nil
}

type expressionFactor struct {
	key          string
	valueType    FactorValueType
	dependencies []string
	program      cel.Program
}

func (f *expressionFactor) Key() string { return f.key }
func (f *expressionFactor) DependentFactorKeys() []string {
	return append([]string(nil), f.dependencies...)
}

func (f *expressionFactor) Fetch(_ context.Context, runtime FactorRuntime) FactorResult {
	values := make(map[string]any, len(f.dependencies))
	for _, dependency := range f.dependencies {
		result, found := runtime.GetFactor(dependency)
		if !found || result.Status != FactorStatusSuccess {
			return factorFailure(f.key, FactorStatusError, "dependency_error", fmt.Sprintf("factor %q is unavailable", dependency))
		}
		values[dependency] = result.Value
	}
	output, _, err := f.program.Eval(values)
	if err != nil {
		return factorFailure(f.key, FactorStatusError, "expression_evaluation_error", err.Error())
	}
	converted, err := convertFactorValue(output.Value(), f.valueType)
	if err != nil {
		return factorFailure(f.key, FactorStatusError, "result_type_mismatch", err.Error())
	}
	return FactorResult{Key: f.key, Status: FactorStatusSuccess, Value: converted}
}

func collectFactorIdentifiers(expression *exprpb.Expr, factorKeys map[string]struct{}, found map[string]struct{}) {
	if expression == nil {
		return
	}
	if identifier := expression.GetIdentExpr(); identifier != nil {
		if _, isFactor := factorKeys[identifier.Name]; isFactor {
			found[identifier.Name] = struct{}{}
		}
		return
	}
	if selection := expression.GetSelectExpr(); selection != nil {
		collectFactorIdentifiers(selection.Operand, factorKeys, found)
		return
	}
	if call := expression.GetCallExpr(); call != nil {
		collectFactorIdentifiers(call.Target, factorKeys, found)
		for _, argument := range call.Args {
			collectFactorIdentifiers(argument, factorKeys, found)
		}
		return
	}
	if list := expression.GetListExpr(); list != nil {
		for _, element := range list.Elements {
			collectFactorIdentifiers(element, factorKeys, found)
		}
		return
	}
	if object := expression.GetStructExpr(); object != nil {
		for _, entry := range object.Entries {
			collectFactorIdentifiers(entry.GetMapKey(), factorKeys, found)
			collectFactorIdentifiers(entry.Value, factorKeys, found)
		}
		return
	}
	if comprehension := expression.GetComprehensionExpr(); comprehension != nil {
		collectFactorIdentifiers(comprehension.IterRange, factorKeys, found)
		collectFactorIdentifiers(comprehension.AccuInit, factorKeys, found)
		collectFactorIdentifiers(comprehension.LoopCondition, factorKeys, found)
		collectFactorIdentifiers(comprehension.LoopStep, factorKeys, found)
		collectFactorIdentifiers(comprehension.Result, factorKeys, found)
	}
}

// RPCFactorAdapter is a code-registered, typed adapter for one approved remote
// operation. Its key is stable configuration identity, not an arbitrary URL.
type RPCFactorAdapter interface {
	Key() string
	DependentFactorKeys() []string
	Fetch(ctx context.Context, runtime FactorRuntime) (any, error)
}

type RPCFactorAdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[string]RPCFactorAdapter
}

func NewRPCFactorAdapterRegistry() *RPCFactorAdapterRegistry {
	return &RPCFactorAdapterRegistry{adapters: make(map[string]RPCFactorAdapter)}
}

func (r *RPCFactorAdapterRegistry) Register(adapter RPCFactorAdapter) error {
	if adapter == nil || adapter.Key() == "" {
		return fmt.Errorf("RPC factor adapter and key are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[adapter.Key()]; exists {
		return fmt.Errorf("RPC factor adapter %q is already registered", adapter.Key())
	}
	r.adapters[adapter.Key()] = adapter
	return nil
}

func (r *RPCFactorAdapterRegistry) Get(key string) (RPCFactorAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, found := r.adapters[key]
	return adapter, found
}

type RPCFactorProvider struct {
	adapters *RPCFactorAdapterRegistry
}

func NewRPCFactorProvider(adapters *RPCFactorAdapterRegistry) *RPCFactorProvider {
	return &RPCFactorProvider{adapters: adapters}
}

func (*RPCFactorProvider) Type() FactorType { return FactorTypeRPC }

func (p *RPCFactorProvider) NewFactor(definition FactorDefinition) (Factor, error) {
	if err := validateFactorDefinitionBase(definition); err != nil {
		return nil, err
	}
	if definition.ResolverKey == "" || definition.ParamKey != "" || definition.Expression != "" {
		return nil, fmt.Errorf("RPC factor %q must define only resolver_key", definition.Key)
	}
	if definition.Timeout <= 0 {
		return nil, fmt.Errorf("RPC factor %q requires a positive timeout", definition.Key)
	}
	if p.adapters == nil {
		return nil, fmt.Errorf("RPC factor adapter registry is required")
	}
	adapter, found := p.adapters.Get(definition.ResolverKey)
	if !found {
		return nil, fmt.Errorf("RPC factor adapter %q is not registered", definition.ResolverKey)
	}
	return &rpcFactor{
		key:          definition.Key,
		valueType:    definition.ValueType,
		dependencies: append([]string(nil), adapter.DependentFactorKeys()...),
		adapter:      adapter,
		timeout:      definition.Timeout,
	}, nil
}

type rpcFactor struct {
	key          string
	valueType    FactorValueType
	dependencies []string
	adapter      RPCFactorAdapter
	timeout      time.Duration
}

func (f *rpcFactor) Key() string { return f.key }
func (f *rpcFactor) DependentFactorKeys() []string {
	return append([]string(nil), f.dependencies...)
}

func (f *rpcFactor) Fetch(ctx context.Context, runtime FactorRuntime) FactorResult {
	callCtx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	value, err := f.adapter.Fetch(callCtx, runtime)
	if err != nil {
		return factorFailure(f.key, FactorStatusError, "rpc_failed", err.Error())
	}
	converted, err := convertFactorValue(value, f.valueType)
	if err != nil {
		return factorFailure(f.key, FactorStatusError, "result_type_mismatch", err.Error())
	}
	return FactorResult{Key: f.key, Status: FactorStatusSuccess, Value: converted}
}

func validateFactorDefinitionBase(definition FactorDefinition) error {
	if definition.Key == "" {
		return fmt.Errorf("factor key is required")
	}
	_, err := celFactorType(string(definition.ValueType))
	return err
}

func convertFactorValue(value any, valueType FactorValueType) (any, error) {
	switch valueType {
	case FactorValueTypeInt:
		converted, ok := value.(int64)
		if ok {
			return converted, nil
		}
		if number, ok := value.(json.Number); ok {
			converted, err := number.Int64()
			if err == nil {
				return converted, nil
			}
		}
	case FactorValueTypeDouble:
		converted, ok := value.(float64)
		if ok {
			return converted, nil
		}
		if number, ok := value.(json.Number); ok {
			converted, err := number.Float64()
			if err == nil {
				return converted, nil
			}
		}
	case FactorValueTypeString:
		converted, ok := value.(string)
		if ok {
			return converted, nil
		}
	case FactorValueTypeBool:
		converted, ok := value.(bool)
		if ok {
			return converted, nil
		}
	case FactorValueTypeStrings:
		converted, ok := value.([]string)
		if ok {
			return append([]string(nil), converted...), nil
		}
	}
	return nil, fmt.Errorf("value of type %T does not match factor value type %q", value, valueType)
}

func factorFailure(key string, status FactorStatus, code, message string) FactorResult {
	return FactorResult{Key: key, Status: status, ErrorCode: code, ErrorMessage: message}
}
