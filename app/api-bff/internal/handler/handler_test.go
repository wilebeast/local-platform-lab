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

type fakeUserClient struct {
	tokenPair     *userv1.TokenPair
	prepare       *userv1.PrepareLoginResponse
	setup         *userv1.TOTPOptionsResponse
	recoveryCodes []string
}

type fakeGenericCaller struct{}

func (fakeGenericCaller) CallRoute(_ context.Context, route string, request map[string]any) (map[string]any, error) {
	switch route {
	case "get-profile":
		return map[string]any{"userId": request["userId"], "verificationStatus": "verified"}, nil
	case "get-account-summary":
		return map[string]any{"accountId": "account-" + request["userId"].(string), "status": "active"}, nil
	case "get-account-limits":
		return map[string]any{"dailyLimitCents": "100000", "currency": "USD"}, nil
	default:
		return nil, fmt.Errorf("unexpected route: %s", route)
	}
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
func (f fakeUserClient) PrepareLogin(_ context.Context, _ *userv1.PrepareLoginRequest, _ ...grpc.CallOption) (*userv1.PrepareLoginResponse, error) {
	if f.prepare != nil {
		return f.prepare, nil
	}
	return &userv1.PrepareLoginResponse{TokenPair: f.tokenPair}, nil
}
func (f fakeUserClient) VerifyLogin2FA(_ context.Context, _ *userv1.VerifyLogin2FARequest, _ ...grpc.CallOption) (*userv1.TokenPair, error) {
	return f.tokenPair, nil
}
func (f fakeUserClient) TOTPOptions(_ context.Context, _ *userv1.TOTPOptionsRequest, _ ...grpc.CallOption) (*userv1.TOTPOptionsResponse, error) {
	return f.setup, nil
}
func (f fakeUserClient) ConfirmTOTP(_ context.Context, _ *userv1.ConfirmTOTPRequest, _ ...grpc.CallOption) (*userv1.RecoveryCodesResponse, error) {
	return &userv1.RecoveryCodesResponse{RecoveryCodes: f.recoveryCodes}, nil
}
func (f fakeUserClient) UnbindTOTP(_ context.Context, _ *userv1.UnbindTOTPRequest, _ ...grpc.CallOption) (*userv1.UnbindTOTPResponse, error) {
	return &userv1.UnbindTOTPResponse{Unbound: true}, nil
}
func (f fakeUserClient) RegenerateRecoveryCodes(_ context.Context, _ *userv1.RegenerateRecoveryCodesRequest, _ ...grpc.CallOption) (*userv1.RecoveryCodesResponse, error) {
	return &userv1.RecoveryCodesResponse{RecoveryCodes: f.recoveryCodes}, nil
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
		GenericRPC: fakeGenericCaller{},
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
	ProfileHandler(&svc.ServiceContext{GenericRPC: fakeGenericCaller{}}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
}

func TestAccountHandlersUseAuthenticatedActor(t *testing.T) {
	tests := []struct {
		name    string
		handler http.Handler
		assert  func(map[string]any) bool
	}{
		{"summary", AccountSummaryHandler(&svc.ServiceContext{GenericRPC: fakeGenericCaller{}}), func(body map[string]any) bool {
			return body["accountId"] == "account-user-123" && body["status"] == "active"
		}},
		{"limits", AccountLimitsHandler(&svc.ServiceContext{GenericRPC: fakeGenericCaller{}}), func(body map[string]any) bool {
			return body["dailyLimitCents"] == float64(100000) && body["currency"] == "USD"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/v3/account/"+test.name, nil)
			request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, Actor{UserID: "user-123"}))
			test.handler.ServeHTTP(recorder, request)
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if recorder.Code != http.StatusOK || !test.assert(body) || body["source"] != "account-service-generic-grpc" {
				t.Fatalf("unexpected response: status=%d body=%#v", recorder.Code, body)
			}
		})
	}
}
