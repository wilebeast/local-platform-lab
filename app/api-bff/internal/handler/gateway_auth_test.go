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

	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/apikey"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/authn"
)

type fakeEd25519Authenticator struct {
	result apikey.AuthenticationResult
	seen   apikey.Ed25519Request
}

func (f *fakeEd25519Authenticator) Authenticate(_ context.Context, request apikey.Ed25519Request) (apikey.AuthenticationResult, error) {
	f.seen = request
	return f.result, nil
}

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

func TestAPIKeyGatewayAuthBuildsActorFromSignedGatewayContext(t *testing.T) {
	secret := "01234567890123456789012345678901"
	request := httptest.NewRequest(http.MethodGet, "/api/v3/profile", nil)
	request.Header.Set("X-Authenticated", "true")
	request.Header.Set("X-Auth-Method", "hmac")
	request.Header.Set("X-Merchant-ID", "local-org")
	request.Header.Set("X-API-Key-ID", "local-key-1")
	signAPIKeyGatewayRequest(request, secret, time.Now().Unix())
	recorder := httptest.NewRecorder()

	APIKeyGatewayAuth(secret, func(_ http.ResponseWriter, request *http.Request) {
		actor, ok := actorFromContext(request.Context())
		if !ok || actor.Type != ActorTypeAPIKey || actor.MerchantID != "local-org" || actor.KeyID != "local-key-1" {
			t.Fatalf("unexpected actor: %+v", actor)
		}
	})(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestEd25519APIKeyAuthBuildsActorAfterSignatureVerification(t *testing.T) {
	secret := "gateway-signature-secret-at-least-32-bytes"
	request := httptest.NewRequest(http.MethodGet, "/api/v3/open/v2/profile", nil)
	request.Header.Set("X-API-Key-ID", "local-ed25519-1")
	request.Header.Set("X-API-Timestamp", "1792650000")
	request.Header.Set("X-API-Nonce", "AAAAAAAAAAAAAAAAAAAAAA")
	request.Header.Set("Digest", "SHA-256=47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=")
	request.Header.Set("Authorization", `Ed25519 signature="signature"`)
	request.Header.Set("X-Auth-Method", "ed25519")
	signAPIKeyGatewayRequest(request, secret, time.Now().Unix())
	authenticator := &fakeEd25519Authenticator{result: apikey.AuthenticationResult{Valid: true, MerchantID: "local-org"}}

	reached := false
	Ed25519APIKeyAuth(secret, authenticator, func(_ http.ResponseWriter, request *http.Request) {
		reached = true
		actor, ok := actorFromContext(request.Context())
		if !ok || actor.MerchantID != "local-org" || actor.KeyID != "local-ed25519-1" {
			t.Fatalf("unexpected actor: %+v", actor)
		}
	}).ServeHTTP(httptest.NewRecorder(), request)

	if !reached || authenticator.seen.ComputedDigest != request.Header.Get("Digest") {
		t.Fatalf("expected authenticated handler and verified body digest, reached=%v seen=%+v", reached, authenticator.seen)
	}
}

func signAPIKeyGatewayRequest(request *http.Request, secret string, timestamp int64) {
	timestampValue := strconv.FormatInt(timestamp, 10)
	request.Header.Set("X-Gateway", "apisix")
	request.Header.Set("X-Gateway-Timestamp", timestampValue)
	payload := strings.Join([]string{
		request.Method, request.URL.RequestURI(), timestampValue,
		request.Header.Get("X-Merchant-ID"), request.Header.Get("X-API-Key-ID"),
	}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	request.Header.Set("X-Gateway-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
}
