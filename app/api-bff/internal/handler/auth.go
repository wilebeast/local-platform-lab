package handler

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"github.com/zeromicro/go-zero/rest/httpx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type credentialsRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func LoginHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request credentialsRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Email == "" || request.Password == "" {
			writeError(w, http.StatusBadRequest, "email and password are required")
			return
		}
		pair, err := serviceContext.UserClient.Login(r.Context(), &userv1.LoginRequest{Email: request.Email, Password: request.Password})
		writeTokenResult(w, r, pair, err)
	}
}

func RefreshHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := refreshTokenFromRequest(r)
		pair, err := serviceContext.UserClient.Refresh(r.Context(), &userv1.RefreshRequest{RefreshToken: token})
		writeTokenResult(w, r, pair, err)
	}
}

func LogoutHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := refreshTokenFromRequest(r)
		response, err := serviceContext.UserClient.Logout(r.Context(), &userv1.LogoutRequest{RefreshToken: token})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		clearAuthCookies(w)
		httpx.OkJson(w, map[string]bool{"revoked": response.GetRevoked()})
	}
}

func refreshTokenFromRequest(r *http.Request) string {
	if cookie, err := r.Cookie("refresh_token"); err == nil {
		return cookie.Value
	}
	return ""
}

func writeTokenResult(w http.ResponseWriter, r *http.Request, pair *userv1.TokenPair, err error) {
	if err != nil {
		writeRPCError(w, err)
		return
	}
	setAuthCookies(w, pair)
	httpx.OkJson(w, map[string]any{
		"authenticated": true, "expiresIn": pair.GetExpiresIn(), "tokenType": pair.GetTokenType(),
	})
}

func setAuthCookies(w http.ResponseWriter, pair *userv1.TokenPair) {
	http.SetCookie(w, &http.Cookie{Name: "access_token", Value: pair.GetAccessToken(), Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: int(pair.GetExpiresIn())})
	http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: pair.GetRefreshToken(), Path: "/api/v3/auth", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: int((7 * 24 * time.Hour).Seconds())})
	csrfToken, err := randomCSRFToken()
	if err == nil {
		http.SetCookie(w, &http.Cookie{Name: "csrf_token", Value: csrfToken, Path: "/api/v3/auth", Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: int((7 * 24 * time.Hour).Seconds())})
	}
}

func clearAuthCookies(w http.ResponseWriter) {
	for _, cookie := range []*http.Cookie{
		{Name: "access_token", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1},
		{Name: "refresh_token", Path: "/api/v3/auth", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1},
		{Name: "csrf_token", Path: "/api/v3/auth", Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1},
	} {
		http.SetCookie(w, cookie)
	}
}

func BrowserOrigin(allowedOrigin string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if allowedOrigin == "" || r.Header.Get("Origin") != allowedOrigin {
			writeError(w, http.StatusForbidden, "origin is not allowed")
			return
		}
		next.ServeHTTP(w, r)
	}
}

func CSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("csrf_token")
		header := r.Header.Get("X-CSRF-Token")
		if err != nil || header == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
			writeError(w, http.StatusForbidden, "invalid csrf token")
			return
		}
		next.ServeHTTP(w, r)
	}
}

func randomCSRFToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func normalizeOrigin(value string) string { return strings.TrimRight(value, "/") }

func writeRPCError(w http.ResponseWriter, err error) {
	if status.Code(err) == codes.Unauthenticated {
		writeError(w, http.StatusUnauthorized, status.Convert(err).Message())
		return
	}
	writeError(w, http.StatusBadGateway, "user service request failed")
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
