package server

import (
	"context"
	"testing"
	"time"

	accountv1 "github.com/wilebeast/local-platform-lab/api/account/v1"
	shared "github.com/wilebeast/local-platform-lab/pkg/accountcontext"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestAccountServerRejectsBusinessRPCWithoutAccountContext(t *testing.T) {
	server := NewAccountServer()
	if _, err := server.GetSummary(context.Background(), &accountv1.GetSummaryRequest{UserId: "user-42"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated without account context, got %v", err)
	}
}

func TestAccountServerUsesVerifiedAccountContextInsteadOfRequestIdentity(t *testing.T) {
	secret := "01234567890123456789012345678901"
	signer, err := shared.NewSigner(secret)
	if err != nil {
		t.Fatal(err)
	}
	outgoing, err := signer.Attach(context.Background(), shared.Context{
		UserID: "user-42", SessionID: "session-1", AccountID: "account-enterprise-1", AccountUID: "enterprise-1",
		AccountType: "enterprise", PermissionVersion: 4, SelectedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	md, _ := metadata.FromOutgoingContext(outgoing)
	incoming := metadata.NewIncomingContext(context.Background(), md)
	interceptor, err := shared.UnaryServerInterceptor(secret, time.Minute, accountv1.AccountService_ListAccesses_FullMethodName)
	if err != nil {
		t.Fatal(err)
	}
	server := NewAccountServer()
	response, err := interceptor(incoming, &accountv1.GetSummaryRequest{UserId: "forged-user"}, &grpc.UnaryServerInfo{FullMethod: accountv1.AccountService_GetSummary_FullMethodName}, func(ctx context.Context, request any) (any, error) {
		return server.GetSummary(ctx, request.(*accountv1.GetSummaryRequest))
	})
	if err != nil {
		t.Fatal(err)
	}
	summary := response.(*accountv1.GetSummaryResponse)
	if summary.GetAccountId() != "account-enterprise-1" || summary.GetStatus() != "active" {
		t.Fatalf("request identity overrode verified account context: %#v", summary)
	}

	limits, err := interceptor(incoming, &accountv1.GetLimitsRequest{UserId: "forged-user"}, &grpc.UnaryServerInfo{FullMethod: accountv1.AccountService_GetLimits_FullMethodName}, func(ctx context.Context, request any) (any, error) {
		return server.GetLimits(ctx, request.(*accountv1.GetLimitsRequest))
	})
	if err != nil {
		t.Fatal(err)
	}
	if limits.(*accountv1.GetLimitsResponse).GetDailyLimitCents() != 100000 || limits.(*accountv1.GetLimitsResponse).GetCurrency() != "USD" {
		t.Fatalf("unexpected limits: %#v", limits)
	}
}

func TestAccountServerListsUserAccountAccesses(t *testing.T) {
	server := NewAccountServer()

	result, err := server.ListAccesses(context.Background(), &accountv1.ListAccessesRequest{UserId: "user-42"})
	if err != nil {
		t.Fatal(err)
	}
	if result.GetPermissionVersion() != 1 || len(result.GetAccesses()) != 2 {
		t.Fatalf("unexpected accesses: %+v", result)
	}
	personal, enterprise := result.GetAccesses()[0], result.GetAccesses()[1]
	if personal.GetAccountUid() != "personal-user-42" || personal.GetRelationship() != "owner" {
		t.Fatalf("unexpected personal access: %+v", personal)
	}
	if enterprise.GetAccountUid() != "enterprise-local" || enterprise.GetOrganizationId() != "org-local" || enterprise.GetMemberId() != "member-user-42" || len(enterprise.GetRoleIds()) != 1 || enterprise.GetRoleIds()[0] != "finance_member" {
		t.Fatalf("unexpected enterprise access: %+v", enterprise)
	}
}
