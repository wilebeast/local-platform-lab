package featureplatform_test

import (
	"context"
	"testing"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/featureplatform"
)

func TestPostgresFeatureServingGovernancePersistsTwoPersonApproval(t *testing.T) {
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
	evidenceStart := time.Date(2026, 11, 15, 8, 0, 0, 0, time.UTC)
	for index := 0; index < 3; index++ {
		comparedAt := evidenceStart.Add(time.Duration(index)*time.Hour + time.Minute)
		if err := recorder.Record(ctx, []featureplatform.FeatureShadowComparison{{
			RequestID:     "s6-governance-evidence-" + string(rune('a'+index)),
			FeatureSetKey: draft.Key, FeatureSetVersion: draft.Version,
			Subject: featureplatform.Subject{Type: "organization", Keys: map[string]string{
				"subject_id": "org-governance", "currency": "USD",
			}},
			Cutoff: comparedAt, CandidateAsOf: comparedAt.Add(-10 * time.Second),
			ReferenceAsOf:         comparedAt.Add(-10 * time.Second),
			CandidateReadDuration: 5 * time.Millisecond, ReferenceReadDuration: 20 * time.Millisecond,
			Status: featureplatform.ShadowComparisonMatched, ComparedAt: comparedAt,
		}}); err != nil {
			t.Fatal(err)
		}
	}

	promotionRepository := featureplatform.NewPostgresPromotionRepository(pool, schema)
	governanceRepository := featureplatform.NewPostgresFeatureServingGovernanceRepository(pool, schema)
	governance := featureplatform.NewFeatureServingGovernance(
		governanceRepository,
		featureplatform.NewPromotionController(promotionRepository, promotionRepository),
	)
	request := featureplatform.PromotionRequest{
		RequestID: "s6-governance-promotion", FeatureSetKey: draft.Key,
		FeatureSetVersion: draft.Version, EvidenceStart: evidenceStart,
		EvidenceEnd: evidenceStart.Add(3 * time.Hour), BucketDuration: time.Hour,
		Criteria: featureplatform.PromotionCriteria{
			MinimumComparisons: 3, MinimumDistinctBuckets: 3,
			MaximumMismatchRate: 0, MaximumUnavailableRate: 0,
			MaximumCandidateFreshnessLag:    time.Minute,
			MaximumP95CandidateReadDuration: 20 * time.Millisecond,
		},
		Reason: "request production feature serving",
	}
	created, err := governance.RequestPromotion(ctx, featureplatform.GovernancePrincipal{
		Subject: "alice", Roles: []featureplatform.GovernanceRole{featureplatform.GovernanceRoleOperator},
	}, request)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := governance.ApprovePromotion(ctx, featureplatform.GovernancePrincipal{
		Subject: "bob", Roles: []featureplatform.GovernanceRole{featureplatform.GovernanceRoleReviewer},
	}, created.RequestID, "evidence independently reviewed")
	if err != nil {
		t.Fatal(err)
	}
	if approved.Request.Status != featureplatform.ChangeRequestApproved ||
		approved.Promotion.Route.Mode != featureplatform.ServingModeOnline ||
		approved.Promotion.Route.ChangedBy != "bob" {
		t.Fatalf("approved result = %+v", approved)
	}

	replayed, err := governanceRepository.CompleteReview(ctx, created.RequestID, featureplatform.ChangeRequestReview{
		Status: featureplatform.ChangeRequestApproved, ReviewedBy: "bob",
		Reason:       "evidence independently reviewed",
		AssessmentID: approved.Promotion.Assessment.AssessmentID,
		ReviewedAt:   approved.Request.ReviewedAt,
	})
	if err != nil || replayed.Status != featureplatform.ChangeRequestApproved {
		t.Fatalf("review replay = %+v err=%v", replayed, err)
	}

	var requestRows, assessmentRows, routeEvents int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+schema+".feature_serving_change_requests").Scan(&requestRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+schema+".feature_serving_promotion_assessments").Scan(&assessmentRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+schema+".feature_serving_route_events").Scan(&routeEvents); err != nil {
		t.Fatal(err)
	}
	if requestRows != 1 || assessmentRows != 1 || routeEvents != 1 {
		t.Fatalf("request=%d assessment=%d route events=%d", requestRows, assessmentRows, routeEvents)
	}
}
