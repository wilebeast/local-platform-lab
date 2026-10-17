package server

import (
	"context"
	"testing"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
)

func TestGetProfile(t *testing.T) {
	response, err := NewUserServer().GetProfile(context.Background(), &userv1.GetProfileRequest{UserId: "user-123"})
	if err != nil {
		t.Fatalf("GetProfile returned error: %v", err)
	}
	if response.GetUserId() != "user-123" || response.GetVerificationStatus() != "not_started" {
		t.Fatalf("unexpected response: %#v", response)
	}
}
