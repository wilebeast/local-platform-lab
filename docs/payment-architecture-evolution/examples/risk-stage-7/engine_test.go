package riskdemo

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type scenario struct {
	AsOf          time.Time `json:"as_of"`
	Sources       Sources   `json:"sources"`
	Requests      []Request `json:"requests"`
	CardAvailable int64     `json:"card_available_minor"`
}

func load(t *testing.T) *scenario {
	t.Helper()
	f, err := os.Open("scenario.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var result scenario
	decoder := json.NewDecoder(f)
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	result.Sources.Errors = map[string]bool{}
	return &result
}
func def(code, kind, valueType string, cfg any) Definition {
	raw, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	return Definition{code, kind, valueType, raw}
}
func definitions() []Definition {
	value := func(code, param, valueType string) Definition {
		return def(code, "value", valueType, map[string]any{"ParamKey": param})
	}
	list := func(code, list, subject string) Definition {
		return def(code, "list", "bool", map[string]any{"ListCode": list, "SubjectCode": subject})
	}
	feature := func(code, key string) Definition {
		return def(code, "feature", "int", map[string]any{"SetKey": "subject_fund_activity", "Version": "2", "FeatureKey": key, "Window": "rolling_24h", "SubjectCode": "organization_id", "MaxAgeSeconds": int64(300)})
	}
	return []Definition{
		value("amount_minor", "amount_minor", "int"), value("merchant_id", "merchant_id", "string"),
		value("organization_id", "organization_id", "string"), value("beneficiary_id", "beneficiary_id", "string"),
		value("cardholder_id", "cardholder_id", "string"), value("unused_value", "not_requested", "int"),
		list("merchant_listed", "restricted_merchants", "merchant_id"),
		list("beneficiary_listed", "restricted_beneficiaries", "beneficiary_id"),
		list("cardholder_listed", "restricted_cardholders", "cardholder_id"),
		def("account_blocked", "rpc", "bool", map[string]any{"Service": "account-service", "Method": "GetRiskState", "SubjectCode": "organization_id"}),
		def("withdrawal_attempts_5m", "counter", "int", map[string]any{"SubjectCode": "organization_id", "Window": "rolling_5m"}),
		def("card_attempts_5m", "counter", "int", map[string]any{"SubjectCode": "cardholder_id", "Window": "rolling_5m"}),
		feature("inflow_24h", "inflow_minor"), feature("outflow_24h", "outflow_minor"),
		def("outflow_ratio_24h", "expression", "double", map[string]any{"Expression": "double(outflow_24h) / double(inflow_24h)"}),
	}
}
func events() []Event {
	block := func(code, rule string, priority int, reason, target string) Strategy {
		actions := []ActionDefinition{}
		if target != "" {
			actions = append(actions, ActionDefinition{"create_case", target})
		}
		return Strategy{code, rule, Decision{"block", priority, []string{reason}}, actions}
	}
	return []Event{
		{Code: "acquiring.payment.submit", Params: map[string]string{"merchant_id": "string", "amount_minor": "int"}, Strategies: []Strategy{
			block("restricted_merchant", "merchant_listed", 100, "merchant_restricted", ""),
			block("large_payment", "amount_minor >= 100000", 50, "payment_amount_limit", "")}},
		{Code: "withdrawal.submit", Params: map[string]string{"organization_id": "string", "beneficiary_id": "string", "amount_minor": "int"}, Strategies: []Strategy{
			block("restricted_beneficiary", "beneficiary_listed", 100, "beneficiary_restricted", "organization_id"),
			block("restricted_account", "account_blocked", 95, "account_restricted", "organization_id"),
			block("rapid_outflow", "amount_minor >= 50000 && inflow_24h >= 50000 && outflow_ratio_24h >= 0.90", 80, "rapid_outflow", "organization_id"),
			block("frequent_withdrawals", "withdrawal_attempts_5m >= 5", 60, "frequent_withdrawals", "organization_id")}},
		{Code: "card.authorization.requested", Params: map[string]string{"cardholder_id": "string", "amount_minor": "int"}, Strategies: []Strategy{
			block("restricted_cardholder", "cardholder_listed", 100, "cardholder_restricted", ""),
			block("frequent_card_attempts", "card_attempts_5m >= 5", 60, "frequent_card_attempts", "")}},
	}
}
func service(t *testing.T, fixture *scenario) *Service {
	t.Helper()
	s := &Service{Snapshots: map[string]*Snapshot{}, Records: map[string]record{}, Outbox: map[string]*execution{}}
	for _, event := range events() {
		snapshot, err := Compile(definitions(), event, &fixture.Sources)
		if err != nil {
			t.Fatal(err)
		}
		s.Snapshots[event.Code] = snapshot
	}
	return s
}
func evaluate(t *testing.T, s *Service, req Request, asOf time.Time) Evaluation {
	t.Helper()
	result, err := s.Evaluate(req, asOf)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestThreeBusinessEvents(t *testing.T) {
	f := load(t)
	s := service(t, f)
	for i, want := range []string{"block", "block", "pass"} {
		r := evaluate(t, s, f.Requests[i], f.AsOf)
		if r.Status != "completed" || r.Decision.Code != want {
			t.Fatalf("%+v", r)
		}
		t.Logf("event=%s decision=%s reasons=%v plan=%v", f.Requests[i].EventCode, r.Decision.Code, r.Decision.ReasonCodes, s.Snapshots[f.Requests[i].EventCode].Plan)
		seen := map[string]bool{}
		for _, e := range r.Evidence {
			if seen[e.Code] {
				t.Fatal("factor fetched twice")
			}
			seen[e.Code] = true
			t.Logf("factor=%s value=%v status=%s source=%s version=%s", e.Code, e.Value, e.Status, e.Source, e.SourceVersion)
		}
		if seen["unused_value"] {
			t.Fatal("unreferenced factor fetched")
		}
		if i == 0 && (seen["account_blocked"] || seen["inflow_24h"]) {
			t.Fatal("payment loaded unrelated factors")
		}
		if i == 2 && (seen["organization_id"] || seen["inflow_24h"]) {
			t.Fatal("card loaded unrelated factors")
		}
	}
	withdrawal := evaluate(t, s, f.Requests[1], f.AsOf)
	if len(withdrawal.Actions) != 1 || withdrawal.Actions[0].Type != "create_case" {
		t.Fatal("missing case intent")
	}
	card := evaluate(t, s, f.Requests[2], f.AsOf)
	requested, err := f.Requests[2].Params["amount_minor"].(json.Number).Int64()
	if err != nil {
		t.Fatal(err)
	}
	// The card business owns the atomic balance/reservation check; risk pass is insufficient.
	canAuthorize := card.Decision.Code == "pass" && f.CardAvailable >= requested
	if canAuthorize {
		t.Fatal("risk pass bypassed business funds check")
	}
	t.Log("card risk=pass, business authorization=declined: insufficient_funds")
}
func TestInvalidConfiguration(t *testing.T) {
	f := load(t)
	base := events()[0]
	for name, rule := range map[string]string{"unknown_factor": "not_registered > 0", "non_boolean": "amount_minor + 1", "type_mismatch": "merchant_id >= 100"} {
		t.Run(name, func(t *testing.T) {
			e := base
			e.Strategies = []Strategy{{Code: "invalid", Rule: rule, Decision: Decision{Code: "block"}}}
			if _, err := Compile(definitions(), e, &f.Sources); err == nil {
				t.Fatal("invalid expression accepted")
			} else {
				t.Log(err)
			}
		})
	}
	t.Run("cycle", func(t *testing.T) {
		defs := definitions()
		defs = append(defs, def("cycle_a", "expression", "int", map[string]string{"Expression": "cycle_b + 1"}), def("cycle_b", "expression", "int", map[string]string{"Expression": "cycle_a + 1"}))
		if _, err := Compile(defs, base, &f.Sources); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Fatalf("expected cycle, got %v", err)
		}
	})
}
func TestFactorFailureIsNotPass(t *testing.T) {
	for _, mode := range []string{"stale", "rpc_error", "missing", "zero_denominator"} {
		t.Run(mode, func(t *testing.T) {
			f := load(t)
			switch mode {
			case "stale":
				v := f.Sources.Features["org-demo-01"]
				v.ObservedAt = f.AsOf.Add(-10 * time.Minute)
				f.Sources.Features["org-demo-01"] = v
			case "rpc_error":
				f.Sources.Errors["rpc"] = true
			case "missing":
				delete(f.Sources.Features, "org-demo-01")
			case "zero_denominator":
				v := f.Sources.Features["org-demo-01"]
				v.Inflow = 0
				f.Sources.Features["org-demo-01"] = v
			}
			s := service(t, f)
			r := evaluate(t, s, f.Requests[1], f.AsOf)
			if r.Status != "error" || r.Decision.Code != "" || len(r.Actions) != 0 {
				t.Fatalf("failure became ordinary decision: %+v", r)
			}
			t.Logf("status=%s error=%s actions=%d", r.Status, r.ErrorCode, len(r.Actions))
		})
	}
}
func TestValueAndCounterIsolation(t *testing.T) {
	f := load(t)
	s := service(t, f)
	before := f.Sources.Counters["org-demo-01"]
	first := evaluate(t, s, f.Requests[1], f.AsOf)
	secondRequest := f.Requests[1]
	secondRequest.EventID = "withdrawal-demo-002"
	secondRequest.Params = map[string]any{}
	for k, v := range f.Requests[1].Params {
		secondRequest.Params[k] = v
	}
	secondRequest.Params["amount_minor"] = json.Number("60000")
	second := evaluate(t, s, secondRequest, f.AsOf)
	value := func(e Evaluation, code string) any {
		for _, r := range e.Evidence {
			if r.Code == code {
				return r.Value
			}
		}
		return nil
	}
	if value(first, "amount_minor") != int64(50000) || value(second, "amount_minor") != int64(60000) {
		t.Fatal("evaluation state leaked")
	}
	if f.Sources.Counters["org-demo-01"] != before {
		t.Fatal("counter fetch performed an increment")
	}
	first.Evidence[0].Status = "mutated_by_caller"
	again := evaluate(t, s, f.Requests[1], f.AsOf)
	if again.Evidence[0].Status == "mutated_by_caller" {
		t.Fatal("caller mutated stored evidence")
	}
}
func TestActionAcknowledgementLoss(t *testing.T) {
	f := load(t)
	s := service(t, f)
	original := evaluate(t, s, f.Requests[1], f.AsOf)
	saved, _ := json.Marshal(original)
	receiver := &CaseReceiver{Created: map[string]string{}, LoseNextReply: true}
	id := original.Actions[0].ID
	if err := s.Deliver(id, receiver); err == nil {
		t.Fatal("expected reply loss")
	}
	if len(receiver.Created) != 1 || s.Outbox[id].Status != "pending" {
		t.Fatal("incorrect partial outcome")
	}
	if err := s.Deliver(id, receiver); err != nil {
		t.Fatal(err)
	}
	if err := s.Deliver(id, receiver); err != nil {
		t.Fatal(err)
	}
	if len(receiver.Created) != 1 || s.Outbox[id].Attempts != 2 || s.Outbox[id].Status != "succeeded" {
		t.Fatal("duplicate side effect")
	}
	again := evaluate(t, s, f.Requests[1], f.AsOf)
	after, _ := json.Marshal(again)
	if string(saved) != string(after) {
		t.Fatal("action failure changed original decision/evidence")
	}
	t.Logf("decision=%s cases=%d execution=%s attempts=%d", again.Decision.Code, len(receiver.Created), s.Outbox[id].Status, s.Outbox[id].Attempts)
}
func TestRequestReplayConflictAndDecisionAggregation(t *testing.T) {
	f := load(t)
	s := service(t, f)
	original := evaluate(t, s, f.Requests[1], f.AsOf)
	again := evaluate(t, s, f.Requests[1], f.AsOf)
	if !reflect.DeepEqual(original, again) || len(s.Outbox) != 1 {
		t.Fatal("request replay duplicated output")
	}
	conflict := f.Requests[1]
	conflict.Params = map[string]any{"organization_id": "org-demo-01", "beneficiary_id": "beneficiary-demo-01", "amount_minor": json.Number("50001")}
	if _, err := s.Evaluate(conflict, f.AsOf); err == nil {
		t.Fatal("same request accepted changed content")
	}
	f2 := load(t)
	f2.Sources.Lists["restricted_beneficiaries"] = []string{"beneficiary-demo-01"}
	s2 := service(t, f2)
	result := evaluate(t, s2, f2.Requests[1], f2.AsOf)
	if len(result.MatchedStrategies) != 2 || result.Decision.Priority != 100 || result.Decision.ReasonCodes[0] != "beneficiary_restricted" || len(result.Actions) != 1 {
		t.Fatalf("wrong aggregation: %+v", result)
	}
	t.Logf("matched=%v winning_decision=%s priority=%d reason=%v actions=%d", result.MatchedStrategies, result.Decision.Code, result.Decision.Priority, result.Decision.ReasonCodes, len(result.Actions))
}
