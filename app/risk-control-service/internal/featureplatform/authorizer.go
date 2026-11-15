package featureplatform

import (
	"context"
	"fmt"
)

type FeatureAccessDeniedError struct {
	Principal     string
	FeatureSetKey string
}

func (e FeatureAccessDeniedError) Error() string {
	return fmt.Sprintf("principal %q is not allowed to read feature set %q", e.Principal, e.FeatureSetKey)
}

type AllowListFeatureAuthorizer struct {
	featureSetsByPrincipal map[string]map[string]struct{}
}

func NewAllowListFeatureAuthorizer(grants map[string][]string) *AllowListFeatureAuthorizer {
	index := make(map[string]map[string]struct{}, len(grants))
	for principal, featureSets := range grants {
		allowed := make(map[string]struct{}, len(featureSets))
		for _, featureSet := range featureSets {
			if featureSet != "" {
				allowed[featureSet] = struct{}{}
			}
		}
		index[principal] = allowed
	}
	return &AllowListFeatureAuthorizer{featureSetsByPrincipal: index}
}

func (a *AllowListFeatureAuthorizer) Authorize(
	_ context.Context,
	principal string,
	definitions []ServingFeatureDefinition,
) error {
	if a == nil || principal == "" {
		return FeatureAccessDeniedError{Principal: principal, FeatureSetKey: "*"}
	}
	allowed := a.featureSetsByPrincipal[principal]
	for _, definition := range definitions {
		if _, wildcard := allowed["*"]; wildcard {
			continue
		}
		if _, exists := allowed[definition.Ref.FeatureSetKey]; !exists {
			return FeatureAccessDeniedError{
				Principal:     principal,
				FeatureSetKey: definition.Ref.FeatureSetKey,
			}
		}
	}
	return nil
}
