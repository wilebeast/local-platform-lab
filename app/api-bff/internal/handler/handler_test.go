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
	tokenPair           *userv1.TokenPair
	prepare             *userv1.PrepareLoginResponse
	setup               *userv1.TOTPOptionsResponse
	recoveryCodes       []string
	passkeyOptions      *userv1.PasskeyOptionsResponse
	passkeyRegistration *userv1.FinishPasskeyRegistrationResponse
	permissionAllowed   bool
	checkPermission     func(*userv1.CheckPermissionRequest)
}

func (f fakeUserClient) CheckPermission(_ context.Context, request *userv1.CheckPermissionRequest, _ ...grpc.CallOption) (*userv1.CheckPermissionResponse, error) {
	if f.checkPermission != nil {
		f.checkPermission(request)
	}
	return &userv1.CheckPermissionResponse{Allowed: f.permissionAllowed}, nil
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
func (f fakeUserClient) BeginPasskeyRegistration(_ context.Context, _ *userv1.BeginPasskeyRegistrationRequest, _ ...grpc.CallOption) (*userv1.PasskeyOptionsResponse, error) {
	return f.passkeyOptions, nil
}
func (f fakeUserClient) FinishPasskeyRegistration(_ context.Context, _ *userv1.FinishPasskeyRegistrationRequest, _ ...grpc.CallOption) (*userv1.FinishPasskeyRegistrationResponse, error) {
	return f.passkeyRegistration, nil
}
func (f fakeUserClient) BeginPasskeyLogin(_ context.Context, _ *userv1.BeginPasskeyLoginRequest, _ ...grpc.CallOption) (*userv1.PasskeyOptionsResponse, error) {
	return f.passkeyOptions, nil
}
func (f fakeUserClient) FinishPasskeyLogin(_ context.Context, _ *userv1.FinishPasskeyLoginRequest, _ ...grpc.CallOption) (*userv1.TokenPair, error) {
	return f.tokenPair, nil
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

func TestAuthorizeRejectsMissingPermission(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v3/profile", nil)
	request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, Actor{UserID: "user-123"}))
	called := false

	Authorize(&svc.ServiceContext{
		UserClient: fakeUserClient{permissionAllowed: false}, GenericRPC: fakeGenericCaller{}, RBACOrganizationID: "local-org",
	}, "profile.read", func(http.ResponseWriter, *http.Request) { called = true }).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if called {
		t.Fatal("protected handler was called after permission denial")
	}
}

func TestAuthorizePassesTrustedPermissionContext(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v3/profile", nil)
	request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, Actor{UserID: "user-123"}))
	called := false
	var checked *userv1.CheckPermissionRequest

	Authorize(&svc.ServiceContext{
		UserClient: fakeUserClient{permissionAllowed: true, checkPermission: func(request *userv1.CheckPermissionRequest) {
			checked = request
		}},
		RBACOrganizationID: "org-456",
	}, "profile.read", func(http.ResponseWriter, *http.Request) { called = true }).ServeHTTP(recorder, request)

	if !called {
		t.Fatal("protected handler was not called after permission approval")
	}
	if checked.GetUserId() != "user-123" || checked.GetOrganizationId() != "org-456" || checked.GetPermissionKey() != "profile.read" {
		t.Fatalf("unexpected permission context: %#v", checked)
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
