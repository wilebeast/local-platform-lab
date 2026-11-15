package featureplatform

import (
	"context"
	"fmt"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/risk"
)

const featureFactorInputParam = "__feature_platform_factor_inputs"

type FeatureFetchPlanEntry struct {
	FactorKey  string
	Definition ServingFeatureDefinition
}

// FeatureFetchPlan is an immutable runtime optimization compiled from the
// Feature Factor definitions used by a policy snapshot. It is not part of the
// policy or strategy domain model.
type FeatureFetchPlan struct {
	entries  []FeatureFetchPlanEntry
	byFactor map[string]FeatureFetchPlanEntry
	maxAge   time.Duration
}

func LoadFeatureFetchPlan(
	ctx context.Context,
	catalog FeatureCatalog,
	factors []risk.FactorDefinition,
) (*FeatureFetchPlan, error) {
	if catalog == nil {
		return nil, fmt.Errorf("feature catalog is required")
	}
	refs, err := featureRefsFromFactorDefinitions(factors)
	if err != nil {
		return nil, err
	}
	definitions, err := catalog.ResolveFeatures(ctx, refs)
	if err != nil {
		return nil, fmt.Errorf("resolve feature factor definitions: %w", err)
	}
	return CompileFeatureFetchPlan(factors, definitions)
}

func CompileFeatureFetchPlan(
	factors []risk.FactorDefinition,
	definitions []ServingFeatureDefinition,
) (*FeatureFetchPlan, error) {
	featureFactors := make([]risk.FactorDefinition, 0)
	for _, factor := range factors {
		if factor.Type == risk.FactorTypeFeature {
			featureFactors = append(featureFactors, factor)
		}
	}
	if len(featureFactors) == 0 || len(definitions) == 0 {
		return nil, fmt.Errorf("feature factors and resolved definitions must be complete")
	}
	expectedReferences := make(map[string]struct{}, len(featureFactors))
	for _, factor := range featureFactors {
		ref, err := featureRefFromFactorDefinition(factor)
		if err != nil {
			return nil, err
		}
		expectedReferences[featureRefIdentity(ref)] = struct{}{}
	}
	definitionByRef := make(map[string]ServingFeatureDefinition, len(definitions))
	for _, definition := range definitions {
		if definition.ValueType == "" || definition.MaxAge <= 0 {
			return nil, fmt.Errorf("resolved feature definition requires type and positive max age")
		}
		identity := featureRefIdentity(definition.Ref)
		if _, expected := expectedReferences[identity]; !expected {
			return nil, fmt.Errorf("resolved feature definition %s was not requested", identity)
		}
		if _, duplicate := definitionByRef[identity]; duplicate {
			return nil, fmt.Errorf("resolved feature definition %s is duplicated", identity)
		}
		definitionByRef[identity] = definition
	}
	if len(definitionByRef) != len(expectedReferences) {
		return nil, fmt.Errorf("feature factors and resolved definitions must be complete")
	}
	plan := &FeatureFetchPlan{
		entries:  make([]FeatureFetchPlanEntry, 0, len(featureFactors)),
		byFactor: make(map[string]FeatureFetchPlanEntry, len(featureFactors)),
	}
	for _, factor := range featureFactors {
		ref, err := featureRefFromFactorDefinition(factor)
		if err != nil {
			return nil, err
		}
		if _, exists := plan.byFactor[factor.Key]; exists {
			return nil, fmt.Errorf("feature factor %q is declared more than once", factor.Key)
		}
		definition, found := definitionByRef[featureRefIdentity(ref)]
		if !found {
			return nil, fmt.Errorf("feature factor %q reference %s is unresolved", factor.Key, featureRefIdentity(ref))
		}
		wantType, err := riskFactorValueType(definition.ValueType)
		if err != nil {
			return nil, err
		}
		if factor.ValueType != wantType {
			return nil, fmt.Errorf("feature factor %q expects %s, got %s", factor.Key, wantType, factor.ValueType)
		}
		entry := FeatureFetchPlanEntry{FactorKey: factor.Key, Definition: definition}
		plan.entries = append(plan.entries, entry)
		plan.byFactor[factor.Key] = entry
		if plan.maxAge == 0 || definition.MaxAge < plan.maxAge {
			plan.maxAge = definition.MaxAge
		}
	}
	return plan, nil
}

func (p *FeatureFetchPlan) Entries() []FeatureFetchPlanEntry {
	if p == nil {
		return nil
	}
	return append([]FeatureFetchPlanEntry(nil), p.entries...)
}

func (p *FeatureFetchPlan) featureRefs() []FeatureRef {
	refs := make([]FeatureRef, 0, len(p.entries))
	seen := make(map[string]struct{}, len(p.entries))
	for _, entry := range p.entries {
		identity := featureRefIdentity(entry.Definition.Ref)
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		refs = append(refs, entry.Definition.Ref)
	}
	return refs
}

func featureRefsFromFactorDefinitions(factors []risk.FactorDefinition) ([]FeatureRef, error) {
	refs := make([]FeatureRef, 0)
	seen := make(map[string]struct{})
	for _, factor := range factors {
		if factor.Type != risk.FactorTypeFeature {
			continue
		}
		ref, err := featureRefFromFactorDefinition(factor)
		if err != nil {
			return nil, err
		}
		identity := featureRefIdentity(ref)
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("at least one feature factor is required")
	}
	return refs, nil
}

func featureRefFromFactorDefinition(factor risk.FactorDefinition) (FeatureRef, error) {
	if factor.Key == "" || factor.FeatureSetKey == "" || factor.FeatureSetVersion <= 0 || factor.FeatureKey == "" {
		return FeatureRef{}, fmt.Errorf("feature factor %q requires a complete feature reference", factor.Key)
	}
	if factor.ParamKey != "" || factor.Expression != "" || factor.ResolverKey != "" || factor.Timeout != 0 {
		return FeatureRef{}, fmt.Errorf("feature factor %q must define only its feature reference", factor.Key)
	}
	return FeatureRef{
		FeatureSetKey: factor.FeatureSetKey, FeatureSetVersion: factor.FeatureSetVersion,
		FeatureKey: factor.FeatureKey,
	}, nil
}

type FeatureFactorProvider struct{ plan *FeatureFetchPlan }

func NewFeatureFactorProvider(plan *FeatureFetchPlan) (*FeatureFactorProvider, error) {
	if plan == nil || len(plan.entries) == 0 {
		return nil, fmt.Errorf("compiled feature fetch plan is required")
	}
	return &FeatureFactorProvider{plan: plan}, nil
}

func (*FeatureFactorProvider) Type() risk.FactorType { return risk.FactorTypeFeature }

func (p *FeatureFactorProvider) NewFactor(definition risk.FactorDefinition) (risk.Factor, error) {
	ref, err := featureRefFromFactorDefinition(definition)
	if err != nil {
		return nil, err
	}
	if definition.Type != risk.FactorTypeFeature {
		return nil, fmt.Errorf("feature factor type is required")
	}
	entry, found := p.plan.byFactor[definition.Key]
	if !found || featureRefIdentity(entry.Definition.Ref) != featureRefIdentity(ref) {
		return nil, fmt.Errorf("feature factor %q is not present in the compiled fetch plan", definition.Key)
	}
	wantType, err := riskFactorValueType(entry.Definition.ValueType)
	if err != nil {
		return nil, err
	}
	if definition.ValueType != wantType {
		return nil, fmt.Errorf("feature factor %q expects %s, got %s", definition.Key, wantType, definition.ValueType)
	}
	return &featureFactor{key: definition.Key, valueType: wantType}, nil
}

type featureFactor struct {
	key       string
	valueType risk.FactorValueType
}

func (f *featureFactor) Key() string                 { return f.key }
func (*featureFactor) DependentFactorKeys() []string { return nil }

func (f *featureFactor) Fetch(_ context.Context, runtime risk.FactorRuntime) risk.FactorResult {
	input, found := runtime.ParamValue(featureFactorInputParam)
	values, ok := input.(map[string]FeatureValueResult)
	if !found || !ok {
		return featureFactorFailure(f.key, risk.FactorStatusError, "feature_fetch_plan_missing", "feature fetch plan was not executed")
	}
	value, found := values[f.key]
	if !found {
		return featureFactorFailure(f.key, risk.FactorStatusMissing, "feature_value_missing", "feature factor is missing from the serving response")
	}
	evidence := &risk.FactorEvidence{
		SourceType: "feature", SourceRef: fmt.Sprintf("%s:%d:%s", value.Ref.FeatureSetKey, value.Ref.FeatureSetVersion, value.Ref.FeatureKey),
		SnapshotAsOf: value.SnapshotAsOf, ComputedAt: value.ComputedAt,
		SourceRunID: value.SourceRunID, ContentFingerprint: value.ContentFingerprint,
	}
	switch value.Status {
	case ValueStatusSuccess:
		converted, err := convertFeatureFactorValue(value.Value, f.valueType)
		if err != nil {
			result := featureFactorFailure(f.key, risk.FactorStatusError, "feature_type_mismatch", err.Error())
			result.Evidence = evidence
			return result
		}
		return risk.FactorResult{Key: f.key, Status: risk.FactorStatusSuccess, Value: converted, Evidence: evidence}
	case ValueStatusMissing:
		result := featureFactorFailure(f.key, risk.FactorStatusMissing, "feature_missing", "feature snapshot is missing")
		result.Evidence = evidence
		return result
	case ValueStatusUndefined:
		result := featureFactorFailure(f.key, risk.FactorStatusMissing, "feature_undefined", "feature value is undefined")
		result.Evidence = evidence
		return result
	case ValueStatusStale:
		result := featureFactorFailure(f.key, risk.FactorStatusError, "feature_stale", "feature snapshot is stale")
		result.Evidence = evidence
		return result
	default:
		code := value.ErrorCode
		if code == "" {
			code = "feature_serving_error"
		}
		result := featureFactorFailure(f.key, risk.FactorStatusError, code, "feature serving returned an error")
		result.Evidence = evidence
		return result
	}
}

type FeaturePolicyEvaluationRequest struct {
	Principal string
	RequestID string
	Subject   Subject
	Cutoff    time.Time
	Params    map[string]any
}

// FeaturePolicyHistoricalEvaluationRequest evaluates one policy against the
// exact Feature Snapshot that existed at a fixed business-time boundary.
// It is used by offline evaluation and deterministic replay.
type FeaturePolicyHistoricalEvaluationRequest struct {
	Principal string
	RequestID string
	Subject   Subject
	AsOf      time.Time
	Params    map[string]any
}

type FeaturePolicyRuntime struct {
	policy  *risk.CompiledPolicySnapshot
	plan    *FeatureFetchPlan
	serving FeatureServing
}

func NewFeaturePolicyRuntime(
	policy *risk.CompiledPolicySnapshot,
	plan *FeatureFetchPlan,
	serving FeatureServing,
) (*FeaturePolicyRuntime, error) {
	if policy == nil || plan == nil || serving == nil {
		return nil, fmt.Errorf("compiled policy, feature fetch plan and serving client are required")
	}
	return &FeaturePolicyRuntime{policy: policy, plan: plan, serving: serving}, nil
}

func (r *FeaturePolicyRuntime) EvaluateLatestBefore(
	ctx context.Context,
	request FeaturePolicyEvaluationRequest,
) (risk.PolicyEvaluation, error) {
	if request.Principal == "" || request.RequestID == "" || request.Subject.Type == "" || len(request.Subject.Keys) == 0 || request.Cutoff.IsZero() {
		return risk.PolicyEvaluation{}, fmt.Errorf("principal, request, subject and cutoff are required")
	}
	response, err := r.serving.BatchGetLatestBefore(ctx, BatchGetLatestBeforeRequest{
		Principal: request.Principal, RequestID: request.RequestID,
		Subjects: []Subject{request.Subject}, Features: r.plan.featureRefs(),
		Cutoff: request.Cutoff, MaxAge: r.plan.maxAge,
	})
	if err != nil {
		return risk.PolicyEvaluation{}, fmt.Errorf("fetch feature factors: %w", err)
	}
	return r.evaluateResponse(ctx, request.Params, request.RequestID, request.Subject, response)
}

func (r *FeaturePolicyRuntime) EvaluateAt(
	ctx context.Context,
	request FeaturePolicyHistoricalEvaluationRequest,
) (risk.PolicyEvaluation, error) {
	if request.Principal == "" || request.RequestID == "" || request.Subject.Type == "" || len(request.Subject.Keys) == 0 || request.AsOf.IsZero() {
		return risk.PolicyEvaluation{}, fmt.Errorf("principal, request, subject and as_of are required")
	}
	response, err := r.serving.BatchGetAt(ctx, BatchGetAtRequest{
		Principal: request.Principal, RequestID: request.RequestID,
		Subjects: []Subject{request.Subject}, Features: r.plan.featureRefs(),
		AsOf: request.AsOf,
	})
	if err != nil {
		return risk.PolicyEvaluation{}, fmt.Errorf("fetch historical feature factors: %w", err)
	}
	return r.evaluateResponse(ctx, request.Params, request.RequestID, request.Subject, response)
}

func (r *FeaturePolicyRuntime) evaluateResponse(
	ctx context.Context,
	params map[string]any,
	requestID string,
	subject Subject,
	response BatchGetResponse,
) (risk.PolicyEvaluation, error) {
	if response.RequestID != requestID {
		return risk.PolicyEvaluation{}, fmt.Errorf("feature serving response request_id mismatch")
	}
	byReference := make(map[string]FeatureValueResult, len(response.Values))
	for _, value := range response.Values {
		if subjectIdentity(value.Subject) != subjectIdentity(subject) {
			return risk.PolicyEvaluation{}, fmt.Errorf("feature serving returned an unexpected subject")
		}
		byReference[featureRefIdentity(value.Ref)] = value
	}
	factorInputs := make(map[string]FeatureValueResult, len(r.plan.entries))
	for _, entry := range r.plan.entries {
		value, found := byReference[featureRefIdentity(entry.Definition.Ref)]
		if !found {
			value = FeatureValueResult{Subject: subject, Ref: entry.Definition.Ref, ValueType: entry.Definition.ValueType, Status: ValueStatusMissing}
		}
		factorInputs[entry.FactorKey] = value
	}
	policyParams := make(map[string]any, len(params)+1)
	for key, value := range params {
		policyParams[key] = value
	}
	policyParams[featureFactorInputParam] = factorInputs
	return r.policy.Evaluate(ctx, policyParams)
}

func riskFactorValueType(valueType string) (risk.FactorValueType, error) {
	switch valueType {
	case "int":
		return risk.FactorValueTypeInt, nil
	case "double", "decimal":
		return risk.FactorValueTypeDouble, nil
	case "string":
		return risk.FactorValueTypeString, nil
	case "bool":
		return risk.FactorValueTypeBool, nil
	default:
		return "", fmt.Errorf("feature value type %q is not supported by the risk factor runtime", valueType)
	}
}

func convertFeatureFactorValue(value any, valueType risk.FactorValueType) (any, error) {
	switch valueType {
	case risk.FactorValueTypeInt:
		if converted, ok := value.(int64); ok {
			return converted, nil
		}
	case risk.FactorValueTypeDouble:
		if converted, ok := value.(float64); ok {
			return converted, nil
		}
	case risk.FactorValueTypeString:
		if converted, ok := value.(string); ok {
			return converted, nil
		}
	case risk.FactorValueTypeBool:
		if converted, ok := value.(bool); ok {
			return converted, nil
		}
	}
	return nil, fmt.Errorf("feature value %T does not match factor type %s", value, valueType)
}

func featureFactorFailure(key string, status risk.FactorStatus, code, message string) risk.FactorResult {
	return risk.FactorResult{Key: key, Status: status, ErrorCode: code, ErrorMessage: message}
}
