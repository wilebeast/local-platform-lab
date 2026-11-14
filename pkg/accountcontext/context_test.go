package accountcontext

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestUnaryServerInterceptorMakesVerifiedAccountContextAvailable(t *testing.T) {
	signer, err := NewSigner(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	want := Context{
		UserID: "user-1", SessionID: "session-desktop",
		AccountID: "account-enterprise-2", AccountUID: "enterprise-2", AccountType: "enterprise",
		OrganizationID: "org-2", MemberID: "member-8", RoleIDs: []string{"finance_admin"},
		PermissionVersion: 7, SelectedAt: 1_699_999_900,
	}
	outgoing, err := signer.Attach(context.Background(), want)
	if err != nil {
		t.Fatal(err)
	}
	md, ok := metadata.FromOutgoingContext(outgoing)
	if !ok {
		t.Fatal("outgoing metadata was not attached")
	}
	incoming := metadata.NewIncomingContext(context.Background(), md)
	interceptor, err := UnaryServerInterceptor(testSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	_, err = interceptor(incoming, nil, &grpc.UnaryServerInfo{FullMethod: "/account.v1.AccountService/GetSummary"}, func(ctx context.Context, _ any) (any, error) {
		got, ok := FromContext(ctx)
		if !ok {
			t.Fatal("verified account context was not injected")
		}
		if got.UserID != want.UserID || got.SessionID != want.SessionID || got.AccountID != want.AccountID || got.AccountUID != want.AccountUID || got.OrganizationID != want.OrganizationID || got.MemberID != want.MemberID || got.PermissionVersion != want.PermissionVersion || len(got.RoleIDs) != 1 || got.RoleIDs[0] != "finance_admin" {
			t.Fatalf("unexpected account context: %+v", got)
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnaryServerInterceptorRejectsTamperedAccountContext(t *testing.T) {
	signer, err := NewSigner(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	outgoing, err := signer.Attach(context.Background(), Context{
		UserID: "user-1", SessionID: "session-1", AccountID: "account-personal", AccountUID: "personal",
		AccountType: "personal", PermissionVersion: 1, SelectedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	md, _ := metadata.FromOutgoingContext(outgoing)
	payload, err := base64.RawURLEncoding.DecodeString(md.Get(metadataContextKey)[0])
	if err != nil {
		t.Fatal(err)
	}
	for index := range payload {
		if payload[index] == 'p' {
			payload[index] = 'e'
			break
		}
	}
	md.Set(metadataContextKey, base64.RawURLEncoding.EncodeToString(payload))
	interceptor, err := UnaryServerInterceptor(testSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, err = interceptor(metadata.NewIncomingContext(context.Background(), md), nil, &grpc.UnaryServerInfo{FullMethod: "/account.v1.AccountService/GetSummary"}, func(context.Context, any) (any, error) {
		t.Fatal("handler must not run for tampered account context")
		return nil, nil
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated, got %v", err)
	}
}

func TestUnaryServerInterceptorAllowsExplicitPublicMethodWithoutAccountContext(t *testing.T) {
	const healthMethod = "/grpc.health.v1.Health/Check"
	interceptor, err := UnaryServerInterceptor(testSecret, time.Minute, healthMethod)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: healthMethod}, func(context.Context, any) (any, error) {
		called = true
		return "serving", nil
	})
	if err != nil || !called {
		t.Fatalf("public health method was blocked: called=%v err=%v", called, err)
	}
}
