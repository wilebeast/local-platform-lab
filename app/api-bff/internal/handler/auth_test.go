package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
)

func TestBrowserOriginRejectsMissingAndWrongOrigin(t *testing.T) {
	called := false
	handler := BrowserOrigin("https://localhost:18443", func(http.ResponseWriter, *http.Request) { called = true })
	for _, origin := range []string{"", "https://evil.example"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v3/auth/login", nil)
		request.Header.Set("Origin", origin)
		handler(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("origin %q: expected 403, got %d", origin, recorder.Code)
		}
	}
	if called {
		t.Fatal("protected handler must not be called")
	}
}

func TestCSRFRequiresMatchingCookieAndHeader(t *testing.T) {
	called := false
	handler := CSRF(func(http.ResponseWriter, *http.Request) { called = true })
	request := httptest.NewRequest(http.MethodPost, "/api/v3/auth/refresh", nil)
	request.AddCookie(&http.Cookie{Name: "csrf_token", Value: "expected"})
	request.Header.Set("X-CSRF-Token", "wrong")
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != http.StatusForbidden || called {
		t.Fatalf("mismatched CSRF must be rejected")
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v3/auth/refresh", nil)
	request.AddCookie(&http.Cookie{Name: "csrf_token", Value: "expected"})
	request.Header.Set("X-CSRF-Token", "expected")
	recorder = httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != http.StatusOK || !called {
		t.Fatalf("matching CSRF must pass")
	}
}

func TestLoginResponseUsesSecureHttpOnlyCookiesWithoutTokensInJSON(t *testing.T) {
	serviceContext := testServiceContextWithTokens()
	request := httptest.NewRequest(http.MethodPost, "/api/v3/auth/login", strings.NewReader(`{"email":"demo@local.test","password":"local-demo-password"}`))
	recorder := httptest.NewRecorder()
	LoginHandler(serviceContext)(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "access-value") || strings.Contains(recorder.Body.String(), "refresh-value") {
		t.Fatal("tokens must not be present in JSON response")
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 3 {
		t.Fatalf("expected three cookies, got %d", len(cookies))
	}
	for _, cookie := range cookies {
		if !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
			t.Fatalf("cookie %s is missing secure attributes", cookie.Name)
		}
		if cookie.Name != "csrf_token" && !cookie.HttpOnly {
			t.Fatalf("cookie %s must be HttpOnly", cookie.Name)
		}
	}
}

func TestLoginReturnsSecondFactorChallengeWithoutCookies(t *testing.T) {
	serviceContext := &svc.ServiceContext{UserClient: fakeUserClient{prepare: &userv1.PrepareLoginResponse{
		SecondFactorRequired: true, AuthSessionToken: "auth-session",
	}}}
	request := httptest.NewRequest(http.MethodPost, "/api/v3/auth/login", strings.NewReader(`{"email":"demo@local.test","password":"local-demo-password"}`))
	recorder := httptest.NewRecorder()
	LoginHandler(serviceContext)(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"secondFactorRequired":true`) || !strings.Contains(recorder.Body.String(), `"authSessionToken":"auth-session"`) {
		t.Fatalf("unexpected challenge response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(recorder.Result().Cookies()) != 0 {
		t.Fatal("2FA challenge must not issue cookies")
	}
}

func TestVerifyLogin2FASetsTokensInSecureCookies(t *testing.T) {
	serviceContext := testServiceContextWithTokens()
	request := httptest.NewRequest(http.MethodPost, "/api/v3/auth/login/verify-2fa", strings.NewReader(`{"authSessionToken":"session","code":"123456"}`))
	recorder := httptest.NewRecorder()
	VerifyLogin2FAHandler(serviceContext)(recorder, request)
	if recorder.Code != http.StatusOK || len(recorder.Result().Cookies()) != 3 {
		t.Fatalf("expected token cookies after second factor: status=%d cookies=%d", recorder.Code, len(recorder.Result().Cookies()))
	}
}

func TestTOTPOptionsUsesAuthenticatedActor(t *testing.T) {
	serviceContext := &svc.ServiceContext{UserClient: fakeUserClient{setup: &userv1.TOTPOptionsResponse{SetupToken: "setup", OtpauthUri: "otpauth://totp/Wildbeast"}}}
	request := httptest.NewRequest(http.MethodPost, "/api/v3/auth/2fa/totp/options", nil)
	request = request.WithContext(context.WithValue(request.Context(), actorContextKey{}, Actor{UserID: "local-user"}))
	recorder := httptest.NewRecorder()
	TOTPOptionsHandler(serviceContext)(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "otpauth://") {
		t.Fatalf("unexpected options response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
