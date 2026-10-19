package server

import (
	"context"

	accountv1 "github.com/wilebeast/local-platform-lab/api/account/v1"
)

type AccountServer struct {
	accountv1.UnimplementedAccountServiceServer
}

func NewAccountServer() *AccountServer {
	return &AccountServer{}
}

func (s *AccountServer) GetSummary(_ context.Context, request *accountv1.GetSummaryRequest) (*accountv1.GetSummaryResponse, error) {
	return &accountv1.GetSummaryResponse{AccountId: "account-" + request.GetUserId(), Status: "active"}, nil
}

func (s *AccountServer) GetLimits(_ context.Context, _ *accountv1.GetLimitsRequest) (*accountv1.GetLimitsResponse, error) {
	return &accountv1.GetLimitsResponse{DailyLimitCents: 100000, Currency: "USD"}, nil
}
