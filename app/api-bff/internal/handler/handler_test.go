package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/config"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"google.golang.org/grpc"
)

type fakeUserClient struct{ tokenPair *userv1.TokenPair }

type fakeGenericCaller struct{}

func (fakeGenericCaller) Call(_ context.Context, method string, request map[string]any) (map[string]any, error) {
	if method != "/user.v1.UserService/GetProfile" {
		return nil, fmt.Errorf("unexpected method: %s", method)
	}
	return map[string]any{
		"userId":             request["userId"],
		"verificationStatus": "verified",
	}, nil
}

func (fakeUserClient) GetProfile(_ context.Context, request *userv1.GetProfileRequest, _ ...grpc.CallOption) (*userv1.GetProfileResponse, error) {
	return &userv1.GetProfileResponse{
		UserId:             request.GetUserId(),
		VerificationStatus: "verified",
	}, nil
}
func (f fakeUserClient) Login(_ context.Context, _ *userv1.LoginRequest, _ ...grpc.CallOption) (*userv1.TokenPair, error) {
	if f.tokenPair != nil {
		return f.tokenPair, nil
	}
	return &userv1.TokenPair{}, nil
}

func testServiceContextWithTokens() *svc.ServiceContext {
	return &svc.ServiceContext{UserClient: fakeUserClient{tokenPair: &userv1.TokenPair{
		AccessToken: "access-value", RefreshToken: "refresh-value", ExpiresIn: 1800, TokenType: "Bearer",
	}}}
}
func (fakeUserClient) Refresh(_ context.Context, _ *userv1.RefreshRequest, _ ...grpc.CallOption) (*userv1.TokenPair, error) {
	return &userv1.TokenPair{}, nil
}
func (fakeUserClient) Logout(_ context.Context, _ *userv1.LogoutRequest, _ ...grpc.CallOption) (*userv1.LogoutResponse, error) {
	return &userv1.LogoutResponse{}, nil
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

func TestProfileHandlerUsesAuthenticatedActor(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v3/profile", nil)
	request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, Actor{UserID: "user-123"}))

	ProfileHandler(&svc.ServiceContext{
		GenericRPC:    fakeGenericCaller{},
		ProfileMethod: "/user.v1.UserService/GetProfile",
	}).ServeHTTP(recorder, request)

	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["userId"] != "user-123" || body["verificationStatus"] != "verified" || body["source"] != "user-service-generic-grpc" {
		t.Fatalf("unexpected response: %#v", body)
	}
}

func TestProfileHandlerRejectsRawUserHeader(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v3/profile", nil)
	request.Header.Set("X-User-ID", "forged-user")
	ProfileHandler(&svc.ServiceContext{GenericRPC: fakeGenericCaller{}, ProfileMethod: "/user.v1.UserService/GetProfile"}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
}
