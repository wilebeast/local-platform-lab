package featureplatform

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFeatureServingGovernanceHTTPHandlerUsesTrustedPrincipal(t *testing.T) {
	repository := &governanceRepositoryStub{}
	promotion := &promotionRepositoryStub{evidence: cleanGovernanceEvidence()}
	governance := NewFeatureServingGovernance(repository, NewPromotionController(promotion, promotion))
	principals := &mutableGovernancePrincipalResolver{principal: GovernancePrincipal{
		Subject: "alice", Roles: []GovernanceRole{GovernanceRoleOperator},
	}}
	handler := NewFeatureServingGovernanceHTTPHandler(governance, principals)
	request := governancePromotionRequest()
	createBody := `{
		"request_id":"governance-promotion-001",
		"feature_set_key":"subject_fund_activity",
		"feature_set_version":1,
		"evidence_start":"2026-11-15T00:00:00Z",
		"evidence_end":"2026-11-15T12:00:00Z",
		"bucket_seconds":3600,
		"criteria":{
			"minimum_comparisons":100,
			"minimum_distinct_buckets":12,
			"maximum_mismatch_rate":0,
			"maximum_unavailable_rate":0,
			"maximum_candidate_freshness_lag_millis":60000,
			"maximum_p95_candidate_read_millis":20
		},
		"reason":"candidate passed shadow comparison"
	}`
	create := httptest.NewRequest(http.MethodPost, "/admin/v1/feature-serving/promotion-requests", bytes.NewBufferString(createBody))
	create.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	if repository.request.RequestedBy != "alice" {
		t.Fatalf("requested_by=%q, want trusted principal alice", repository.request.RequestedBy)
	}

	principals.principal = GovernancePrincipal{
		Subject: "bob", Roles: []GovernanceRole{GovernanceRoleReviewer},
	}
	approve := httptest.NewRequest(
		http.MethodPost,
		"/admin/v1/feature-serving/promotion-requests/"+request.RequestID+"/approve",
		bytes.NewBufferString(`{"reason":"independent review passed"}`),
	)
	approve.Header.Set("Content-Type", "application/json")
	approveResponse := httptest.NewRecorder()
	handler.ServeHTTP(approveResponse, approve)
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approveResponse.Code, approveResponse.Body.String())
	}
	if repository.request.Status != ChangeRequestApproved || repository.request.ReviewedBy != "bob" {
		t.Fatalf("reviewed request = %+v", repository.request)
	}
}

func TestFeatureServingGovernanceHTTPHandlerRejectsUntrustedOrUnknownRoutes(t *testing.T) {
	handler := NewFeatureServingGovernanceHTTPHandler(
		NewFeatureServingGovernance(&governanceRepositoryStub{}, NewPromotionController(&promotionRepositoryStub{}, &promotionRepositoryStub{})),
		&mutableGovernancePrincipalResolver{},
	)
	request := httptest.NewRequest(http.MethodPost, "/admin/v1/feature-serving/promotion-requests", bytes.NewBufferString(`{}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status=%d, want %d", response.Code, http.StatusForbidden)
	}

	unknown := httptest.NewRequest(http.MethodGet, "/admin/v1/feature-serving/unknown", nil)
	unknownResponse := httptest.NewRecorder()
	handler.ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusNotFound {
		t.Fatalf("unknown status=%d, want %d", unknownResponse.Code, http.StatusNotFound)
	}
}

type mutableGovernancePrincipalResolver struct {
	principal GovernancePrincipal
	err       error
}

func (r *mutableGovernancePrincipalResolver) ResolveGovernancePrincipal(context.Context, *http.Request) (GovernancePrincipal, error) {
	return r.principal, r.err
}
