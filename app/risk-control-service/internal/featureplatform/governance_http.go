package featureplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const featureServingAdminPrefix = "/admin/v1/feature-serving/"

type GovernancePrincipalResolver interface {
	ResolveGovernancePrincipal(context.Context, *http.Request) (GovernancePrincipal, error)
}

type FeatureServingGovernanceHTTPHandler struct {
	governance *FeatureServingGovernance
	principals GovernancePrincipalResolver
}

func NewFeatureServingGovernanceHTTPHandler(
	governance *FeatureServingGovernance,
	principals GovernancePrincipalResolver,
) *FeatureServingGovernanceHTTPHandler {
	return &FeatureServingGovernanceHTTPHandler{governance: governance, principals: principals}
}

func (h *FeatureServingGovernanceHTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	operation, requestID, found := matchFeatureServingAdminRoute(request.Method, request.URL.Path)
	if !found {
		http.NotFound(writer, request)
		return
	}
	if h == nil || h.governance == nil || h.principals == nil {
		writeGovernanceError(writer, fmt.Errorf("feature serving governance handler is not configured"))
		return
	}
	principal, err := h.principals.ResolveGovernancePrincipal(request.Context(), request)
	if err != nil || principal.Subject == "" {
		writeGovernanceError(writer, GovernanceAccessDeniedError{Principal: principal.Subject})
		return
	}

	switch operation {
	case "request_promotion":
		var payload promotionRequestHTTPPayload
		if err := decodeGovernanceJSON(request, &payload); err != nil {
			writeGovernanceError(writer, err)
			return
		}
		promotion := payload.domain()
		created, err := h.governance.RequestPromotion(request.Context(), principal, promotion)
		if err != nil {
			writeGovernanceError(writer, err)
			return
		}
		writeGovernanceJSON(writer, http.StatusCreated, created)
	case "approve_promotion":
		var review struct {
			Reason string `json:"reason"`
		}
		if err := decodeGovernanceJSON(request, &review); err != nil {
			writeGovernanceError(writer, err)
			return
		}
		approved, err := h.governance.ApprovePromotion(request.Context(), principal, requestID, review.Reason)
		if err != nil {
			writeGovernanceError(writer, err)
			return
		}
		writeGovernanceJSON(writer, http.StatusOK, approved)
	case "reject_promotion":
		var review struct {
			Reason string `json:"reason"`
		}
		if err := decodeGovernanceJSON(request, &review); err != nil {
			writeGovernanceError(writer, err)
			return
		}
		rejected, err := h.governance.RejectPromotion(request.Context(), principal, requestID, review.Reason)
		if err != nil {
			writeGovernanceError(writer, err)
			return
		}
		writeGovernanceJSON(writer, http.StatusOK, rejected)
	case "rollback":
		var payload rollbackRequestHTTPPayload
		if err := decodeGovernanceJSON(request, &payload); err != nil {
			writeGovernanceError(writer, err)
			return
		}
		rollback := payload.domain()
		route, err := h.governance.EmergencyRollback(request.Context(), principal, rollback)
		if err != nil {
			writeGovernanceError(writer, err)
			return
		}
		writeGovernanceJSON(writer, http.StatusOK, route)
	}
}

type promotionRequestHTTPPayload struct {
	RequestID         string    `json:"request_id"`
	FeatureSetKey     string    `json:"feature_set_key"`
	FeatureSetVersion int       `json:"feature_set_version"`
	EvidenceStart     time.Time `json:"evidence_start"`
	EvidenceEnd       time.Time `json:"evidence_end"`
	BucketSeconds     int64     `json:"bucket_seconds"`
	Criteria          struct {
		MinimumComparisons                 int64   `json:"minimum_comparisons"`
		MinimumDistinctBuckets             int64   `json:"minimum_distinct_buckets"`
		MaximumMismatchRate                float64 `json:"maximum_mismatch_rate"`
		MaximumUnavailableRate             float64 `json:"maximum_unavailable_rate"`
		MaximumCandidateFreshnessLagMillis int64   `json:"maximum_candidate_freshness_lag_millis"`
		MaximumP95CandidateReadMillis      int64   `json:"maximum_p95_candidate_read_millis"`
	} `json:"criteria"`
	Reason string `json:"reason"`
}

func (p promotionRequestHTTPPayload) domain() PromotionRequest {
	return PromotionRequest{
		RequestID: p.RequestID, FeatureSetKey: p.FeatureSetKey,
		FeatureSetVersion: p.FeatureSetVersion, EvidenceStart: p.EvidenceStart,
		EvidenceEnd: p.EvidenceEnd, BucketDuration: time.Duration(p.BucketSeconds) * time.Second,
		Criteria: PromotionCriteria{
			MinimumComparisons:              p.Criteria.MinimumComparisons,
			MinimumDistinctBuckets:          p.Criteria.MinimumDistinctBuckets,
			MaximumMismatchRate:             p.Criteria.MaximumMismatchRate,
			MaximumUnavailableRate:          p.Criteria.MaximumUnavailableRate,
			MaximumCandidateFreshnessLag:    time.Duration(p.Criteria.MaximumCandidateFreshnessLagMillis) * time.Millisecond,
			MaximumP95CandidateReadDuration: time.Duration(p.Criteria.MaximumP95CandidateReadMillis) * time.Millisecond,
		},
		Reason: p.Reason,
	}
}

type rollbackRequestHTTPPayload struct {
	RequestID         string `json:"request_id"`
	FeatureSetKey     string `json:"feature_set_key"`
	FeatureSetVersion int    `json:"feature_set_version"`
	Reason            string `json:"reason"`
}

func (p rollbackRequestHTTPPayload) domain() ServingRollbackRequest {
	return ServingRollbackRequest{
		RequestID: p.RequestID, FeatureSetKey: p.FeatureSetKey,
		FeatureSetVersion: p.FeatureSetVersion, Reason: p.Reason,
	}
}

func matchFeatureServingAdminRoute(method, path string) (operation, requestID string, found bool) {
	if method != http.MethodPost || !strings.HasPrefix(path, featureServingAdminPrefix) {
		return "", "", false
	}
	relative := strings.Trim(strings.TrimPrefix(path, featureServingAdminPrefix), "/")
	if relative == "promotion-requests" {
		return "request_promotion", "", true
	}
	if relative == "rollbacks" {
		return "rollback", "", true
	}
	parts := strings.Split(relative, "/")
	if len(parts) == 3 && parts[0] == "promotion-requests" && parts[1] != "" {
		switch parts[2] {
		case "approve":
			return "approve_promotion", parts[1], true
		case "reject":
			return "reject_promotion", parts[1], true
		}
	}
	return "", "", false
}

func decodeGovernanceJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return InvalidRequestError{Message: "invalid JSON request: " + err.Error()}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return InvalidRequestError{Message: "request must contain one JSON object"}
	}
	return nil
}

func writeGovernanceJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeGovernanceError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	var denied GovernanceAccessDeniedError
	var separation SeparationOfDutiesError
	var invalid InvalidRequestError
	var invalidState InvalidStateError
	var conflict ConflictError
	var notFound NotFoundError
	switch {
	case errors.As(err, &denied), errors.As(err, &separation):
		status = http.StatusForbidden
	case errors.As(err, &invalid):
		status = http.StatusBadRequest
	case errors.As(err, &invalidState), errors.As(err, &conflict):
		status = http.StatusConflict
	case errors.As(err, &notFound):
		status = http.StatusNotFound
	}
	writeGovernanceJSON(writer, status, map[string]string{"error": err.Error()})
}
