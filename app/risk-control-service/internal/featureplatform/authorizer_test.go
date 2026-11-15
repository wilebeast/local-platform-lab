package featureplatform

import (
	"context"
	"errors"
	"testing"
)

func TestAllowListFeatureAuthorizerRejectsUnapprovedFeatureSet(t *testing.T) {
	authorizer := NewAllowListFeatureAuthorizer(map[string][]string{
		"risk-control-service": {"subject_fund_activity"},
	})
	allowed := []ServingFeatureDefinition{{
		Ref: FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "inflow_minor"},
	}}
	if err := authorizer.Authorize(context.Background(), "risk-control-service", allowed); err != nil {
		t.Fatalf("authorized feature was rejected: %v", err)
	}
	denied := []ServingFeatureDefinition{{
		Ref: FeatureRef{FeatureSetKey: "subject_profile_risk", FeatureSetVersion: 1, FeatureKey: "verification_age_days"},
	}}
	err := authorizer.Authorize(context.Background(), "risk-control-service", denied)
	var deniedError FeatureAccessDeniedError
	if !errors.As(err, &deniedError) {
		t.Fatalf("authorization error=%v, want FeatureAccessDeniedError", err)
	}
}
