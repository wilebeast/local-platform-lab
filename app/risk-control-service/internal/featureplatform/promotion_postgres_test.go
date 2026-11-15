package featureplatform_test

import (
	"context"
	"testing"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/featureplatform"
)

func TestPostgresPromotionControllerPromotesAndRollsBackServingRoute(t *testing.T) {
	ctx := context.Background()
	pool, schema := newRegistryTestPool(t, ctx)
	registry := featureplatform.NewPostgresRegistry(pool, schema)
	registerFundActivityPrerequisites(t, ctx, registry)
	draft := fundActivityDraft()
	if err := registry.SaveDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Publish(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}

	recorder := featureplatform.NewPostgresShadowComparisonRecorder(pool, schema)
	for index, hour := range []int{10, 11, 12} {
		cutoff := time.Date(2026, 11, 15, hour, 5, 0, 0, time.UTC)
		comparison := featureplatform.FeatureShadowComparison{
			RequestID:     "promotion-evidence-" + string(rune('a'+index)),
			FeatureSetKey: draft.Key, FeatureSetVersion: draft.Version,
			Subject: featureplatform.Subject{Type: "organization", Keys: map[string]string{
				"subject_id": "org-promotion", "currency": "USD",
			}},
			Cutoff: cutoff, CandidateAsOf: cutoff.Add(-30 * time.Second),
			ReferenceAsOf:         cutoff.Add(-30 * time.Second),
			CandidateReadDuration: 5 * time.Millisecond,
			ReferenceReadDuration: 25 * time.Millisecond,
			Status:                featureplatform.ShadowComparisonMatched, ComparedAt: cutoff,
		}
		if err := recorder.Record(ctx, []featureplatform.FeatureShadowComparison{comparison}); err != nil {
			t.Fatal(err)
		}
	}

	repository := featureplatform.NewPostgresPromotionRepository(pool, schema)
	controller := featureplatform.NewPromotionController(repository, repository)
	promotionRequest := featureplatform.PromotionRequest{
		RequestID: "promote-fund-v1", FeatureSetKey: draft.Key, FeatureSetVersion: draft.Version,
		EvidenceStart:  time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC),
		EvidenceEnd:    time.Date(2026, 11, 15, 13, 0, 0, 0, time.UTC),
		BucketDuration: time.Hour,
		Criteria: featureplatform.PromotionCriteria{
			MinimumComparisons: 3, MinimumDistinctBuckets: 3,
			MaximumMismatchRate: 0, MaximumUnavailableRate: 0,
			MaximumCandidateFreshnessLag:    time.Minute,
			MaximumP95CandidateReadDuration: 20 * time.Millisecond,
		},
		RequestedBy: "feature-operator", Reason: "three clean hourly buckets",
	}
	result, err := controller.AssessAndPromote(ctx, promotionRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Assessment.Eligible || result.Route.Mode != featureplatform.ServingModeOnline || result.Route.Generation != 1 {
		t.Fatalf("promotion result = %+v", result)
	}
	replayedPromotion, err := controller.AssessAndPromote(ctx, promotionRequest)
	if err != nil || replayedPromotion.Route.Generation != 1 {
		t.Fatalf("promotion replay = %+v err=%v", replayedPromotion, err)
	}

	rollbackRequest := featureplatform.ServingRollbackRequest{
		RequestID: "rollback-fund-v1", FeatureSetKey: draft.Key, FeatureSetVersion: draft.Version,
		RequestedBy: "feature-operator", Reason: "coach rollback drill",
	}
	route, err := controller.Rollback(ctx, rollbackRequest)
	if err != nil {
		t.Fatal(err)
	}
	if route.Mode != featureplatform.ServingModeShadow || route.Generation != 2 {
		t.Fatalf("rollback route = %+v", route)
	}
	replayedRollback, err := controller.Rollback(ctx, rollbackRequest)
	if err != nil || replayedRollback.Generation != 2 {
		t.Fatalf("rollback replay = %+v err=%v", replayedRollback, err)
	}

	var assessmentCount, routeEventCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+schema+".feature_serving_promotion_assessments").Scan(&assessmentCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+schema+".feature_serving_route_events").Scan(&routeEventCount); err != nil {
		t.Fatal(err)
	}
	if assessmentCount != 1 || routeEventCount != 2 {
		t.Fatalf("assessment rows=%d route events=%d", assessmentCount, routeEventCount)
	}
}
