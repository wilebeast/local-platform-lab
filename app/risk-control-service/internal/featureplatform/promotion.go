package featureplatform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

type ServingMode string

const (
	ServingModeShadow ServingMode = "shadow"
	ServingModeOnline ServingMode = "online"
)

type PromotionCriteria struct {
	MinimumComparisons              int64
	MinimumDistinctBuckets          int64
	MaximumMismatchRate             float64
	MaximumUnavailableRate          float64
	MaximumCandidateFreshnessLag    time.Duration
	MaximumP95CandidateReadDuration time.Duration
}

type PromotionEvidenceQuery struct {
	FeatureSetKey     string
	FeatureSetVersion int
	Start             time.Time
	End               time.Time
	BucketDuration    time.Duration
}

type PromotionEvidence struct {
	TotalComparisons             int64
	DistinctBuckets              int64
	MatchedComparisons           int64
	MismatchComparisons          int64
	MissingCandidateComparisons  int64
	MissingReferenceComparisons  int64
	CandidateErrorComparisons    int64
	ReferenceErrorComparisons    int64
	MaximumCandidateFreshnessLag time.Duration
	P95CandidateReadDuration     time.Duration
}

type PromotionRequest struct {
	RequestID         string
	FeatureSetKey     string
	FeatureSetVersion int
	EvidenceStart     time.Time
	EvidenceEnd       time.Time
	BucketDuration    time.Duration
	Criteria          PromotionCriteria
	RequestedBy       string
	Reason            string
}

type PromotionAssessment struct {
	AssessmentID string
	Eligible     bool
	Reasons      []string
	Evidence     PromotionEvidence
	Criteria     PromotionCriteria
	AssessedAt   time.Time
}

type ServingRoute struct {
	FeatureSetKey     string
	FeatureSetVersion int
	Mode              ServingMode
	Generation        int64
	ChangedBy         string
	ChangeReason      string
	ChangedAt         time.Time
}

type PromotionResult struct {
	Assessment PromotionAssessment
	Route      ServingRoute
}

type ServingRollbackRequest struct {
	RequestID         string
	FeatureSetKey     string
	FeatureSetVersion int
	RequestedBy       string
	Reason            string
}

type PromotionEvidenceReader interface {
	SummarizePromotionEvidence(context.Context, PromotionEvidenceQuery) (PromotionEvidence, error)
}

type PromotionStateStore interface {
	ApplyPromotionAssessment(context.Context, PromotionRequest, PromotionAssessment) (ServingRoute, error)
	RollbackServingRoute(context.Context, ServingRollbackRequest) (ServingRoute, error)
	GetServingRoute(context.Context, string, int) (ServingRoute, error)
}

type PromotionController struct {
	evidence PromotionEvidenceReader
	state    PromotionStateStore
	now      func() time.Time
}

func NewPromotionController(
	evidence PromotionEvidenceReader,
	state PromotionStateStore,
) *PromotionController {
	return &PromotionController{
		evidence: evidence,
		state:    state,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

func (c *PromotionController) AssessAndPromote(
	ctx context.Context,
	request PromotionRequest,
) (PromotionResult, error) {
	if c == nil || c.evidence == nil || c.state == nil {
		return PromotionResult{}, fmt.Errorf("promotion evidence and state stores are required")
	}
	if err := validatePromotionRequest(request); err != nil {
		return PromotionResult{}, err
	}
	evidence, err := c.evidence.SummarizePromotionEvidence(ctx, PromotionEvidenceQuery{
		FeatureSetKey: request.FeatureSetKey, FeatureSetVersion: request.FeatureSetVersion,
		Start: request.EvidenceStart, End: request.EvidenceEnd,
		BucketDuration: request.BucketDuration,
	})
	if err != nil {
		return PromotionResult{}, fmt.Errorf("summarize promotion evidence: %w", err)
	}
	if err := validatePromotionEvidence(evidence); err != nil {
		return PromotionResult{}, err
	}
	assessment := assessPromotion(request.Criteria, evidence, c.now())
	assessment.AssessmentID = stableServingChangeID("assessment", request.RequestID, request.FeatureSetKey, request.FeatureSetVersion)
	route, err := c.state.ApplyPromotionAssessment(ctx, request, assessment)
	if err != nil {
		return PromotionResult{}, fmt.Errorf("apply promotion assessment: %w", err)
	}
	return PromotionResult{Assessment: assessment, Route: route}, nil
}

func (c *PromotionController) Rollback(
	ctx context.Context,
	request ServingRollbackRequest,
) (ServingRoute, error) {
	if c == nil || c.state == nil {
		return ServingRoute{}, fmt.Errorf("promotion state store is required")
	}
	if request.RequestID == "" || request.FeatureSetKey == "" || request.FeatureSetVersion <= 0 || request.RequestedBy == "" || request.Reason == "" {
		return ServingRoute{}, InvalidRequestError{Message: "feature set, operator and rollback reason are required"}
	}
	return c.state.RollbackServingRoute(ctx, request)
}

func assessPromotion(
	criteria PromotionCriteria,
	evidence PromotionEvidence,
	assessedAt time.Time,
) PromotionAssessment {
	reasons := make([]string, 0, 6)
	if evidence.TotalComparisons < criteria.MinimumComparisons {
		reasons = append(reasons, "insufficient_comparisons")
	}
	if evidence.DistinctBuckets < criteria.MinimumDistinctBuckets {
		reasons = append(reasons, "insufficient_time_coverage")
	}
	if ratio(evidence.MismatchComparisons, evidence.TotalComparisons) > criteria.MaximumMismatchRate {
		reasons = append(reasons, "mismatch_rate_exceeded")
	}
	unavailable := evidence.MissingCandidateComparisons + evidence.MissingReferenceComparisons +
		evidence.CandidateErrorComparisons + evidence.ReferenceErrorComparisons
	if ratio(unavailable, evidence.TotalComparisons) > criteria.MaximumUnavailableRate {
		reasons = append(reasons, "unavailable_rate_exceeded")
	}
	if evidence.MaximumCandidateFreshnessLag > criteria.MaximumCandidateFreshnessLag {
		reasons = append(reasons, "freshness_lag_exceeded")
	}
	if evidence.P95CandidateReadDuration > criteria.MaximumP95CandidateReadDuration {
		reasons = append(reasons, "p95_latency_exceeded")
	}
	return PromotionAssessment{
		Eligible: len(reasons) == 0, Reasons: reasons, Evidence: evidence,
		Criteria: criteria, AssessedAt: assessedAt,
	}
}

func validatePromotionRequest(request PromotionRequest) error {
	criteria := request.Criteria
	if request.RequestID == "" || request.FeatureSetKey == "" || request.FeatureSetVersion <= 0 ||
		request.EvidenceStart.IsZero() || !request.EvidenceEnd.After(request.EvidenceStart) ||
		request.BucketDuration < time.Second || request.BucketDuration%time.Second != 0 ||
		request.RequestedBy == "" || request.Reason == "" {
		return InvalidRequestError{Message: "complete feature set, evidence window, operator and reason are required"}
	}
	if criteria.MinimumComparisons <= 0 || criteria.MinimumDistinctBuckets <= 0 ||
		criteria.MaximumMismatchRate < 0 || criteria.MaximumMismatchRate > 1 ||
		criteria.MaximumUnavailableRate < 0 || criteria.MaximumUnavailableRate > 1 ||
		criteria.MaximumCandidateFreshnessLag <= 0 || criteria.MaximumP95CandidateReadDuration <= 0 {
		return InvalidRequestError{Message: "valid promotion criteria are required"}
	}
	return nil
}

func validatePromotionEvidence(evidence PromotionEvidence) error {
	counts := []int64{
		evidence.TotalComparisons, evidence.DistinctBuckets, evidence.MatchedComparisons,
		evidence.MismatchComparisons, evidence.MissingCandidateComparisons,
		evidence.MissingReferenceComparisons, evidence.CandidateErrorComparisons,
		evidence.ReferenceErrorComparisons,
	}
	for _, count := range counts {
		if count < 0 {
			return InvalidStateError{Message: "promotion evidence counts cannot be negative"}
		}
	}
	classified := evidence.MatchedComparisons + evidence.MismatchComparisons +
		evidence.MissingCandidateComparisons + evidence.MissingReferenceComparisons +
		evidence.CandidateErrorComparisons + evidence.ReferenceErrorComparisons
	if classified != evidence.TotalComparisons || evidence.DistinctBuckets > evidence.TotalComparisons ||
		evidence.MaximumCandidateFreshnessLag < 0 || evidence.P95CandidateReadDuration < 0 {
		return InvalidStateError{Message: "promotion evidence is internally inconsistent"}
	}
	return nil
}

func ratio(part, total int64) float64 {
	if total <= 0 {
		if part > 0 {
			return 1
		}
		return 0
	}
	return float64(part) / float64(total)
}

func stableServingChangeID(kind, requestID, featureSetKey string, featureSetVersion int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d", kind, requestID, featureSetKey, featureSetVersion)))
	return hex.EncodeToString(sum[:])
}
