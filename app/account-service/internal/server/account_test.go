package server

import (
	"context"
	"testing"

	accountv1 "github.com/wilebeast/local-platform-lab/api/account/v1"
)

func TestAccountServerSupportsBothUnaryMethods(t *testing.T) {
	server := NewAccountServer()

	summary, err := server.GetSummary(context.Background(), &accountv1.GetSummaryRequest{UserId: "user-42"})
	if err != nil {
		t.Fatal(err)
	}
	if summary.GetAccountId() != "account-user-42" || summary.GetStatus() != "active" {
		t.Fatalf("unexpected summary: %#v", summary)
	}

	limits, err := server.GetLimits(context.Background(), &accountv1.GetLimitsRequest{UserId: "user-42"})
	if err != nil {
		t.Fatal(err)
	}
	if limits.GetDailyLimitCents() != 100000 || limits.GetCurrency() != "USD" {
		t.Fatalf("unexpected limits: %#v", limits)
	}
}
