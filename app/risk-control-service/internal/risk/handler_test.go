package risk

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeStore struct {
	facts      CustomerFacts
	evaluation Evaluation
	check      RestrictionCheck
	offlineRun OfflineRunResult
	replay     bool
	err        error
}

type runtimeDeciderStub struct{ runtime *PolicySnapshotRuntime }

func (s runtimeDeciderStub) Decide(
	ctx context.Context,
	command RealtimeDecisionCommand,
) (DurableRealtimeDecision, bool, error) {
	evaluation, err := s.runtime.Evaluate(ctx, command.Params)
	return DurableRealtimeDecision{
		EvaluationID:     command.EvaluationID,
		SubjectType:      command.SubjectType,
		SubjectID:        command.SubjectID,
		PolicyEvaluation: evaluation,
	}, false, err
}

func (s *fakeStore) UpsertFacts(_ context.Context, f CustomerFacts) (CustomerFacts, error) {
	s.facts = f
	s.facts.FactVersion = 1
	return s.facts, s.err
}
func (s *fakeStore) Evaluate(_ context.Context, c EvaluationCommand) (Evaluation, bool, error) {
	return s.evaluation, s.replay, s.err
}
func (s *fakeStore) GetEvaluation(_ context.Context, id string) (Evaluation, error) {
	return s.evaluation, s.err
}
func (s *fakeStore) CompleteAction(_ context.Context, id string, c CompleteActionCommand) (Action, error) {
	return Action{ActionID: id, Status: "completed"}, s.err
}
func (s *fakeStore) CheckRestriction(_ context.Context, cmd RestrictionCheckCommand) (RestrictionCheck, error) {
	return s.check, s.err
}
func (s *fakeStore) GetOfflineRun(_ context.Context, id string) (OfflineRunResult, error) {
	return s.offlineRun, s.err
}

func TestPublicHTTPContract(t *testing.T) {
	store := &fakeStore{evaluation: Evaluation{EvaluationID: "eval-1", Decision: "review", Actions: []Action{{ActionID: "action-eval-1-provide-poa", ActionType: "provide_poa", Status: "open"}}, EvaluatedAt: time.Unix(1, 0)}}
	h := NewHandler(store)
	t.Run("records trusted customer facts", func(t *testing.T) {
		body := `{"verification_status":"approved","residence_country":"SG","poa_verified":false}`
		r := httptest.NewRequest(http.MethodPut, "/api/v1/customers/customer-1/facts", bytes.NewBufferString(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || store.facts.CustomerID != "customer-1" {
			t.Fatalf("status=%d facts=%+v", w.Code, store.facts)
		}
	})
	t.Run("returns review with a durable action", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/evaluations", bytes.NewBufferString(`{"evaluation_id":"eval-1","customer_id":"customer-1","product_code":"business_card"}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got Evaluation
		_ = json.NewDecoder(w.Body).Decode(&got)
		if w.Code != 200 || got.Decision != "review" || len(got.Actions) != 1 {
			t.Fatalf("status=%d got=%+v", w.Code, got)
		}
	})
	t.Run("marks exact replay", func(t *testing.T) {
		store.replay = true
		r := httptest.NewRequest(http.MethodPost, "/api/v1/evaluations", bytes.NewBufferString(`{"evaluation_id":"eval-1","customer_id":"customer-1","product_code":"business_card"}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("Idempotent-Replay") != "true" {
			t.Fatalf("missing replay header")
		}
	})
	t.Run("denies withdrawal when an offline restriction is active", func(t *testing.T) {
		store.check = RestrictionCheck{
			SubjectType: "organization", SubjectID: "org-risky", Capability: "withdrawal",
			Decision: "deny", ReasonCodes: []string{"offline_restriction_active"},
			RestrictionID: "restriction-1", FindingID: "finding-1",
		}
		r := httptest.NewRequest(http.MethodPost, "/api/v1/withdrawal-checks", bytes.NewBufferString(`{"subject_type":"organization","subject_id":"org-risky"}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got RestrictionCheck
		_ = json.NewDecoder(w.Body).Decode(&got)
		if w.Code != http.StatusOK || got.Decision != "deny" || got.RestrictionID != "restriction-1" {
			t.Fatalf("status=%d got=%+v", w.Code, got)
		}
	})
	t.Run("returns an auditable offline run", func(t *testing.T) {
		store.offlineRun = OfflineRunResult{RunID: "offline-run-1", Status: "completed", PolicyKey: "rapid_fund_movement", PolicyVersion: 1, SubjectsEvaluated: 2, FindingsCreated: 1, RestrictionsActivated: 1}
		r := httptest.NewRequest(http.MethodGet, "/api/v1/offline-runs/offline-run-1", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var got OfflineRunResult
		_ = json.NewDecoder(w.Body).Decode(&got)
		if w.Code != http.StatusOK || got.RunID != "offline-run-1" || got.FindingsCreated != 1 {
			t.Fatalf("status=%d got=%+v", w.Code, got)
		}
	})
}

func TestPolicyDecisionHTTPUsesActiveCompiledSnapshot(t *testing.T) {
	factors := []FactorDefinition{{
		Key: "amount_minor", Type: FactorTypeValue,
		ValueType: FactorValueTypeInt, ParamKey: "amount_minor",
	}}
	snapshot, err := CompilePolicySnapshot(ExecutablePolicyDefinition{
		PolicyKey: "withdrawal_risk",
		Version:   7,
		DefaultDecision: DecisionTemplate{
			Decision: "pass", ReasonCodes: []string{"amount_within_limit"},
		},
		Strategies: []ExecutableStrategyDefinition{{
			StrategyKey: "block_large_withdrawal",
			Priority:    100,
			Rule: CELRule{
				RuleKey: "large_withdrawal", Version: 3,
				Expression: "amount_minor >= 100000",
			},
			Effect: StrategyEffect{
				Decision: DecisionTemplate{
					Decision: "block", ReasonCodes: []string{"large_withdrawal"},
				},
				Actions: []PolicyAction{{
					ActionCode: "create_case", Params: map[string]any{"queue": "risk_review"},
				}},
			},
		}},
	}, factors, NewRPCFactorAdapterRegistry())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewPolicySnapshotRuntime(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandlerWithRealtimeDecider(&fakeStore{}, runtimeDeciderStub{runtime: runtime})

	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/policy-decisions",
		bytes.NewBufferString(`{"evaluation_id":"decision-1","subject_type":"organization","subject_id":"org-1","params":{"amount_minor":150000}}`),
	)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var got PolicyEvaluation
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || got.PolicyKey != "withdrawal_risk" || got.PolicyVersion != 7 || got.Decision != "block" {
		t.Fatalf("status=%d got=%+v", w.Code, got)
	}
	if got.MatchedRuleKey != "large_withdrawal" || len(got.Actions) != 1 || got.Actions[0].ActionCode != "create_case" {
		t.Fatalf("missing rule trace or action: %+v", got)
	}

	next, err := CompilePolicySnapshot(ExecutablePolicyDefinition{
		PolicyKey: "withdrawal_risk",
		Version:   8,
		DefaultDecision: DecisionTemplate{
			Decision: "pass", ReasonCodes: []string{"amount_within_limit"},
		},
		Strategies: []ExecutableStrategyDefinition{{
			StrategyKey: "block_very_large_withdrawal",
			Priority:    100,
			Rule: CELRule{
				RuleKey: "very_large_withdrawal", Version: 4,
				Expression: "amount_minor >= 200000",
			},
			Effect: StrategyEffect{Decision: DecisionTemplate{
				Decision: "block", ReasonCodes: []string{"very_large_withdrawal"},
			}},
		}},
	}, factors, NewRPCFactorAdapterRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Activate(next); err != nil {
		t.Fatal(err)
	}

	r = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/policy-decisions",
		bytes.NewBufferString(`{"evaluation_id":"decision-2","subject_type":"organization","subject_id":"org-1","params":{"amount_minor":150000}}`),
	)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	got = PolicyEvaluation{}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || got.PolicyVersion != 8 || got.Decision != "pass" || len(got.Actions) != 0 {
		t.Fatalf("hot-switched decision: status=%d got=%+v", w.Code, got)
	}
}
