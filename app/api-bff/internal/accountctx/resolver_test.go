package accountctx

import (
	"context"
	"testing"
	"time"
)

func TestResolverSharesAccessProjectionButKeepsSelectionPerSession(t *testing.T) {
	store := newMemoryStore()
	resolver := NewResolver(store, time.Hour)
	projection := Projection{
		UserID: "user-1", PermissionVersion: 7,
		Accesses: []Access{
			{AccountID: "account-personal", AccountUID: "personal", AccountType: "personal", Relationship: "owner"},
			{AccountID: "account-enterprise", AccountUID: "enterprise", AccountType: "enterprise", OrganizationID: "org-2", MemberID: "member-8", RoleIDs: []string{"finance_admin"}, Relationship: "member"},
		},
	}
	if err := resolver.Prime(context.Background(), projection); err != nil {
		t.Fatal(err)
	}
	phone := Actor{UserID: "user-1", SessionID: "session-phone"}
	desktop := Actor{UserID: "user-1", SessionID: "session-desktop"}
	if _, err := resolver.Select(context.Background(), phone, "personal"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Select(context.Background(), desktop, "enterprise"); err != nil {
		t.Fatal(err)
	}

	phoneContext, err := resolver.Resolve(context.Background(), phone)
	if err != nil {
		t.Fatal(err)
	}
	desktopContext, err := resolver.Resolve(context.Background(), desktop)
	if err != nil {
		t.Fatal(err)
	}
	if phoneContext.AccountUID != "personal" || desktopContext.AccountUID != "enterprise" {
		t.Fatalf("unexpected selections: phone=%+v desktop=%+v", phoneContext, desktopContext)
	}
	if desktopContext.OrganizationID != "org-2" || desktopContext.MemberID != "member-8" || desktopContext.PermissionVersion != 7 {
		t.Fatalf("unexpected enterprise context: %+v", desktopContext)
	}
	if store.projectionWrites != 1 || len(store.selections) != 2 {
		t.Fatalf("expected one shared projection and two session selections: writes=%d selections=%d", store.projectionWrites, len(store.selections))
	}
}

type memoryStore struct {
	projection       Projection
	projectionWrites int
	selections       map[string]Selection
}

func newMemoryStore() *memoryStore { return &memoryStore{selections: make(map[string]Selection)} }

func (s *memoryStore) PutProjection(_ context.Context, value Projection, _ time.Duration) error {
	s.projection = value
	s.projectionWrites++
	return nil
}

func (s *memoryStore) GetProjection(_ context.Context, _ string) (Projection, error) {
	return s.projection, nil
}

func (s *memoryStore) PutSelection(_ context.Context, sessionID string, value Selection, _ time.Duration) error {
	s.selections[sessionID] = value
	return nil
}

func (s *memoryStore) GetSelection(_ context.Context, sessionID string) (Selection, error) {
	return s.selections[sessionID], nil
}
