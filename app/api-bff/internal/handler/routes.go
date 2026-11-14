package handler

import (
	"net/http"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
)

type route struct {
	method  string
	path    string
	handler http.Handler
}

func RegisterHandlers(server *kratoshttp.Server, ctx *svc.ServiceContext) {
	gatewaySecret := ctx.GatewaySignatureKey
	validator := ctx.AuthValidator
	allowedOrigin := normalizeOrigin(ctx.AuthAllowedOrigin)
	routes := []route{
		{http.MethodGet, "/healthz", HealthHandler(ctx)},
		{http.MethodGet, "/.well-known/jwks.json", JWKSHandler(validator)},
		{
			method: http.MethodPost, path: "/v3/uc/register/send-code",
			handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, SendRegistrationCodeHandler(ctx))),
		},
		{
			method: http.MethodGet, path: "/api/v3/open/profile",
			handler: APIKeyGatewayAuth(gatewaySecret, Authorize(ctx, "profile.read", ProfileHandler(ctx))),
		}, {
			method: http.MethodGet, path: "/api/v3/open/v2/profile",
			handler: Ed25519APIKeyAuth(gatewaySecret, ctx.Ed25519Authenticator, Authorize(ctx, "profile.read", ProfileHandler(ctx))),
		},
		{
			method: http.MethodPost, path: "/api/v3/auth/login",
			handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, LoginHandler(ctx))),
		}, {
			method: http.MethodPost, path: "/api/v3/auth/login/prepare",
			handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, LoginHandler(ctx))),
		}, {
			method: http.MethodPost, path: "/api/v3/auth/login/verify-2fa",
			handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, VerifyLogin2FAHandler(ctx))),
		}, {
			method: http.MethodPost, path: "/api/v3/auth/2fa/totp/options",
			handler: GatewayAuth(gatewaySecret, validator, true, BrowserOrigin(allowedOrigin, CSRF(TOTPOptionsHandler(ctx)))),
		}, {
			method: http.MethodPost, path: "/api/v3/auth/2fa/totp/confirm",
			handler: GatewayAuth(gatewaySecret, validator, true, BrowserOrigin(allowedOrigin, CSRF(ConfirmTOTPHandler(ctx)))),
		}, {
			method: http.MethodPost, path: "/api/v3/auth/2fa/unbind",
			handler: GatewayAuth(gatewaySecret, validator, true, BrowserOrigin(allowedOrigin, CSRF(UnbindTOTPHandler(ctx)))),
		}, {
			method: http.MethodPost, path: "/api/v3/auth/2fa/recovery-codes/regenerate",
			handler: GatewayAuth(gatewaySecret, validator, true, BrowserOrigin(allowedOrigin, CSRF(RegenerateRecoveryCodesHandler(ctx)))),
		}, {
			method: http.MethodPost, path: "/api/v3/auth/passkey/register/options",
			handler: GatewayAuth(gatewaySecret, validator, true, BrowserOrigin(allowedOrigin, CSRF(BeginPasskeyRegistrationHandler(ctx)))),
		}, {
			method: http.MethodPost, path: "/api/v3/auth/passkey/register/verify",
			handler: GatewayAuth(gatewaySecret, validator, true, BrowserOrigin(allowedOrigin, CSRF(FinishPasskeyRegistrationHandler(ctx)))),
		}, {
			method: http.MethodPost, path: "/api/v3/auth/passkey/login/options",
			handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, BeginPasskeyLoginHandler(ctx))),
		}, {
			method: http.MethodPost, path: "/api/v3/auth/passkey/login/verify",
			handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, FinishPasskeyLoginHandler(ctx))),
		}, {
			method: http.MethodPost, path: "/api/v3/auth/account-context/select",
			handler: GatewayAuth(gatewaySecret, validator, true, BrowserOrigin(allowedOrigin, CSRF(SelectAccountContextHandler(ctx)))),
		}, {
			method: http.MethodPost, path: "/api/v3/auth/refresh",
			handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, CSRF(RefreshHandler(ctx)))),
		}, {
			method: http.MethodPost, path: "/api/v3/auth/logout",
			handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, CSRF(LogoutHandler(ctx)))),
		}, {
			method:  http.MethodGet,
			path:    "/api/v3/profile",
			handler: GatewayAuth(gatewaySecret, validator, true, Authorize(ctx, "profile.read", ProfileHandler(ctx))),
		}, {
			method: http.MethodGet, path: "/api/v3/account/summary",
			handler: GatewayAuth(gatewaySecret, validator, true, AccountSummaryHandler(ctx)),
		}, {
			method: http.MethodGet, path: "/api/v3/account/limits",
			handler: GatewayAuth(gatewaySecret, validator, true, AccountLimitsHandler(ctx)),
		}}
	for _, route := range routes {
		server.Handle(route.path, requireMethod(route.method, route.handler))
	}
}

func requireMethod(method string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}
