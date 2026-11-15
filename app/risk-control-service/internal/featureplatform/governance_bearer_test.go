package featureplatform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"testing"
)

func TestBearerGovernancePrincipalResolverMapsSecretToTrustedRoles(t *testing.T) {
	digest := sha256.Sum256([]byte("reviewer-secret"))
	resolver, err := NewBearerGovernancePrincipalResolver([]GovernanceBearerCredential{{
		Subject: "reviewer-001", TokenSHA256: hex.EncodeToString(digest[:]),
		Roles: []GovernanceRole{GovernanceRoleReviewer},
	}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/admin/v1/feature-serving/promotion-requests/one/approve", nil)
	request.Header.Set("Authorization", "Bearer reviewer-secret")
	principal, err := resolver.ResolveGovernancePrincipal(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Subject != "reviewer-001" || !principal.hasRole(GovernanceRoleReviewer) {
		t.Fatalf("principal = %+v", principal)
	}

	request.Header.Set("Authorization", "Bearer wrong-secret")
	_, err = resolver.ResolveGovernancePrincipal(context.Background(), request)
	var denied GovernanceAccessDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("invalid credential error=%v, want GovernanceAccessDeniedError", err)
	}
}
