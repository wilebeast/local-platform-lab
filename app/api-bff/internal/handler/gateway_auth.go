package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type actorContextKey struct{}

type Actor struct {
	UserID string
}

func GatewayAuth(secret string, requireUser bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validGatewayRequest(r, secret, time.Now()) {
			writeError(w, http.StatusUnauthorized, "untrusted gateway request")
			return
		}
		userID := r.Header.Get("X-User-ID")
		if requireUser && (r.Header.Get("X-Authenticated") != "true" || userID == "") {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		ctx := context.WithValue(r.Context(), actorContextKey{}, Actor{UserID: userID})
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
