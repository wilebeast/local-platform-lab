package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/authn"
)

type actorContextKey struct{}

type Actor struct {
	UserID      string
	SessionID   string
	TokenID     string
	UserVersion int64
}

type requestValidator interface {
	ValidateRequest(context.Context, *http.Request) (authn.Actor, error)
}

func GatewayAuth(secret string, validator requestValidator, requireUser bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validGatewayRequest(r, secret, time.Now()) {
			writeError(w, http.StatusUnauthorized, "untrusted gateway request")
			return
		}
		actor := Actor{}
		if requireUser {
			if validator == nil {
				writeError(w, http.StatusInternalServerError, "authentication is not configured")
				return
			}
			validated, err := validator.ValidateRequest(r.Context(), r)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "invalid access token")
				return
			}
			actor = Actor{UserID: validated.UserID, SessionID: validated.SessionID, TokenID: validated.TokenID, UserVersion: validated.UserVersion}
		}
		ctx := context.WithValue(r.Context(), actorContextKey{}, actor)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func actorFromContext(ctx context.Context) (Actor, bool) {
	actor, ok := ctx.Value(actorContextKey{}).(Actor)
	return actor, ok && actor.UserID != ""
}

func validGatewayRequest(r *http.Request, secret string, now time.Time) bool {
	if len(secret) < 32 || r.Header.Get("X-Gateway") != "apisix" {
		return false
	}
	timestamp := r.Header.Get("X-Gateway-Timestamp")
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || now.Sub(time.Unix(unix, 0)) > 5*time.Minute || time.Unix(unix, 0).Sub(now) > 5*time.Minute {
		return false
	}
	payload := strings.Join([]string{r.Method, r.URL.RequestURI(), timestamp, r.Header.Get("X-User-ID")}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Gateway-Signature")))
}

func JWKSHandler(validator *authn.Validator) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if validator == nil {
			writeError(w, http.StatusServiceUnavailable, "JWKS is not configured")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_ = json.NewEncoder(w).Encode(validator.JWKS())
	}
}
