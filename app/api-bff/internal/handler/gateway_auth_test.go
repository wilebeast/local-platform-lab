package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/authn"
)

type fakeRequestValidator struct {
	actor authn.Actor
	err   error
}

func (v fakeRequestValidator) ValidateRequest(context.Context, *http.Request) (authn.Actor, error) {
	return v.actor, v.err
}

func signGatewayRequest(request *http.Request, secret string, timestamp int64) {
	timestampValue := strconv.FormatInt(timestamp, 10)
	request.Header.Set("X-Gateway", "apisix")
	request.Header.Set("X-Gateway-Timestamp", timestampValue)
	payload := strings.Join([]string{request.Method, request.URL.RequestURI(), timestampValue, request.Header.Get("X-User-ID")}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	request.Header.Set("X-Gateway-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
}

func TestGatewayAuthRejectsForgedIdentity(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v3/profile", nil)
	request.Header.Set("X-User-ID", "forged-user")
	recorder := httptest.NewRecorder()
	GatewayAuth("01234567890123456789012345678901", fakeRequestValidator{err: authn.ErrInvalidAccessToken}, true, func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not be called")
	})(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
}

func TestGatewayAuthRejectsRequestWithoutGatewaySignature(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v3/profile", nil)
	recorder := httptest.NewRecorder()
	validator := fakeRequestValidator{actor: authn.Actor{UserID: "token-user", SessionID: "session-1", TokenID: "token-1", UserVersion: 1}}
	GatewayAuth("01234567890123456789012345678901", validator, true, func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not be called")
	})(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
}

func TestGatewayAuthUsesValidatedTokenIdentityInsteadOfGatewayHeader(t *testing.T) {
	secret := "01234567890123456789012345678901"
	request := httptest.NewRequest(http.MethodGet, "/api/v3/profile?view=full", nil)
	request.Header.Set("X-User-ID", "user-123")
	request.Header.Set("X-Authenticated", "true")
	signGatewayRequest(request, secret, time.Now().Unix())
	recorder := httptest.NewRecorder()
	validator := fakeRequestValidator{actor: authn.Actor{UserID: "token-user", SessionID: "session-1", TokenID: "token-1", UserVersion: 1}}
	GatewayAuth(secret, validator, true, func(_ http.ResponseWriter, r *http.Request) {
		actor, ok := actorFromContext(r.Context())
		if !ok || actor.UserID != "token-user" || actor.SessionID != "session-1" {
			t.Fatalf("unexpected actor: %+v", actor)
		}
	})(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
}
