package risk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"time"
)

type ActivityEvent struct {
	EventID     string    `json:"event_id"`
	SubjectType string    `json:"subject_type"`
	SubjectID   string    `json:"subject_id"`
	EventType   string    `json:"event_type"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type OfflineRunCommand struct {
	RunID         string    `json:"run_id"`
	AsOf          time.Time `json:"as_of"`
	PolicyKey     string    `json:"policy_key"`
	PolicyVersion int       `json:"policy_version"`
}

type OfflinePolicy struct {
	PolicyKey    string        `json:"policy_key"`
	Version      int           `json:"policy_version"`
	Status       string        `json:"status"`
	Window       time.Duration `json:"window"`
	MinInflow    int64         `json:"min_inflow_minor"`
	OutflowRatio float64       `json:"outflow_ratio"`
	Capability   string        `json:"capability"`
}

type OfflineFeatures struct {
	InflowMinor  int64   `json:"inflow_minor"`
	OutflowMinor int64   `json:"outflow_minor"`
	OutflowRatio float64 `json:"outflow_ratio"`
	Currency     string  `json:"currency"`
}

type RiskFinding struct {
	FindingID     string    `json:"finding_id"`
	SubjectType   string    `json:"subject_type"`
	SubjectID     string    `json:"subject_id"`
	RiskType      string    `json:"risk_type"`
	Severity      string    `json:"severity"`
	PolicyKey     string    `json:"policy_key"`
	PolicyVersion int       `json:"policy_version"`
	WindowStart   time.Time `json:"window_start"`
	WindowEnd     time.Time `json:"window_end"`
	Status        string    `json:"status"`
}

type RiskRestriction struct {
	RestrictionID string `json:"restriction_id"`
	FindingID     string `json:"finding_id"`
	SubjectType   string `json:"subject_type"`
	SubjectID     string `json:"subject_id"`
	Capability    string `json:"capability"`
	Status        string `json:"status"`
}

type OfflineEvaluation struct {
	SubjectType        string           `json:"subject_type"`
	SubjectID          string           `json:"subject_id"`
	Decision           string           `json:"decision"`
	ReasonCodes        []string         `json:"reason_codes"`
	MatchedStrategyKey string           `json:"matched_strategy_key,omitempty"`
	MatchedRuleKey     string           `json:"matched_rule_key,omitempty"`
	Features           OfflineFeatures  `json:"features"`
	Finding            *RiskFinding     `json:"finding,omitempty"`
	Restriction        *RiskRestriction `json:"restriction,omitempty"`
}

type OfflineRunResult struct {
	RunID                 string              `json:"run_id"`
	Status                string              `json:"status"`
	AsOf                  time.Time           `json:"as_of"`
	WindowStart           time.Time           `json:"window_start"`
	WindowEnd             time.Time           `json:"window_end"`
	PolicyKey             string              `json:"policy_key"`
	PolicyVersion         int                 `json:"policy_version"`
	SubjectsEvaluated     int                 `json:"subjects_evaluated"`
	FindingsCreated       int                 `json:"findings_created"`
	RestrictionsActivated int                 `json:"restrictions_activated"`
	Evaluations           []OfflineEvaluation `json:"evaluations"`
	CommandFingerprint    string              `json:"-"`
}

type OfflineStore interface {
	LoadOfflinePolicy(context.Context, string, int) (OfflinePolicy, error)
	LoadActivityEvents(context.Context, time.Time, time.Time) ([]ActivityEvent, error)
	SaveOfflineRun(context.Context, OfflineRunResult) (OfflineRunResult, bool, error)
}

type OfflineScanner struct{ store OfflineStore }

func NewOfflineScanner(store OfflineStore) *OfflineScanner { return &OfflineScanner{store: store} }

func (s *OfflineScanner) Run(ctx context.Context, cmd OfflineRunCommand) (OfflineRunResult, bool, error) {
	if cmd.RunID == "" || cmd.AsOf.IsZero() || cmd.PolicyKey == "" || cmd.PolicyVersion <= 0 {
		return OfflineRunResult{}, false, fmt.Errorf("run_id, as_of, policy_key and policy_version are required")
	}
	policy, err := s.store.LoadOfflinePolicy(ctx, cmd.PolicyKey, cmd.PolicyVersion)
	if err != nil {
		return OfflineRunResult{}, false, err
	}
	if policy.Status != "published" || policy.Window <= 0 || policy.Capability == "" {
		return OfflineRunResult{}, false, fmt.Errorf("offline policy is not executable")
	}
	compiledPolicy, err := compileOfflinePolicy(policy)
	if err != nil {
		return OfflineRunResult{}, false, err
	}
	windowEnd := cmd.AsOf.UTC()
	windowStart := windowEnd.Add(-policy.Window)
	events, err := s.store.LoadActivityEvents(ctx, windowStart, windowEnd)
	if err != nil {
		return OfflineRunResult{}, false, err
	}

	type subjectKey struct{ subjectType, subjectID, currency string }
	featuresBySubject := make(map[subjectKey]OfflineFeatures)
	for _, event := range events {
		key := subjectKey{event.SubjectType, event.SubjectID, event.Currency}
		features := featuresBySubject[key]
		features.Currency = event.Currency
		switch event.EventType {
		case "funds_in":
			features.InflowMinor += event.AmountMinor
		case "funds_out":
			features.OutflowMinor += event.AmountMinor
		}
		featuresBySubject[key] = features
	}

	keys := make([]subjectKey, 0, len(featuresBySubject))
	for key := range featuresBySubject {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].subjectType != keys[j].subjectType {
			return keys[i].subjectType < keys[j].subjectType
		}
		if keys[i].subjectID != keys[j].subjectID {
			return keys[i].subjectID < keys[j].subjectID
		}
		return keys[i].currency < keys[j].currency
	})

	result := OfflineRunResult{
		RunID: cmd.RunID, Status: "completed", AsOf: windowEnd,
		WindowStart: windowStart, WindowEnd: windowEnd,
		PolicyKey: cmd.PolicyKey, PolicyVersion: cmd.PolicyVersion,
		Evaluations: make([]OfflineEvaluation, 0, len(keys)),
	}
	result.CommandFingerprint = offlineCommandFingerprint(cmd)
	for _, key := range keys {
		features := featuresBySubject[key]
		policyEvaluation, err := compiledPolicy.Evaluate(ctx, map[string]any{
			"inflow_minor_24h":  features.InflowMinor,
			"outflow_minor_24h": features.OutflowMinor,
		})
		if err != nil {
			return OfflineRunResult{}, false, fmt.Errorf("evaluate offline policy for %s/%s: %w", key.subjectType, key.subjectID, err)
		}
		factorValues := policyEvaluation.Factors.Values()
		features.OutflowRatio = factorValues["outflow_ratio_24h"].(float64)
		evaluation := OfflineEvaluation{
			SubjectType: key.subjectType, SubjectID: key.subjectID,
			Decision: policyEvaluation.Decision, ReasonCodes: policyEvaluation.ReasonCodes,
			MatchedStrategyKey: policyEvaluation.MatchedStrategyKey,
			MatchedRuleKey:     policyEvaluation.MatchedRuleKey, Features: features,
		}
		if restrictionAction, ok := findPolicyAction(policyEvaluation.Actions, "activate_restriction"); ok {
			capability, ok := restrictionAction.Params["capability"].(string)
			if !ok || capability == "" {
				return OfflineRunResult{}, false, fmt.Errorf("activate_restriction action requires capability")
			}
			findingID := offlineStableID(cmd.RunID, cmd.PolicyKey, key.subjectType, key.subjectID, key.currency, windowStart.Format(time.RFC3339), windowEnd.Format(time.RFC3339), fmt.Sprint(cmd.PolicyVersion))
			restrictionID := offlineStableID(key.subjectType, key.subjectID, capability)
			finding := &RiskFinding{FindingID: "finding-" + findingID, SubjectType: key.subjectType, SubjectID: key.subjectID, RiskType: cmd.PolicyKey, Severity: "high", PolicyKey: cmd.PolicyKey, PolicyVersion: cmd.PolicyVersion, WindowStart: windowStart, WindowEnd: windowEnd, Status: "open"}
			restriction := &RiskRestriction{RestrictionID: "restriction-" + restrictionID, FindingID: finding.FindingID, SubjectType: key.subjectType, SubjectID: key.subjectID, Capability: capability, Status: "active"}
			evaluation.Finding = finding
			evaluation.Restriction = restriction
			result.FindingsCreated++
			result.RestrictionsActivated++
		}
		result.Evaluations = append(result.Evaluations, evaluation)
	}
	result.SubjectsEvaluated = len(result.Evaluations)
	return s.store.SaveOfflineRun(ctx, result)
}

func compileOfflinePolicy(policy OfflinePolicy) (*CompiledPolicySnapshot, error) {
	factorDefinitions := []FactorDefinition{
		{Key: "inflow_minor_24h", Type: FactorTypeValue, ValueType: FactorValueTypeInt, ParamKey: "inflow_minor_24h"},
		{Key: "outflow_minor_24h", Type: FactorTypeValue, ValueType: FactorValueTypeInt, ParamKey: "outflow_minor_24h"},
		{
			Key: "outflow_ratio_24h", Type: FactorTypeExpression, ValueType: FactorValueTypeDouble,
			Expression: "inflow_minor_24h == 0 ? 0.0 : double(outflow_minor_24h) / double(inflow_minor_24h)",
		},
	}
	ruleExpression := fmt.Sprintf(
		"inflow_minor_24h >= %d && outflow_ratio_24h >= %s",
		policy.MinInflow,
		strconv.FormatFloat(policy.OutflowRatio, 'g', -1, 64),
	)
	return CompilePolicySnapshot(
		ExecutablePolicyDefinition{
			PolicyKey: policy.PolicyKey,
			Version:   policy.Version,
			DefaultDecision: DecisionTemplate{
				Decision: "pass", ReasonCodes: []string{"policy_not_matched"},
			},
			Strategies: []ExecutableStrategyDefinition{{
				StrategyKey: "block_" + policy.PolicyKey,
				Rule: CELRule{
					RuleKey: policy.PolicyKey, Version: policy.Version, Expression: ruleExpression,
				},
				Priority: 100,
				Effect: StrategyEffect{
					Decision: DecisionTemplate{
						Decision: "block", ReasonCodes: []string{"rapid_fund_movement_detected"},
					},
					Actions: []PolicyAction{{
						ActionCode: "activate_restriction",
						Params:     map[string]any{"capability": policy.Capability},
					}},
				},
			}},
		},
		factorDefinitions,
		NewRPCFactorAdapterRegistry(),
	)
}

func findPolicyAction(actions []PolicyAction, actionCode string) (PolicyAction, bool) {
	for _, action := range actions {
		if action.ActionCode == actionCode {
			return action, true
		}
	}
	return PolicyAction{}, false
}

func offlineStableID(parts ...string) string {
	sum := sha256.Sum256([]byte(fmt.Sprint(parts)))
	return hex.EncodeToString(sum[:12])
}

func offlineCommandFingerprint(cmd OfflineRunCommand) string {
	return offlineStableID(cmd.RunID, cmd.AsOf.UTC().Format(time.RFC3339Nano), cmd.PolicyKey, fmt.Sprint(cmd.PolicyVersion))
}
