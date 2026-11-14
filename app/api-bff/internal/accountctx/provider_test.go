package accountctx

import (
	"context"
	"testing"
	"time"
)

type fakeRouteCaller struct {
	route   string
	request map[string]any
}

type fakeAccessProvider struct {
	projection Projection
}

func (f fakeAccessProvider) Load(_ context.Context, _ string) (Projection, error) {
	return f.projection, nil
}

func TestLoginInitializerPrimesSharedProjectionWithoutChoosingBetweenMultipleAccounts(t *testing.T) {
	store := newMemoryStore()
	resolver := NewResolver(store, time.Hour)
	initializer := NewLoginInitializer(fakeAccessProvider{projection: Projection{
		UserID: "user-1", PermissionVersion: 3,
		Accesses: []Access{
			{AccountID: "account-personal", AccountUID: "personal", AccountType: "personal", Relationship: "owner"},
			{AccountID: "account-enterprise", AccountUID: "enterprise", AccountType: "enterprise", Relationship: "member"},
		},
	}}, resolver)

	projection, err := initializer.Initialize(context.Background(), Actor{UserID: "user-1", SessionID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Accesses) != 2 || store.projectionWrites != 1 || len(store.selections) != 0 {
		t.Fatalf("unexpected initialized state: projection=%#v projectionWrites=%d selections=%d", projection, store.projectionWrites, len(store.selections))
	}
}

func (f *fakeRouteCaller) CallRoute(_ context.Context, route string, request map[string]any) (map[string]any, error) {
	f.route = route
	f.request = request
	return map[string]any{
		"permissionVersion": "7",
		"accesses": []any{
			map[string]any{"accountId": "account-personal-1", "accountUid": "personal-1", "accountType": "personal", "relationship": "owner"},
			map[string]any{"accountId": "account-enterprise-1", "accountUid": "enterprise-1", "accountType": "enterprise", "organizationId": "org-1", "memberId": "member-1", "roleIds": []any{"finance_member"}, "relationship": "member"},
		},
	}, nil
}

func TestGenericProviderLoadsVersionedAccountAccessProjection(t *testing.T) {
	caller := &fakeRouteCaller{}
	provider := NewGenericProvider(caller)

	projection, err := provider.Load(context.Background(), "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if caller.route != "list-account-accesses" || caller.request["userId"] != "user-1" {
		t.Fatalf("unexpected generic RPC call: route=%q request=%#v", caller.route, caller.request)
	}
	if projection.UserID != "user-1" || projection.PermissionVersion != 7 || len(projection.Accesses) != 2 {
		t.Fatalf("unexpected projection: %#v", projection)
	}
	enterprise := projection.Accesses[1]
	if enterprise.AccountUID != "enterprise-1" || enterprise.OrganizationID != "org-1" || len(enterprise.RoleIDs) != 1 || enterprise.RoleIDs[0] != "finance_member" {
		t.Fatalf("unexpected enterprise access: %#v", enterprise)
	}
}
