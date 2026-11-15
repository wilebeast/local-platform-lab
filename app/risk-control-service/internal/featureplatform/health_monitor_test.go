package featureplatform

import (
	"context"
	"testing"
	"time"
)

func TestOnlineHealthMonitorCreatesRollbackRecommendationWithoutChangingRoute(t *testing.T) {
	ctx := context.Background()
	repository := &healthMonitorRepositoryStub{
		route: ServingRoute{
			FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1,
			Mode: ServingModeOnline, Generation: 4,
		},
		evidence: PromotionEvidence{
			TotalComparisons: 100, DistinctBuckets: 10,
			MatchedComparisons: 98, MismatchComparisons: 2,
			MaximumCandidateFreshnessLag: 10 * time.Second,
			P95CandidateReadDuration:     5 * time.Millisecond,
		},
	}
	monitor := NewOnlineFeatureServingHealthMonitor(repository, repository, repository)
	request := onlineHealthCheckRequest()

	result, err := monitor.Evaluate(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Monitored || result.Healthy || result.Alert == nil {
		t.Fatalf("health result = %+v", result)
	}
	if result.Alert.RecommendedAction != HealthRecommendationRollback ||
		len(result.Alert.ReasonCodes) == 0 || result.Alert.RouteGeneration != 4 {
		t.Fatalf("alert = %+v", result.Alert)
	}
	if repository.route.Mode != ServingModeOnline {
		t.Fatalf("monitor must not mutate route: %+v", repository.route)
	}
}

func TestOnlineHealthMonitorSkipsShadowAndLeavesHealthyOnlineRouteUnalerted(t *testing.T) {
	ctx := context.Background()
	repository := &healthMonitorRepositoryStub{route: ServingRoute{
		FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1,
		Mode: ServingModeShadow, Generation: 2,
	}}
	monitor := NewOnlineFeatureServingHealthMonitor(repository, repository, repository)
	result, err := monitor.Evaluate(ctx, onlineHealthCheckRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Monitored || repository.evidenceReads != 0 || repository.alertWrites != 0 {
		t.Fatalf("shadow result=%+v reads=%d writes=%d", result, repository.evidenceReads, repository.alertWrites)
	}
}

func TestOnlineHealthMonitorResolvesAlertsAfterRecovery(t *testing.T) {
	ctx := context.Background()
	repository := &healthMonitorRepositoryStub{
		route: ServingRoute{
			FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1,
			Mode: ServingModeOnline, Generation: 4,
		},
		evidence: PromotionEvidence{
			TotalComparisons: 100, DistinctBuckets: 10, MatchedComparisons: 100,
			MaximumCandidateFreshnessLag: 10 * time.Second,
			P95CandidateReadDuration:     5 * time.Millisecond,
		},
		resolved: 2,
	}
	monitor := NewOnlineFeatureServingHealthMonitor(repository, repository, repository)
	result, err := monitor.Evaluate(ctx, onlineHealthCheckRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Healthy || result.ResolvedAlerts != 2 || repository.resolveCalls != 1 {
		t.Fatalf("recovery result=%+v resolve calls=%d", result, repository.resolveCalls)
	}
}

func onlineHealthCheckRequest() OnlineHealthCheckRequest {
	return OnlineHealthCheckRequest{
		CheckID: "online-health-001", FeatureSetKey: "subject_fund_activity",
		FeatureSetVersion: 1,
		EvidenceStart:     time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC),
		EvidenceEnd:       time.Date(2026, 11, 15, 13, 0, 0, 0, time.UTC),
		BucketDuration:    5 * time.Minute,
		Criteria: PromotionCriteria{
			MinimumComparisons: 100, MinimumDistinctBuckets: 10,
			MaximumMismatchRate: 0.01, MaximumUnavailableRate: 0,
			MaximumCandidateFreshnessLag:    time.Minute,
			MaximumP95CandidateReadDuration: 20 * time.Millisecond,
		},
	}
}

type healthMonitorRepositoryStub struct {
	route         ServingRoute
	evidence      PromotionEvidence
	evidenceReads int
	alertWrites   int
	alert         FeatureServingHealthAlert
	resolved      int64
	resolveCalls  int
}

func (s *healthMonitorRepositoryStub) GetServingRoute(context.Context, string, int) (ServingRoute, error) {
	return s.route, nil
}

func (s *healthMonitorRepositoryStub) SummarizePromotionEvidence(context.Context, PromotionEvidenceQuery) (PromotionEvidence, error) {
	s.evidenceReads++
	return s.evidence, nil
}

func (s *healthMonitorRepositoryStub) RecordHealthAlert(_ context.Context, alert FeatureServingHealthAlert) (FeatureServingHealthAlert, error) {
	s.alertWrites++
	s.alert = alert
	return alert, nil
}

func (s *healthMonitorRepositoryStub) ResolveOpenHealthAlerts(context.Context, string, int, int64, time.Time) (int64, error) {
	s.resolveCalls++
	return s.resolved, nil
}
