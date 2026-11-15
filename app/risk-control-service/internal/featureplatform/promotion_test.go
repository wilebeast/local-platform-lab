package featureplatform

import (
	"context"
	"testing"
	"time"
)

func TestPromotionControllerPromotesOnlyWhenEveryGatePasses(t *testing.T) {
	evidence := PromotionEvidence{
		TotalComparisons: 120, DistinctBuckets: 12, MatchedComparisons: 120,
		MaximumCandidateFreshnessLag: 20 * time.Second,
		P95CandidateReadDuration:     8 * time.Millisecond,
	}
	repository := &promotionRepositoryStub{evidence: evidence}
	controller := NewPromotionController(repository, repository)
	request := PromotionRequest{
		RequestID:     "promotion-clean-12h",
		FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1,
		EvidenceStart:  time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
		EvidenceEnd:    time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC),
		BucketDuration: time.Hour,
		Criteria: PromotionCriteria{
			MinimumComparisons: 100, MinimumDistinctBuckets: 12,
			MaximumMismatchRate: 0, MaximumUnavailableRate: 0,
			MaximumCandidateFreshnessLag:    time.Minute,
			MaximumP95CandidateReadDuration: 20 * time.Millisecond,
		},
		RequestedBy: "feature-operator", Reason: "twelve clean hourly buckets",
	}

	result, err := controller.AssessAndPromote(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Assessment.Eligible || result.Route.Mode != ServingModeOnline {
		t.Fatalf("promotion result = %+v", result)
	}
	if repository.applied != 1 {
		t.Fatalf("applied assessments = %d, want one", repository.applied)
	}

	blockedRepository := &promotionRepositoryStub{evidence: evidence}
	blockedRepository.evidence.MismatchComparisons = 1
	blockedRepository.evidence.MatchedComparisons = 119
	blockedController := NewPromotionController(blockedRepository, blockedRepository)
	blocked, err := blockedController.AssessAndPromote(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Assessment.Eligible || blocked.Route.Mode == ServingModeOnline {
		t.Fatalf("mismatched evidence must block promotion: %+v", blocked)
	}
	if len(blocked.Assessment.Reasons) == 0 || blockedRepository.applied != 1 {
		t.Fatalf("blocked assessment was not audited: %+v", blocked)
	}
}

func TestRoutedFeatureSnapshotReaderSwitchesOnlineAndBackToShadow(t *testing.T) {
	ctx := context.Background()
	cutoff := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	referenceSnapshot := onlineStoreTestSnapshot(cutoff, int64(100_000), "reference")
	candidateSnapshot := onlineStoreTestSnapshot(cutoff, int64(90_000), "candidate")
	candidate := NewInMemoryCurrentFeatureStore()
	if _, err := candidate.PutCurrent(ctx, candidateSnapshot); err != nil {
		t.Fatal(err)
	}
	routes := &promotionRepositoryStub{route: ServingRoute{
		FeatureSetKey:     referenceSnapshot.FeatureSetKey,
		FeatureSetVersion: referenceSnapshot.FeatureSetVersion,
		Mode:              ServingModeShadow,
	}}
	recorder := &recordingShadowComparisonRecorder{}
	reader := NewRoutedFeatureSnapshotReader(
		routes, candidate, &staticFeatureSnapshotReader{latest: []FeatureSnapshot{referenceSnapshot}},
		recorder,
	)
	request := SnapshotReadRequest{
		RequestID: "route-read", FeatureSetKey: referenceSnapshot.FeatureSetKey,
		FeatureSetVersion: referenceSnapshot.FeatureSetVersion, FeatureKeys: []string{"inflow_minor"},
		Subjects: []Subject{referenceSnapshot.Subject}, Cutoff: cutoff,
	}

	shadowRows, err := reader.ReadLatestBefore(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if shadowRows[0].Values["inflow_minor"] != int64(100_000) {
		t.Fatalf("shadow rows = %+v, want reference", shadowRows)
	}
	routes.route.Mode = ServingModeOnline
	onlineRows, err := reader.ReadLatestBefore(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if onlineRows[0].Values["inflow_minor"] != int64(90_000) {
		t.Fatalf("online rows = %+v, want candidate", onlineRows)
	}
	if len(recorder.comparisons) != 2 || recorder.comparisons[1].Status != ShadowComparisonMismatch {
		t.Fatalf("online serving must continue reference comparison: %+v", recorder.comparisons)
	}
	routes.route.Mode = ServingModeShadow
	rolledBackRows, err := reader.ReadLatestBefore(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBackRows[0].Values["inflow_minor"] != int64(100_000) {
		t.Fatalf("rolled back rows = %+v, want reference", rolledBackRows)
	}
}

type promotionRepositoryStub struct {
	evidence PromotionEvidence
	applied  int
	route    ServingRoute
}

func (s *promotionRepositoryStub) SummarizePromotionEvidence(
	context.Context,
	PromotionEvidenceQuery,
) (PromotionEvidence, error) {
	return s.evidence, nil
}

func (s *promotionRepositoryStub) ApplyPromotionAssessment(
	_ context.Context,
	request PromotionRequest,
	assessment PromotionAssessment,
) (ServingRoute, error) {
	s.applied++
	if assessment.Eligible {
		s.route = ServingRoute{
			FeatureSetKey: request.FeatureSetKey, FeatureSetVersion: request.FeatureSetVersion,
			Mode: ServingModeOnline, Generation: s.route.Generation + 1,
		}
	}
	return s.route, nil
}

func (s *promotionRepositoryStub) RollbackServingRoute(
	_ context.Context,
	request ServingRollbackRequest,
) (ServingRoute, error) {
	s.route.Mode = ServingModeShadow
	s.route.Generation++
	s.route.ChangedBy = request.RequestedBy
	s.route.ChangeReason = request.Reason
	return s.route, nil
}

func (s *promotionRepositoryStub) GetServingRoute(context.Context, string, int) (ServingRoute, error) {
	return s.route, nil
}
