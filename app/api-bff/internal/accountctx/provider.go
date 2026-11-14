package accountctx

import (
	"context"
	"fmt"
	"strconv"
)

type RouteCaller interface {
	CallRoute(context.Context, string, map[string]any) (map[string]any, error)
}

type AccessProvider interface {
	Load(context.Context, string) (Projection, error)
}

type GenericProvider struct {
	caller RouteCaller
}

type LoginInitializer struct {
	provider AccessProvider
	resolver *Resolver
}

func NewGenericProvider(caller RouteCaller) *GenericProvider {
	return &GenericProvider{caller: caller}
}

func NewLoginInitializer(provider AccessProvider, resolver *Resolver) *LoginInitializer {
	return &LoginInitializer{provider: provider, resolver: resolver}
}

func (i *LoginInitializer) Initialize(ctx context.Context, actor Actor) (Projection, error) {
	if i == nil || i.provider == nil || i.resolver == nil || actor.UserID == "" || actor.SessionID == "" {
		return Projection{}, ErrProjectionNotFound
	}
	projection, err := i.provider.Load(ctx, actor.UserID)
	if err != nil {
		return Projection{}, err
	}
	if err := i.resolver.Prime(ctx, projection); err != nil {
		return Projection{}, err
	}
	if len(projection.Accesses) == 1 {
		if _, err := i.resolver.Select(ctx, actor, projection.Accesses[0].AccountUID); err != nil {
			return Projection{}, err
		}
	}
	return projection, nil
}

func (p *GenericProvider) Load(ctx context.Context, userID string) (Projection, error) {
	if p == nil || p.caller == nil || userID == "" {
		return Projection{}, ErrProjectionNotFound
	}
	result, err := p.caller.CallRoute(ctx, "list-account-accesses", map[string]any{"userId": userID})
	if err != nil {
		return Projection{}, fmt.Errorf("list account accesses: %w", err)
	}
	version, ok := int64Value(result["permissionVersion"])
	if !ok || version <= 0 {
		return Projection{}, ErrInvalidProjection
	}
	rawAccesses, ok := result["accesses"].([]any)
	if !ok || len(rawAccesses) == 0 {
		return Projection{}, ErrInvalidProjection
	}
	projection := Projection{UserID: userID, PermissionVersion: version, Accesses: make([]Access, 0, len(rawAccesses))}
	for _, raw := range rawAccesses {
		item, ok := raw.(map[string]any)
		if !ok {
			return Projection{}, ErrInvalidProjection
		}
		access := Access{
			AccountID: stringValue(item["accountId"]), AccountUID: stringValue(item["accountUid"]),
			AccountType: stringValue(item["accountType"]), OrganizationID: stringValue(item["organizationId"]),
			MemberID: stringValue(item["memberId"]), Relationship: stringValue(item["relationship"]),
			RoleIDs: stringSlice(item["roleIds"]),
		}
		if access.AccountID == "" || access.AccountUID == "" || access.AccountType == "" || access.Relationship == "" {
			return Projection{}, ErrInvalidProjection
		}
		projection.Accesses = append(projection.Accesses, access)
	}
	return projection, nil
}

func int64Value(value any) (int64, bool) {
	switch typed := value.(type) {
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		return parsed, err == nil
	case float64:
		return int64(typed), typed == float64(int64(typed))
	case int64:
		return typed, true
	default:
		return 0, false
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func stringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		if direct, ok := value.([]string); ok {
			return append([]string(nil), direct...)
		}
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && text != "" {
			result = append(result, text)
		}
	}
	return result
}
