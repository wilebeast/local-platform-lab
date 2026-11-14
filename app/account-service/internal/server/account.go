package server

import (
	"context"
	"strings"

	accountv1 "github.com/wilebeast/local-platform-lab/api/account/v1"
	shared "github.com/wilebeast/local-platform-lab/pkg/accountcontext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type AccountServer struct {
	accountv1.UnimplementedAccountServiceServer
}

func NewAccountServer() *AccountServer {
	return &AccountServer{}
}

func (s *AccountServer) ListAccesses(_ context.Context, request *accountv1.ListAccessesRequest) (*accountv1.ListAccessesResponse, error) {
	userID := strings.TrimSpace(request.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user id is required")
	}
	return &accountv1.ListAccessesResponse{
		PermissionVersion: 1,
		Accesses: []*accountv1.AccountAccess{
			{
				AccountId: "account-personal-" + userID, AccountUid: "personal-" + userID,
				AccountType: "personal", Relationship: "owner",
			},
			{
				AccountId: "account-enterprise-local", AccountUid: "enterprise-local",
				AccountType: "enterprise", OrganizationId: "org-local", MemberId: "member-" + userID,
				RoleIds: []string{"finance_member"}, Relationship: "member",
			},
		},
	}, nil
}

func (s *AccountServer) GetSummary(ctx context.Context, _ *accountv1.GetSummaryRequest) (*accountv1.GetSummaryResponse, error) {
	accountContext, ok := shared.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "account context is required")
	}
	return &accountv1.GetSummaryResponse{AccountId: accountContext.AccountID, Status: "active"}, nil
}

func (s *AccountServer) GetLimits(ctx context.Context, _ *accountv1.GetLimitsRequest) (*accountv1.GetLimitsResponse, error) {
	if _, ok := shared.FromContext(ctx); !ok {
		return nil, status.Error(codes.Unauthenticated, "account context is required")
	}
	return &accountv1.GetLimitsResponse{DailyLimitCents: 100000, Currency: "USD"}, nil
}
