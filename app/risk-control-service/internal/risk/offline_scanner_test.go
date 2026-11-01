package risk

import (
	"context"
	"testing"
	"time"
)

type offlineStoreStub struct {
	events      []ActivityEvent
	policy      OfflinePolicy
	savedResult OfflineRunResult
}

func (s *offlineStoreStub) LoadActivityEvents(context.Context, time.Time, time.Time) ([]ActivityEvent, error) {
	return s.events, nil
}

func (s *offlineStoreStub) LoadOfflinePolicy(context.Context, string, int) (OfflinePolicy, error) {
	return s.policy, nil
}

func (s *offlineStoreStub) SaveOfflineRun(_ context.Context, result OfflineRunResult) (OfflineRunResult, bool, error) {
	s.savedResult = result
	return result, false, nil
}

func TestOfflineScannerCreatesFindingAndWithdrawalRestriction(t *testing.T) {
	asOf := time.Date(2026, 10, 29, 2, 0, 0, 0, time.UTC)
	store := &offlineStoreStub{policy: OfflinePolicy{PolicyKey: "rapid_fund_movement", Version: 1, Status: "published", Window: 24 * time.Hour, MinInflow: 50_000, OutflowRatio: 0.90, Capability: "withdrawal"}, events: []ActivityEvent{
		{EventID: "in-1", SubjectType: "organization", SubjectID: "org-risky", EventType: "funds_in", AmountMinor: 100_000, Currency: "USD", OccurredAt: asOf.Add(-4 * time.Hour)},
		{EventID: "out-1", SubjectType: "organization", SubjectID: "org-risky", EventType: "funds_out", AmountMinor: 95_000, Currency: "USD", OccurredAt: asOf.Add(-time.Hour)},
	}}
	scanner := NewOfflineScanner(store)

	result, replay, err := scanner.Run(context.Background(), OfflineRunCommand{
		RunID:         "offline-run-20261029T020000Z-v1",
		AsOf:          asOf,
		PolicyKey:     "rapid_fund_movement",
		PolicyVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replay {
		t.Fatal("first run must not be an idempotent replay")
	}
	if result.Status != "completed" || result.SubjectsEvaluated != 1 || result.FindingsCreated != 1 || result.RestrictionsActivated != 1 {
		t.Fatalf("unexpected run result: %+v", result)
	}
	if len(result.Evaluations) != 1 {
		t.Fatalf("evaluations=%d, want 1", len(result.Evaluations))
	}
	evaluation := result.Evaluations[0]
	t.Logf("decision=%s matched_strategy=%s matched_rule=%s inflow=%d outflow=%d ratio=%.2f", evaluation.Decision, evaluation.MatchedStrategyKey, evaluation.MatchedRuleKey, evaluation.Features.InflowMinor, evaluation.Features.OutflowMinor, evaluation.Features.OutflowRatio)
	if evaluation.Decision != "block" || evaluation.Features.InflowMinor != 100_000 || evaluation.Features.OutflowMinor != 95_000 || evaluation.Features.OutflowRatio != 0.95 {
		t.Fatalf("unexpected evaluation: %+v", evaluation)
	}
	if evaluation.MatchedRuleKey != "rapid_fund_movement" {
		t.Fatalf("matched rule=%q, want rapid_fund_movement", evaluation.MatchedRuleKey)
	}
	if evaluation.MatchedStrategyKey != "block_rapid_fund_movement" {
		t.Fatalf("matched strategy=%q, want block_rapid_fund_movement", evaluation.MatchedStrategyKey)
	}
	if evaluation.Finding == nil || evaluation.Finding.RiskType != "rapid_fund_movement" {
		t.Fatalf("unexpected finding: %+v", evaluation.Finding)
	}
	if evaluation.Restriction == nil || evaluation.Restriction.Capability != "withdrawal" || evaluation.Restriction.Status != "active" {
		t.Fatalf("unexpected restriction: %+v", evaluation.Restriction)
	}
}

func TestOfflineScannerUsesTheStoredPolicyVersion(t *testing.T) {
	asOf := time.Date(2026, 10, 29, 2, 0, 0, 0, time.UTC)
	store := &offlineStoreStub{
		policy: OfflinePolicy{PolicyKey: "rapid_fund_movement", Version: 2, Status: "published", Window: 24 * time.Hour, MinInflow: 200_000, OutflowRatio: 0.90, Capability: "withdrawal"},
		events: []ActivityEvent{
			{EventID: "in-1", SubjectType: "organization", SubjectID: "org-below-threshold", EventType: "funds_in", AmountMinor: 100_000, Currency: "USD", OccurredAt: asOf.Add(-4 * time.Hour)},
			{EventID: "out-1", SubjectType: "organization", SubjectID: "org-below-threshold", EventType: "funds_out", AmountMinor: 95_000, Currency: "USD", OccurredAt: asOf.Add(-time.Hour)},
		},
	}
	result, _, err := NewOfflineScanner(store).Run(context.Background(), OfflineRunCommand{
		RunID: "offline-run-policy-v2", AsOf: asOf, PolicyKey: "rapid_fund_movement", PolicyVersion: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FindingsCreated != 0 || result.Evaluations[0].Decision != "pass" {
		t.Fatalf("stored v2 policy must keep this subject clear: %+v", result)
	}
}
