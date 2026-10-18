package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
