package featureplatform

import (
	"context"
	"fmt"
	"time"
)

type GovernanceRole string

const (
	GovernanceRoleOperator          GovernanceRole = "feature_operator"
	GovernanceRoleReviewer          GovernanceRole = "feature_reviewer"
	GovernanceRoleEmergencyOperator GovernanceRole = "feature_emergency_operator"
)

type GovernancePrincipal struct {
	Subject string
	Roles   []GovernanceRole
}

func (p GovernancePrincipal) hasRole(role GovernanceRole) bool {
	for _, assigned := range p.Roles {
		if assigned == role {
			return true
		}
	}
	return false
}

type GovernanceAccessDeniedError struct {
	Principal string
	Role      GovernanceRole
}

func (e GovernanceAccessDeniedError) Error() string {
	return fmt.Sprintf("principal %q requires role %q", e.Principal, e.Role)
}

type SeparationOfDutiesError struct{ Principal string }

func (e SeparationOfDutiesError) Error() string {
	return fmt.Sprintf("principal %q cannot approve their own feature serving change", e.Principal)
}

type ChangeRequestStatus string

const (
	ChangeRequestPending  ChangeRequestStatus = "pending"
	ChangeRequestApproved ChangeRequestStatus = "approved"
	ChangeRequestRejected ChangeRequestStatus = "rejected"
	ChangeRequestBlocked  ChangeRequestStatus = "blocked"
)

type FeatureServingChangeRequest struct {
	RequestID    string
	Promotion    PromotionRequest
	Status       ChangeRequestStatus
	RequestedBy  string
	ReviewedBy   string
	ReviewReason string
	AssessmentID string
	CreatedAt    time.Time
	ReviewedAt   time.Time
}

type ChangeRequestReview struct {
	Status       ChangeRequestStatus
	ReviewedBy   string
	Reason       string
	AssessmentID string
	ReviewedAt   time.Time
}

type PromotionApprovalResult struct {
	Request   FeatureServingChangeRequest
	Promotion PromotionResult
}

type FeatureServingGovernanceRepository interface {
	CreatePromotionRequest(context.Context, FeatureServingChangeRequest) (FeatureServingChangeRequest, error)
	GetChangeRequest(context.Context, string) (FeatureServingChangeRequest, error)
	CompleteReview(context.Context, string, ChangeRequestReview) (FeatureServingChangeRequest, error)
}

type FeatureServingGovernance struct {
	repository FeatureServingGovernanceRepository
	promotion  *PromotionController
	now        func() time.Time
}

func NewFeatureServingGovernance(
	repository FeatureServingGovernanceRepository,
	promotion *PromotionController,
) *FeatureServingGovernance {
	return &FeatureServingGovernance{
		repository: repository,
		promotion:  promotion,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

func (g *FeatureServingGovernance) RequestPromotion(
	ctx context.Context,
	principal GovernancePrincipal,
	request PromotionRequest,
) (FeatureServingChangeRequest, error) {
	if err := requireGovernanceRole(principal, GovernanceRoleOperator); err != nil {
		return FeatureServingChangeRequest{}, err
	}
	if g == nil || g.repository == nil || g.promotion == nil {
		return FeatureServingChangeRequest{}, fmt.Errorf("feature serving governance dependencies are required")
	}
	request.RequestedBy = principal.Subject
	if err := validatePromotionRequest(request); err != nil {
		return FeatureServingChangeRequest{}, err
	}
	created := FeatureServingChangeRequest{
		RequestID: request.RequestID, Promotion: request,
		Status: ChangeRequestPending, RequestedBy: principal.Subject, CreatedAt: g.now(),
	}
	return g.repository.CreatePromotionRequest(ctx, created)
}

func (g *FeatureServingGovernance) ApprovePromotion(
	ctx context.Context,
	principal GovernancePrincipal,
	requestID string,
	reason string,
) (PromotionApprovalResult, error) {
	if err := requireGovernanceRole(principal, GovernanceRoleReviewer); err != nil {
		return PromotionApprovalResult{}, err
	}
	if g == nil || g.repository == nil || g.promotion == nil {
		return PromotionApprovalResult{}, fmt.Errorf("feature serving governance dependencies are required")
	}
	if requestID == "" || reason == "" {
		return PromotionApprovalResult{}, InvalidRequestError{Message: "change request and review reason are required"}
	}
	stored, err := g.repository.GetChangeRequest(ctx, requestID)
	if err != nil {
		return PromotionApprovalResult{}, err
	}
	if stored.Status != ChangeRequestPending {
		return PromotionApprovalResult{}, InvalidStateError{Message: "feature serving change request is not pending"}
	}
	if stored.RequestedBy == principal.Subject {
		return PromotionApprovalResult{}, SeparationOfDutiesError{Principal: principal.Subject}
	}
	promotionRequest := stored.Promotion
	promotionRequest.RequestedBy = principal.Subject
	promotionRequest.Reason = reason
	result, err := g.promotion.AssessAndPromote(ctx, promotionRequest)
	if err != nil {
		return PromotionApprovalResult{}, err
	}
	status := ChangeRequestApproved
	if !result.Assessment.Eligible {
		status = ChangeRequestBlocked
	}
	completed, err := g.repository.CompleteReview(ctx, requestID, ChangeRequestReview{
		Status: status, ReviewedBy: principal.Subject, Reason: reason,
		AssessmentID: result.Assessment.AssessmentID, ReviewedAt: g.now(),
	})
	if err != nil {
		return PromotionApprovalResult{}, err
	}
	return PromotionApprovalResult{Request: completed, Promotion: result}, nil
}

func (g *FeatureServingGovernance) RejectPromotion(
	ctx context.Context,
	principal GovernancePrincipal,
	requestID string,
	reason string,
) (FeatureServingChangeRequest, error) {
	if err := requireGovernanceRole(principal, GovernanceRoleReviewer); err != nil {
		return FeatureServingChangeRequest{}, err
	}
	if g == nil || g.repository == nil {
		return FeatureServingChangeRequest{}, fmt.Errorf("feature serving governance repository is required")
	}
	if requestID == "" || reason == "" {
		return FeatureServingChangeRequest{}, InvalidRequestError{Message: "change request and rejection reason are required"}
	}
	stored, err := g.repository.GetChangeRequest(ctx, requestID)
	if err != nil {
		return FeatureServingChangeRequest{}, err
	}
	if stored.Status != ChangeRequestPending {
		return FeatureServingChangeRequest{}, InvalidStateError{Message: "feature serving change request is not pending"}
	}
	if stored.RequestedBy == principal.Subject {
		return FeatureServingChangeRequest{}, SeparationOfDutiesError{Principal: principal.Subject}
	}
	return g.repository.CompleteReview(ctx, requestID, ChangeRequestReview{
		Status: ChangeRequestRejected, ReviewedBy: principal.Subject,
		Reason: reason, ReviewedAt: g.now(),
	})
}

func (g *FeatureServingGovernance) EmergencyRollback(
	ctx context.Context,
	principal GovernancePrincipal,
	request ServingRollbackRequest,
) (ServingRoute, error) {
	if err := requireGovernanceRole(principal, GovernanceRoleEmergencyOperator); err != nil {
		return ServingRoute{}, err
	}
	if g == nil || g.promotion == nil {
		return ServingRoute{}, fmt.Errorf("feature serving promotion controller is required")
	}
	request.RequestedBy = principal.Subject
	return g.promotion.Rollback(ctx, request)
}

func requireGovernanceRole(principal GovernancePrincipal, role GovernanceRole) error {
	if principal.Subject == "" || !principal.hasRole(role) {
		return GovernanceAccessDeniedError{Principal: principal.Subject, Role: role}
	}
	return nil
}
