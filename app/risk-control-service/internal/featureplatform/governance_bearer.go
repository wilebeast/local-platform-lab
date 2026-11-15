package featureplatform

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

type GovernanceBearerCredential struct {
	Subject     string           `json:"subject"`
	TokenSHA256 string           `json:"token_sha256"`
	Roles       []GovernanceRole `json:"roles"`
}

type bearerGovernanceCredential struct {
	subject string
	digest  [sha256.Size]byte
	roles   []GovernanceRole
}

type BearerGovernancePrincipalResolver struct {
	credentials []bearerGovernanceCredential
}

func NewBearerGovernancePrincipalResolver(
	definitions []GovernanceBearerCredential,
) (*BearerGovernancePrincipalResolver, error) {
	if len(definitions) == 0 {
		return nil, fmt.Errorf("at least one governance bearer credential is required")
	}
	credentials := make([]bearerGovernanceCredential, 0, len(definitions))
	for _, definition := range definitions {
		decoded, err := hex.DecodeString(definition.TokenSHA256)
		if definition.Subject == "" || err != nil || len(decoded) != sha256.Size || len(definition.Roles) == 0 {
			return nil, fmt.Errorf("complete governance subject, SHA-256 token digest and roles are required")
		}
		credential := bearerGovernanceCredential{
			subject: definition.Subject,
			roles:   append([]GovernanceRole(nil), definition.Roles...),
		}
		copy(credential.digest[:], decoded)
		for _, role := range credential.roles {
			if role != GovernanceRoleOperator && role != GovernanceRoleReviewer && role != GovernanceRoleEmergencyOperator {
				return nil, fmt.Errorf("unsupported governance role %q", role)
			}
		}
		credentials = append(credentials, credential)
	}
	return &BearerGovernancePrincipalResolver{credentials: credentials}, nil
}

func (r *BearerGovernancePrincipalResolver) ResolveGovernancePrincipal(
	_ context.Context,
	request *http.Request,
) (GovernancePrincipal, error) {
	if r == nil || request == nil {
		return GovernancePrincipal{}, GovernanceAccessDeniedError{}
	}
	parts := strings.SplitN(request.Header.Get("Authorization"), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return GovernancePrincipal{}, GovernanceAccessDeniedError{}
	}
	digest := sha256.Sum256([]byte(parts[1]))
	for _, credential := range r.credentials {
		if subtle.ConstantTimeCompare(digest[:], credential.digest[:]) == 1 {
			return GovernancePrincipal{
				Subject: credential.subject,
				Roles:   append([]GovernanceRole(nil), credential.roles...),
			}, nil
		}
	}
	return GovernancePrincipal{}, GovernanceAccessDeniedError{}
}
