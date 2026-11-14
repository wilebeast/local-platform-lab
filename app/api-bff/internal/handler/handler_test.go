package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/accountctx"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/apikey"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/config"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	shared "github.com/wilebeast/local-platform-lab/pkg/accountcontext"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type fakeAPIKeyValidator struct {
	result  apikey.ValidationResult
	request apikey.ValidationRequest
}

func (f *fakeAPIKeyValidator) Validate(_ context.Context, request apikey.ValidationRequest) (apikey.ValidationResult, error) {
	f.request = request
	return f.result, nil
}

type fakeUserClient struct {
	tokenPair           *userv1.TokenPair
	prepare             *userv1.PrepareLoginResponse
	setup               *userv1.TOTPOptionsResponse
	recoveryCodes       []string
	passkeyOptions      *userv1.PasskeyOptionsResponse
	passkeyRegistration *userv1.FinishPasskeyRegistrationResponse
	registrationCode    *userv1.SendRegistrationCodeResponse
	registrationError   error
	registrationCall    func(*userv1.SendRegistrationCodeRequest)
	permissionAllowed   bool
	checkPermission     func(*userv1.CheckPermissionRequest)
}

func (f fakeUserClient) SendRegistrationCode(_ context.Context, request *userv1.SendRegistrationCodeRequest, _ ...grpc.CallOption) (*userv1.SendRegistrationCodeResponse, error) {
	if f.registrationCall != nil {
		f.registrationCall(request)
	}
	return f.registrationCode, f.registrationError
}

func (f fakeUserClient) CheckPermission(_ context.Context, request *userv1.CheckPermissionRequest, _ ...grpc.CallOption) (*userv1.CheckPermissionResponse, error) {
	if f.checkPermission != nil {
		f.checkPermission(request)
	}
	return &userv1.CheckPermissionResponse{Allowed: f.permissionAllowed}, nil
}

type fakeGenericCaller struct{}

type capturingGenericCaller struct {
	metadata metadata.MD
}

func (f *capturingGenericCaller) CallRoute(ctx context.Context, _ string, _ map[string]any) (map[string]any, error) {
	f.metadata, _ = metadata.FromOutgoingContext(ctx)
	return map[string]any{"accountId": "account-enterprise", "status": "active"}, nil
}

type fakeAccountContextManager struct {
	selectedActor accountctx.Actor
	selectedUID   string
	value         shared.Context
}

func (f *fakeAccountContextManager) Select(_ context.Context, actor accountctx.Actor, accountUID string) (shared.Context, error) {
	f.selectedActor, f.selectedUID = actor, accountUID
	value := f.value
	value.UserID, value.SessionID, value.AccountUID = actor.UserID, actor.SessionID, accountUID
	return value, nil
}

func (f *fakeAccountContextManager) Resolve(_ context.Context, _ accountctx.Actor) (shared.Context, error) {
	return f.value, nil
}

type fakeLoginInitializer struct {
	actor accountctx.Actor
}

func (f *fakeLoginInitializer) Initialize(_ context.Context, actor accountctx.Actor) (accountctx.Projection, error) {
	f.actor = actor
	return accountctx.Projection{UserID: actor.UserID, PermissionVersion: 1, Accesses: []accountctx.Access{
		{AccountID: "account-personal", AccountUID: "personal", AccountType: "personal", Relationship: "owner"},
		{AccountID: "account-enterprise", AccountUID: "enterprise", AccountType: "enterprise", Relationship: "member"},
	}}, nil
}

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
		AccessToken: "access-value", RefreshToken: "refresh-value", ExpiresIn: 1800, TokenType: "Bearer", UserId: "user-1", SessionId: "session-1",
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

func TestAuthorizeRejectsAPIKeyWithoutRoutePermission(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v3/profile", nil)
	request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, Actor{
		Type: ActorTypeAPIKey, MerchantID: "local-org", KeyID: "local-key-1",
	}))
	validator := &fakeAPIKeyValidator{result: apikey.ValidationResult{Forbidden: true}}
	called := false

	Authorize(&svc.ServiceContext{APIKeyValidator: validator}, "profile.read", func(http.ResponseWriter, *http.Request) {
		called = true
	}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", recorder.Code, recorder.Body.String())
	}
	if called {
		t.Fatal("protected handler was called after API key permission denial")
	}
	if validator.request.MerchantID != "local-org" || validator.request.KeyID != "local-key-1" || validator.request.RequiredPermission != "profile.read" {
		t.Fatalf("unexpected API key validation request: %#v", validator.request)
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
	manager := &fakeAccountContextManager{value: shared.Context{
		UserID: "user-123", SessionID: "session-123", AccountID: "account-user-123", AccountUID: "personal-user-123",
		AccountType: "personal", PermissionVersion: 1, SelectedAt: time.Now().Unix(),
	}}
	signer, err := shared.NewSigner("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	serviceContext := &svc.ServiceContext{GenericRPC: fakeGenericCaller{}, AccountContexts: manager, AccountContextSigner: signer}
	tests := []struct {
		name    string
		handler http.Handler
		assert  func(map[string]any) bool
	}{
		{"summary", AccountSummaryHandler(serviceContext), func(body map[string]any) bool {
			return body["accountId"] == "account-user-123" && body["status"] == "active"
		}},
		{"limits", AccountLimitsHandler(serviceContext), func(body map[string]any) bool {
			return body["dailyLimitCents"] == float64(100000) && body["currency"] == "USD"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/v3/account/"+test.name, nil)
			request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, Actor{UserID: "user-123", SessionID: "session-123"}))
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

func TestSelectAccountContextStoresSelectionForAuthenticatedSession(t *testing.T) {
	manager := &fakeAccountContextManager{value: shared.Context{AccountID: "account-enterprise", AccountType: "enterprise", PermissionVersion: 3, SelectedAt: 100}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v3/auth/account-context/select", strings.NewReader(`{"accountUid":"enterprise"}`))
	request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, Actor{UserID: "user-1", SessionID: "session-phone"}))

	SelectAccountContextHandler(&svc.ServiceContext{AccountContexts: manager}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || manager.selectedActor.SessionID != "session-phone" || manager.selectedUID != "enterprise" {
		t.Fatalf("unexpected selection: status=%d actor=%#v uid=%q body=%s", recorder.Code, manager.selectedActor, manager.selectedUID, recorder.Body.String())
	}
}

func TestAccountHandlerSignsResolvedAccountContextIntoOutgoingRPCMetadata(t *testing.T) {
	manager := &fakeAccountContextManager{value: shared.Context{
		UserID: "user-1", SessionID: "session-phone", AccountID: "account-enterprise", AccountUID: "enterprise",
		AccountType: "enterprise", PermissionVersion: 3, SelectedAt: time.Now().Unix(),
	}}
	signer, err := shared.NewSigner("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	caller := &capturingGenericCaller{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v3/account/summary", nil)
	request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, Actor{UserID: "user-1", SessionID: "session-phone"}))

	AccountSummaryHandler(&svc.ServiceContext{GenericRPC: caller, AccountContexts: manager, AccountContextSigner: signer}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
	}
	if len(caller.metadata.Get("x-account-context")) != 1 || len(caller.metadata.Get("x-account-context-signature")) != 1 {
		t.Fatalf("signed account context was not propagated: %#v", caller.metadata)
	}
}
