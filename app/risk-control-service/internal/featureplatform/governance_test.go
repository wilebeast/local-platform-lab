package featureplatform

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFeatureServingGovernanceRequiresTwoPeopleForPromotion(t *testing.T) {
	ctx := context.Background()
	repository := &governanceRepositoryStub{}
	promotion := &promotionRepositoryStub{evidence: cleanGovernanceEvidence()}
	governance := NewFeatureServingGovernance(repository, NewPromotionController(promotion, promotion))
	request := governancePromotionRequest()

	created, err := governance.RequestPromotion(ctx, GovernancePrincipal{
		Subject: "alice", Roles: []GovernanceRole{GovernanceRoleOperator},
	}, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != ChangeRequestPending || created.RequestedBy != "alice" {
		t.Fatalf("created request = %+v", created)
	}

	_, err = governance.ApprovePromotion(ctx, GovernancePrincipal{
		Subject: "alice", Roles: []GovernanceRole{GovernanceRoleReviewer},
	}, request.RequestID, "self approval")
	var separationErr SeparationOfDutiesError
	if !errors.As(err, &separationErr) {
		t.Fatalf("self approval error = %v, want SeparationOfDutiesError", err)
	}
	if repository.request.Status != ChangeRequestPending || promotion.route.Mode == ServingModeOnline {
		t.Fatalf("self approval changed state: request=%+v route=%+v", repository.request, promotion.route)
	}

	approved, err := governance.ApprovePromotion(ctx, GovernancePrincipal{
		Subject: "bob", Roles: []GovernanceRole{GovernanceRoleReviewer},
	}, request.RequestID, "independent evidence review passed")
	if err != nil {
		t.Fatal(err)
	}
	if approved.Request.Status != ChangeRequestApproved || approved.Request.ReviewedBy != "bob" ||
		approved.Promotion.Route.Mode != ServingModeOnline {
		t.Fatalf("approved result = %+v", approved)
	}
}

func TestFeatureServingGovernanceEnforcesRolesAndSupportsEmergencyRollback(t *testing.T) {
	ctx := context.Background()
	repository := &governanceRepositoryStub{}
	promotion := &promotionRepositoryStub{route: ServingRoute{
		FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1,
		Mode: ServingModeOnline, Generation: 3,
	}}
	governance := NewFeatureServingGovernance(repository, NewPromotionController(promotion, promotion))

	_, err := governance.RequestPromotion(ctx, GovernancePrincipal{Subject: "reader"}, governancePromotionRequest())
	var denied GovernanceAccessDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("request error = %v, want GovernanceAccessDeniedError", err)
	}

	route, err := governance.EmergencyRollback(ctx, GovernancePrincipal{
		Subject: "incident-commander", Roles: []GovernanceRole{GovernanceRoleEmergencyOperator},
	}, ServingRollbackRequest{
		RequestID: "rollback-incident-001", FeatureSetKey: "subject_fund_activity",
		FeatureSetVersion: 1, Reason: "online mismatch alert",
	})
	if err != nil {
		t.Fatal(err)
	}
	if route.Mode != ServingModeShadow || route.ChangedBy != "incident-commander" {
		t.Fatalf("rollback route = %+v", route)
	}
}

func governancePromotionRequest() PromotionRequest {
	return PromotionRequest{
		RequestID: "governance-promotion-001", FeatureSetKey: "subject_fund_activity",
		FeatureSetVersion: 1,
		EvidenceStart:     time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
		EvidenceEnd:       time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC),
		BucketDuration:    time.Hour,
		Criteria: PromotionCriteria{
			MinimumComparisons: 100, MinimumDistinctBuckets: 12,
			MaximumMismatchRate: 0, MaximumUnavailableRate: 0,
			MaximumCandidateFreshnessLag:    time.Minute,
			MaximumP95CandidateReadDuration: 20 * time.Millisecond,
		},
		Reason: "candidate passed shadow comparison",
	}
}

func cleanGovernanceEvidence() PromotionEvidence {
	return PromotionEvidence{
		TotalComparisons: 120, DistinctBuckets: 12, MatchedComparisons: 120,
		MaximumCandidateFreshnessLag: 20 * time.Second,
		P95CandidateReadDuration:     8 * time.Millisecond,
	}
}

type governanceRepositoryStub struct {
	request FeatureServingChangeRequest
}

func (s *governanceRepositoryStub) CreatePromotionRequest(_ context.Context, request FeatureServingChangeRequest) (FeatureServingChangeRequest, error) {
	if s.request.RequestID != "" && s.request.RequestID != request.RequestID {
		return FeatureServingChangeRequest{}, ConflictError{Message: "different request already exists"}
	}
	s.request = request
	return s.request, nil
}

func (s *governanceRepositoryStub) GetChangeRequest(context.Context, string) (FeatureServingChangeRequest, error) {
	if s.request.RequestID == "" {
		return FeatureServingChangeRequest{}, NotFoundError{Message: "change request not found"}
	}
	return s.request, nil
}

func (s *governanceRepositoryStub) CompleteReview(_ context.Context, requestID string, review ChangeRequestReview) (FeatureServingChangeRequest, error) {
	if s.request.RequestID != requestID || s.request.Status != ChangeRequestPending {
		return FeatureServingChangeRequest{}, ConflictError{Message: "change request is not pending"}
	}
	s.request.Status = review.Status
	s.request.ReviewedBy = review.ReviewedBy
	s.request.ReviewReason = review.Reason
	s.request.AssessmentID = review.AssessmentID
	s.request.ReviewedAt = review.ReviewedAt
	return s.request, nil
}
