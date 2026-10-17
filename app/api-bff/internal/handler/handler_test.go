package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/config"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"google.golang.org/grpc"
)

type fakeUserClient struct{}

func (fakeUserClient) GetProfile(_ context.Context, request *userv1.GetProfileRequest, _ ...grpc.CallOption) (*userv1.GetProfileResponse, error) {
	return &userv1.GetProfileResponse{
		UserId:             request.GetUserId(),
		VerificationStatus: "verified",
	}, nil
}

func TestHealthHandler(t *testing.T) {
	ctx := svc.NewServiceContext(config.Config{})
	ctx.Config.Name = "api-bff"
	recorder := httptest.NewRecorder()

	HealthHandler(ctx).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "ok" || body["service"] != "api-bff" {
		t.Fatalf("unexpected response: %#v", body)
	}
}

func TestProfileHandlerUsesUserHeader(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v3/profile", nil)
	request.Header.Set("X-User-ID", "user-123")

	ProfileHandler(&svc.ServiceContext{UserClient: fakeUserClient{}}).ServeHTTP(recorder, request)

	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["userId"] != "user-123" || body["verificationStatus"] != "verified" || body["source"] != "user-service-grpc" {
		t.Fatalf("unexpected response: %#v", body)
	}
}
