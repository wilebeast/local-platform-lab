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
		result, err := serviceContext.UserClient.PrepareLogin(r.Context(), &userv1.PrepareLoginRequest{Email: request.Email, Password: request.Password})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		if result.GetSecondFactorRequired() {
			httpx.OkJson(w, map[string]any{"secondFactorRequired": true, "authSessionToken": result.GetAuthSessionToken()})
			return
		}
		writeTokenResult(w, r, result.GetTokenPair(), nil)
	}
}

type verifyLogin2FARequest struct {
	AuthSessionToken string `json:"authSessionToken"`
	Code             string `json:"code"`
}
type confirmTOTPRequest struct {
	SetupToken string `json:"setupToken"`
	Code       string `json:"code"`
}
type unbindTOTPRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}
type codeRequest struct {
	Code string `json:"code"`
}
type passkeyCeremonyRequest struct {
	CeremonyID string          `json:"ceremonyId"`
	Credential json.RawMessage `json:"credential"`
}

func VerifyLogin2FAHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request verifyLogin2FARequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.AuthSessionToken == "" || request.Code == "" {
			writeError(w, http.StatusBadRequest, "authSessionToken and code are required")
			return
		}
		pair, err := serviceContext.UserClient.VerifyLogin2FA(r.Context(), &userv1.VerifyLogin2FARequest{AuthSessionToken: request.AuthSessionToken, Code: request.Code})
		writeTokenResult(w, r, pair, err)
	}
}

func TOTPOptionsHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		result, err := serviceContext.UserClient.TOTPOptions(r.Context(), &userv1.TOTPOptionsRequest{UserId: actor.UserID})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		httpx.OkJson(w, map[string]any{"setupToken": result.GetSetupToken(), "otpauthUri": result.GetOtpauthUri()})
	}
}

func ConfirmTOTPHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		var request confirmTOTPRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.SetupToken == "" || request.Code == "" {
			writeError(w, http.StatusBadRequest, "setupToken and code are required")
			return
		}
		result, err := serviceContext.UserClient.ConfirmTOTP(r.Context(), &userv1.ConfirmTOTPRequest{UserId: actor.UserID, SetupToken: request.SetupToken, Code: request.Code})
		writeRecoveryCodes(w, result, err)
	}
}

func UnbindTOTPHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		var request unbindTOTPRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Password == "" || request.Code == "" {
			writeError(w, http.StatusBadRequest, "password and code are required")
			return
		}
		result, err := serviceContext.UserClient.UnbindTOTP(r.Context(), &userv1.UnbindTOTPRequest{UserId: actor.UserID, Password: request.Password, Code: request.Code})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		httpx.OkJson(w, map[string]any{"unbound": result.GetUnbound()})
	}
}

func RegenerateRecoveryCodesHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		var request codeRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Code == "" {
			writeError(w, http.StatusBadRequest, "code is required")
			return
		}
		result, err := serviceContext.UserClient.RegenerateRecoveryCodes(r.Context(), &userv1.RegenerateRecoveryCodesRequest{UserId: actor.UserID, Code: request.Code})
		writeRecoveryCodes(w, result, err)
	}
}

func BeginPasskeyRegistrationHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		result, err := serviceContext.UserClient.BeginPasskeyRegistration(r.Context(), &userv1.BeginPasskeyRegistrationRequest{
			UserId: actor.UserID, UserName: actor.UserID,
		})
		writePasskeyOptions(w, result, err)
	}
}

func FinishPasskeyRegistrationHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := actorFromContext(r.Context()); !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		var request passkeyCeremonyRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.CeremonyID == "" || len(request.Credential) == 0 {
			writeError(w, http.StatusBadRequest, "ceremonyId and credential are required")
			return
		}
		result, err := serviceContext.UserClient.FinishPasskeyRegistration(r.Context(), &userv1.FinishPasskeyRegistrationRequest{
			CeremonyId: request.CeremonyID, ResponseJson: request.Credential,
		})
		if err != nil {
			writeRPCError(w, err)
			return
		}
		httpx.OkJson(w, map[string]any{"registered": result.GetRegistered(), "credentialId": result.GetCredentialId()})
	}
}

func BeginPasskeyLoginHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := serviceContext.UserClient.BeginPasskeyLogin(r.Context(), &userv1.BeginPasskeyLoginRequest{})
		writePasskeyOptions(w, result, err)
	}
}

func FinishPasskeyLoginHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request passkeyCeremonyRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.CeremonyID == "" || len(request.Credential) == 0 {
			writeError(w, http.StatusBadRequest, "ceremonyId and credential are required")
			return
		}
		pair, err := serviceContext.UserClient.FinishPasskeyLogin(r.Context(), &userv1.FinishPasskeyLoginRequest{
			CeremonyId: request.CeremonyID, ResponseJson: request.Credential,
		})
		writeTokenResult(w, r, pair, err)
	}
}

func writePasskeyOptions(w http.ResponseWriter, result *userv1.PasskeyOptionsResponse, err error) {
	if err != nil {
		writeRPCError(w, err)
		return
	}
	if result == nil || result.GetCeremonyId() == "" || len(result.GetOptionsJson()) == 0 {
		writeError(w, http.StatusBadGateway, "user service returned incomplete passkey options")
		return
	}
	var options any
	if err := json.Unmarshal(result.GetOptionsJson(), &options); err != nil {
		writeError(w, http.StatusBadGateway, "user service returned invalid passkey options")
		return
	}
	httpx.OkJson(w, map[string]any{"ceremonyId": result.GetCeremonyId(), "options": options})
}

func writeRecoveryCodes(w http.ResponseWriter, result *userv1.RecoveryCodesResponse, err error) {
	if err != nil {
		writeRPCError(w, err)
		return
	}
	httpx.OkJson(w, map[string]any{"recoveryCodes": result.GetRecoveryCodes()})
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
	if pair == nil || pair.GetAccessToken() == "" || pair.GetRefreshToken() == "" {
		writeError(w, http.StatusBadGateway, "user service returned incomplete tokens")
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
	if status.Code(err) == codes.FailedPrecondition {
		writeError(w, http.StatusConflict, status.Convert(err).Message())
		return
	}
	writeError(w, http.StatusBadGateway, "user service request failed")
}

func writeError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
