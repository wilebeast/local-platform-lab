// Package riskdemo is a read-only teaching engine. All data providers are local
// substitutes; its in-memory records/outbox do not provide durable recovery.
package riskdemo

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"cel.dev/cel-go/cel"
)

type Definition struct {
	Code, Type, ValueType string
	Config                json.RawMessage
}
type FactorResult struct {
	Code, Status, Source, SourceVersion string
	Value                               any
	ObservedAt                          time.Time
}
type FactorContext interface {
	ParamValue(string) (any, bool)
	GetFactor(string) (FactorResult, bool)
	AsOf() time.Time
}
type Factor interface {
	Code() string
	DependentFactorCodes() []string
	Fetch(context.Context, FactorContext) FactorResult
}
type Provider interface {
	NewFactor(Definition) (Factor, error)
}
type providerFunc func(Definition) (Factor, error)

func (p providerFunc) NewFactor(d Definition) (Factor, error) { return p(d) }

type boundFactor struct {
	code  string
	deps  []string
	fetch func(context.Context, FactorContext) FactorResult
}

func (f boundFactor) Code() string                   { return f.code }
func (f boundFactor) DependentFactorCodes() []string { return append([]string(nil), f.deps...) }
func (f boundFactor) Fetch(ctx context.Context, runtime FactorContext) FactorResult {
	r := f.fetch(ctx, runtime)
	r.Code = f.code
	return r
}

type runtimeState struct {
	params  map[string]any
	results map[string]FactorResult
	asOf    time.Time
}

func (s *runtimeState) ParamValue(k string) (any, bool)         { v, ok := s.params[k]; return v, ok }
func (s *runtimeState) GetFactor(k string) (FactorResult, bool) { v, ok := s.results[k]; return v, ok }
func (s *runtimeState) AsOf() time.Time                         { return s.asOf }
func resolved(v any, source, version string, at time.Time) FactorResult {
	return FactorResult{Value: v, Status: "resolved", Source: source, SourceVersion: version, ObservedAt: at}
}
func unavailable(status, source string) FactorResult {
	return FactorResult{Status: status, Source: source}
}
func dependency(runtime FactorContext, code string) any {
	r, _ := runtime.GetFactor(code)
	return r.Value
}
func config[T any](d Definition) (T, error) {
	var result T
	dec := json.NewDecoder(strings.NewReader(string(d.Config)))
	dec.DisallowUnknownFields()
	err := dec.Decode(&result)
	return result, err
}
func normalize(value any, kind string) (any, error) {
	switch kind {
	case "string":
		if v, ok := value.(string); ok && v != "" {
			return v, nil
		}
	case "bool":
		if v, ok := value.(bool); ok {
			return v, nil
		}
	case "int":
		switch v := value.(type) {
		case int64:
			return v, nil
		case json.Number:
			if n, err := v.Int64(); err == nil {
				return n, nil
			}
		}
	case "double":
		if v, ok := value.(float64); ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
			return v, nil
		}
	}
	return nil, fmt.Errorf("type mismatch: expected %s", kind)
}
func valueProvider() Provider {
	return providerFunc(func(d Definition) (Factor, error) {
		c, err := config[struct{ ParamKey string }](d)
		if err != nil || c.ParamKey == "" {
			return nil, fmt.Errorf("invalid value config")
		}
		return boundFactor{code: d.Code, fetch: func(_ context.Context, r FactorContext) FactorResult {
			v, ok := r.ParamValue(c.ParamKey)
			if !ok {
				return unavailable("missing", "params")
			}
			n, err := normalize(v, d.ValueType)
			if err != nil {
				return unavailable("error", "params")
			}
			return resolved(n, "params", "event-schema-v1", r.AsOf())
		}}, nil
	})
}

type expression struct {
	program      cel.Program
	dependencies []string
}

func compile(env *cel.Env, source string, expected *cel.Type, defs map[string]Definition) (expression, error) {
	ast, issues := env.Compile(source)
	if issues.Err() != nil {
		return expression{}, issues.Err()
	}
	if !ast.OutputType().IsExactType(expected) {
		return expression{}, fmt.Errorf("wrong expression result: expected %s", expected)
	}
	checked, err := cel.AstToCheckedExpr(ast)
	if err != nil {
		return expression{}, err
	}
	refs := map[string]bool{}
	for _, ref := range checked.ReferenceMap {
		if _, ok := defs[ref.Name]; ok {
			refs[ref.Name] = true
		}
	}
	deps := make([]string, 0, len(refs))
	for code := range refs {
		deps = append(deps, code)
	}
	sort.Strings(deps)
	program, err := env.Program(ast, cel.CostLimit(1000))
	return expression{program, deps}, err
}
func expressionProvider(env *cel.Env, defs map[string]Definition) Provider {
	return providerFunc(func(d Definition) (Factor, error) {
		c, err := config[struct{ Expression string }](d)
		if err != nil {
			return nil, err
		}
		e, err := compile(env, c.Expression, celType(d.ValueType), defs)
		if err != nil {
			return nil, err
		}
		return boundFactor{code: d.Code, deps: e.dependencies, fetch: func(ctx context.Context, r FactorContext) FactorResult {
			vars := map[string]any{}
			for _, code := range e.dependencies {
				vars[code] = dependency(r, code)
			}
			v, _, err := e.program.ContextEval(ctx, vars)
			if err != nil {
				return unavailable("error", "expression")
			}
			n, err := normalize(v.Value(), d.ValueType)
			if err != nil {
				return unavailable("error", "expression")
			}
			return resolved(n, "expression", "demo-snapshot-v1", r.AsOf())
		}}, nil
	})
}

type FeatureRecord struct {
	Inflow     int64     `json:"inflow_minor"`
	Outflow    int64     `json:"outflow_minor"`
	ObservedAt time.Time `json:"observed_at"`
	Version    string    `json:"version"`
}
type Sources struct {
	Accounts map[string]struct{ Blocked bool } `json:"accounts"`
	Counters map[string]int64                  `json:"counters"`
	Lists    map[string][]string               `json:"lists"`
	Features map[string]FeatureRecord          `json:"features"`
	Errors   map[string]bool                   `json:"-"`
}

// These providers simulate read-only RPC/Redis/Feature Serving, not generic RPC
// transport, Redis atomic updates, online materialization, or historical storage.
func rpcProvider(src *Sources) Provider {
	return providerFunc(func(d Definition) (Factor, error) {
		c, err := config[struct{ Service, Method, SubjectCode string }](d)
		if err != nil || c.Service != "account-service" || c.Method != "GetRiskState" || c.SubjectCode == "" || d.ValueType != "bool" {
			return nil, fmt.Errorf("unregistered RPC")
		}
		return boundFactor{code: d.Code, deps: []string{c.SubjectCode}, fetch: func(_ context.Context, r FactorContext) FactorResult {
			if src.Errors["rpc"] {
				return unavailable("error", "rpc")
			}
			v, ok := src.Accounts[dependency(r, c.SubjectCode).(string)]
			if !ok {
				return unavailable("missing", "rpc")
			}
			return resolved(v.Blocked, "rpc:account-service/GetRiskState", "demo-response-v1", r.AsOf())
		}}, nil
	})
}
func counterProvider(src *Sources) Provider {
	return providerFunc(func(d Definition) (Factor, error) {
		c, err := config[struct{ SubjectCode, Window string }](d)
		if err != nil || c.SubjectCode == "" || c.Window != "rolling_5m" || d.ValueType != "int" {
			return nil, fmt.Errorf("invalid counter")
		}
		return boundFactor{code: d.Code, deps: []string{c.SubjectCode}, fetch: func(_ context.Context, r FactorContext) FactorResult {
			if src.Errors["counter"] {
				return unavailable("error", "counter")
			}
			v, ok := src.Counters[dependency(r, c.SubjectCode).(string)]
			if !ok {
				return unavailable("missing", "counter")
			}
			return resolved(v, "counter:rolling_5m", "demo-counter-v1", r.AsOf())
		}}, nil
	})
}
func listProvider(src *Sources) Provider {
	return providerFunc(func(d Definition) (Factor, error) {
		c, err := config[struct{ ListCode, SubjectCode string }](d)
		if err != nil || c.SubjectCode == "" || c.ListCode == "" || d.ValueType != "bool" {
			return nil, fmt.Errorf("invalid list")
		}
		return boundFactor{code: d.Code, deps: []string{c.SubjectCode}, fetch: func(_ context.Context, r FactorContext) FactorResult {
			if src.Errors["list"] {
				return unavailable("error", "list")
			}
			entries, ok := src.Lists[c.ListCode]
			if !ok {
				return unavailable("missing", "list")
			}
			hit := false
			for _, entry := range entries {
				if entry == dependency(r, c.SubjectCode).(string) {
					hit = true
				}
			}
			return resolved(hit, "list:"+c.ListCode, "demo-list-v1", r.AsOf())
		}}, nil
	})
}
func featureProvider(src *Sources) Provider {
	return providerFunc(func(d Definition) (Factor, error) {
		c, err := config[struct {
			SetKey, Version, FeatureKey, Window, SubjectCode string
			MaxAgeSeconds                                    int64
		}](d)
		if err != nil || c.SetKey != "subject_fund_activity" || c.Version != "2" || c.Window != "rolling_24h" || c.SubjectCode == "" || c.MaxAgeSeconds <= 0 || d.ValueType != "int" || (c.FeatureKey != "inflow_minor" && c.FeatureKey != "outflow_minor") {
			return nil, fmt.Errorf("invalid feature reference")
		}
		return boundFactor{code: d.Code, deps: []string{c.SubjectCode}, fetch: func(_ context.Context, r FactorContext) FactorResult {
			if src.Errors["feature"] {
				return unavailable("error", "feature")
			}
			v, ok := src.Features[dependency(r, c.SubjectCode).(string)]
			if !ok {
				return unavailable("missing", "feature")
			}
			if v.ObservedAt.After(r.AsOf()) || r.AsOf().Sub(v.ObservedAt) > time.Duration(c.MaxAgeSeconds)*time.Second {
				return unavailable("stale", "feature")
			}
			n := v.Inflow
			if c.FeatureKey == "outflow_minor" {
				n = v.Outflow
			}
			return resolved(n, "feature:"+c.SetKey+"/"+c.Window, v.Version, v.ObservedAt)
		}}, nil
	})
}

type Decision struct {
	Code        string
	Priority    int
	ReasonCodes []string
}
type ActionDefinition struct{ Type, TargetFactor string }
type Strategy struct {
	Code, Rule string
	Decision   Decision
	Actions    []ActionDefinition
}
type Event struct {
	Code       string
	Params     map[string]string
	Strategies []Strategy
}
type compiledStrategy struct {
	Strategy
	expression expression
}
type Snapshot struct {
	Event      Event
	Plan       []string
	factors    map[string]Factor
	strategies []compiledStrategy
}

func celType(kind string) *cel.Type {
	return map[string]*cel.Type{"int": cel.IntType, "double": cel.DoubleType, "bool": cel.BoolType, "string": cel.StringType}[kind]
}
func Compile(definitions []Definition, event Event, src *Sources) (*Snapshot, error) {
	defs := map[string]Definition{}
	opts := []cel.EnvOption{}
	for _, d := range definitions {
		if celType(d.ValueType) == nil || d.Code == "" {
			return nil, fmt.Errorf("invalid factor type")
		}
		if _, ok := defs[d.Code]; ok {
			return nil, fmt.Errorf("duplicate factor")
		}
		defs[d.Code] = d
		opts = append(opts, cel.Variable(d.Code, celType(d.ValueType)))
	}
	env, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, err
	}
	providers := map[string]Provider{"value": valueProvider(), "expression": expressionProvider(env, defs), "rpc": rpcProvider(src), "counter": counterProvider(src), "list": listProvider(src), "feature": featureProvider(src)}
	s := &Snapshot{Event: event, factors: map[string]Factor{}}
	for _, d := range definitions {
		p := providers[d.Type]
		if p == nil {
			return nil, fmt.Errorf("unknown provider")
		}
		f, err := p.NewFactor(d)
		if err != nil {
			return nil, fmt.Errorf("factor %s: %w", d.Code, err)
		}
		if d.Type == "rpc" || d.Type == "counter" || d.Type == "list" || d.Type == "feature" {
			for _, dep := range f.DependentFactorCodes() {
				if defs[dep].ValueType != "string" {
					return nil, fmt.Errorf("factor %s requires string subject %s", d.Code, dep)
				}
			}
		}
		s.factors[d.Code] = f
	}
	all := make([]string, 0, len(defs))
	for code := range defs {
		all = append(all, code)
	}
	if _, err := topological(s.factors, all); err != nil {
		return nil, err
	}
	roots := []string{}
	seenStrategies := map[string]bool{}
	for _, strategy := range event.Strategies {
		if strategy.Code == "" || seenStrategies[strategy.Code] || (strategy.Decision.Code != "pass" && strategy.Decision.Code != "block") {
			return nil, fmt.Errorf("invalid strategy")
		}
		seenStrategies[strategy.Code] = true
		e, err := compile(env, strategy.Rule, cel.BoolType, defs)
		if err != nil {
			return nil, err
		}
		roots = append(roots, e.dependencies...)
		for _, action := range strategy.Actions {
			if action.Type != "create_case" || defs[action.TargetFactor].ValueType != "string" {
				return nil, fmt.Errorf("invalid action")
			}
			roots = append(roots, action.TargetFactor)
		}
		s.strategies = append(s.strategies, compiledStrategy{strategy, e})
	}
	s.Plan, err = topological(s.factors, roots)
	return s, err
}
func topological(factors map[string]Factor, roots []string) ([]string, error) {
	marks := map[string]int{}
	order := []string{}
	var visit func(string) error
	visit = func(code string) error {
		if marks[code] == 1 {
			return fmt.Errorf("factor cycle: %s", code)
		}
		if marks[code] == 2 {
			return nil
		}
		f := factors[code]
		if f == nil {
			return fmt.Errorf("unknown factor: %s", code)
		}
		marks[code] = 1
		for _, dep := range f.DependentFactorCodes() {
			if err := visit(dep); err != nil {
				return err
			}
		}
		marks[code] = 2
		order = append(order, code)
		return nil
	}
	roots = append([]string(nil), roots...)
	sort.Strings(roots)
	for _, code := range roots {
		if err := visit(code); err != nil {
			return nil, err
		}
	}
	return order, nil
}

type Request struct {
	EventCode string         `json:"event_code"`
	EventID   string         `json:"event_id"`
	Params    map[string]any `json:"params"`
}
type ActionIntent struct{ ID, Type, Target string }
type Evaluation struct {
	ID, Status, SnapshotVersion, ErrorCode string
	AsOf                                   time.Time
	Decision                               Decision
	MatchedStrategies                      []string
	Evidence                               []FactorResult
	Actions                                []ActionIntent
}

func (s *Snapshot) Evaluate(ctx context.Context, req Request, asOf time.Time) Evaluation {
	id := digest(req.EventCode + "/" + req.EventID)
	out := Evaluation{ID: id, Status: "error", SnapshotVersion: "demo-snapshot-v1", AsOf: asOf}
	if req.EventCode != s.Event.Code || req.EventID == "" {
		out.ErrorCode = "invalid_request"
		return out
	}
	params := map[string]any{}
	for key, kind := range s.Event.Params {
		v, ok := req.Params[key]
		if !ok {
			out.ErrorCode = "missing_param:" + key
			return out
		}
		n, err := normalize(v, kind)
		if err != nil {
			out.ErrorCode = "invalid_param:" + key
			return out
		}
		if key == "amount_minor" && n.(int64) <= 0 {
			out.ErrorCode = "invalid_amount"
			return out
		}
		params[key] = n
	}
	for key := range req.Params {
		if _, ok := s.Event.Params[key]; !ok {
			out.ErrorCode = "unexpected_param:" + key
			return out
		}
	}
	state := &runtimeState{params: params, results: map[string]FactorResult{}, asOf: asOf}
	for _, code := range s.Plan {
		r := s.factors[code].Fetch(ctx, state)
		state.results[code] = r
		out.Evidence = append(out.Evidence, r)
		if r.Status != "resolved" {
			out.ErrorCode = code + ":" + r.Status
			return out
		}
	}
	candidates := []compiledStrategy{}
	for _, strategy := range s.strategies {
		vars := map[string]any{}
		for _, code := range strategy.expression.dependencies {
			vars[code] = state.results[code].Value
		}
		v, _, err := strategy.expression.program.ContextEval(ctx, vars)
		if err != nil {
			out.ErrorCode = "rule_eval_error"
			return out
		}
		if v.Value().(bool) {
			candidates = append(candidates, strategy)
			out.MatchedStrategies = append(out.MatchedStrategies, strategy.Code)
		}
	}
	out.Status = "completed"
	out.Decision = Decision{Code: "pass"}
	if len(candidates) > 0 {
		sort.Slice(candidates, func(i, j int) bool {
			a, b := candidates[i], candidates[j]
			if a.Decision.Priority != b.Decision.Priority {
				return a.Decision.Priority > b.Decision.Priority
			}
			return a.Code < b.Code
		})
		winner := candidates[0]
		out.Decision = winner.Decision
		for _, action := range winner.Actions {
			target := state.results[action.TargetFactor].Value.(string)
			out.Actions = append(out.Actions, ActionIntent{digest(out.ID + "/" + winner.Code + "/" + action.Type + "/" + target), action.Type, target})
		}
	}
	return out
}
func digest(value string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))[:20] }

type record struct {
	fingerprint string
	evaluation  Evaluation
}
type execution struct {
	Intent   ActionIntent
	Status   string
	Attempts int
}
type Service struct {
	Snapshots map[string]*Snapshot
	Records   map[string]record
	Outbox    map[string]*execution
}

func cloneEvaluation(e Evaluation) Evaluation {
	// The demo only exposes scalar factor values. Object-valued production factors
	// need immutable typed snapshots or schema-aware defensive copying as well.
	e.Evidence = append([]FactorResult(nil), e.Evidence...)
	e.MatchedStrategies = append([]string(nil), e.MatchedStrategies...)
	e.Decision.ReasonCodes = append([]string(nil), e.Decision.ReasonCodes...)
	e.Actions = append([]ActionIntent(nil), e.Actions...)
	return e
}
func (s *Service) Evaluate(req Request, asOf time.Time) (Evaluation, error) {
	// One trusted demo caller; a real key must include the authenticated caller.
	key := "demo-caller/" + req.EventCode + "/" + req.EventID
	body, err := json.Marshal(req)
	if err != nil {
		return Evaluation{}, err
	}
	if old, ok := s.Records[key]; ok {
		if old.fingerprint != string(body) {
			return Evaluation{}, fmt.Errorf("idempotency conflict")
		}
		return cloneEvaluation(old.evaluation), nil
	}
	snapshot := s.Snapshots[req.EventCode]
	if snapshot == nil {
		return Evaluation{}, fmt.Errorf("unknown event")
	}
	result := snapshot.Evaluate(context.Background(), req, asOf)
	// In-memory single-threaded stand-in for Evaluation + Evidence + Outbox commit.
	s.Records[key] = record{string(body), cloneEvaluation(result)}
	for _, action := range result.Actions {
		s.Outbox[action.ID] = &execution{Intent: action, Status: "pending"}
	}
	return cloneEvaluation(result), nil
}

type CaseReceiver struct {
	Created       map[string]string
	LoseNextReply bool
}

func (r *CaseReceiver) Create(action ActionIntent) error {
	if _, ok := r.Created[action.ID]; !ok {
		r.Created[action.ID] = "case:" + action.ID
	}
	if r.LoseNextReply {
		r.LoseNextReply = false
		return fmt.Errorf("reply lost after case creation")
	}
	return nil
}
func (s *Service) Deliver(actionID string, receiver *CaseReceiver) error {
	job := s.Outbox[actionID]
	if job == nil {
		return fmt.Errorf("unknown action")
	}
	if job.Status == "succeeded" {
		return nil
	}
	job.Attempts++
	if err := receiver.Create(job.Intent); err != nil {
		job.Status = "pending"
		return err
	}
	job.Status = "succeeded"
	return nil
}
