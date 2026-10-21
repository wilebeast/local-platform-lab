package handler

import (
	"net/http"

	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"github.com/zeromicro/go-zero/rest"
)

func RegisterHandlers(server *rest.Server, ctx *svc.ServiceContext) {
	gatewaySecret := ctx.GatewaySignatureKey
	validator := ctx.AuthValidator
	allowedOrigin := normalizeOrigin(ctx.AuthAllowedOrigin)
	server.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/healthz",
		Handler: HealthHandler(ctx),
	})
	server.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/.well-known/jwks.json",
		Handler: JWKSHandler(validator),
	})

	server.AddRoutes([]rest.Route{
		{
			Method: http.MethodPost, Path: "/auth/login",
			Handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, LoginHandler(ctx))),
		}, {
			Method: http.MethodPost, Path: "/auth/login/prepare",
			Handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, LoginHandler(ctx))),
		}, {
			Method: http.MethodPost, Path: "/auth/login/verify-2fa",
			Handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, VerifyLogin2FAHandler(ctx))),
		}, {
			Method: http.MethodPost, Path: "/auth/2fa/totp/options",
			Handler: GatewayAuth(gatewaySecret, validator, true, BrowserOrigin(allowedOrigin, CSRF(TOTPOptionsHandler(ctx)))),
		}, {
			Method: http.MethodPost, Path: "/auth/2fa/totp/confirm",
			Handler: GatewayAuth(gatewaySecret, validator, true, BrowserOrigin(allowedOrigin, CSRF(ConfirmTOTPHandler(ctx)))),
		}, {
			Method: http.MethodPost, Path: "/auth/2fa/unbind",
			Handler: GatewayAuth(gatewaySecret, validator, true, BrowserOrigin(allowedOrigin, CSRF(UnbindTOTPHandler(ctx)))),
		}, {
			Method: http.MethodPost, Path: "/auth/2fa/recovery-codes/regenerate",
			Handler: GatewayAuth(gatewaySecret, validator, true, BrowserOrigin(allowedOrigin, CSRF(RegenerateRecoveryCodesHandler(ctx)))),
		}, {
			Method: http.MethodPost, Path: "/auth/passkey/register/options",
			Handler: GatewayAuth(gatewaySecret, validator, true, BrowserOrigin(allowedOrigin, CSRF(BeginPasskeyRegistrationHandler(ctx)))),
		}, {
			Method: http.MethodPost, Path: "/auth/passkey/register/verify",
			Handler: GatewayAuth(gatewaySecret, validator, true, BrowserOrigin(allowedOrigin, CSRF(FinishPasskeyRegistrationHandler(ctx)))),
		}, {
			Method: http.MethodPost, Path: "/auth/passkey/login/options",
			Handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, BeginPasskeyLoginHandler(ctx))),
		}, {
			Method: http.MethodPost, Path: "/auth/passkey/login/verify",
			Handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, FinishPasskeyLoginHandler(ctx))),
		}, {
			Method: http.MethodPost, Path: "/auth/refresh",
			Handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, CSRF(RefreshHandler(ctx)))),
		}, {
			Method: http.MethodPost, Path: "/auth/logout",
			Handler: GatewayAuth(gatewaySecret, validator, false, BrowserOrigin(allowedOrigin, CSRF(LogoutHandler(ctx)))),
		}, {
			Method:  http.MethodGet,
			Path:    "/profile",
			Handler: GatewayAuth(gatewaySecret, validator, true, Authorize(ctx, "profile.read", ProfileHandler(ctx))),
		}, {
			Method: http.MethodGet, Path: "/account/summary",
			Handler: GatewayAuth(gatewaySecret, validator, true, AccountSummaryHandler(ctx)),
		}, {
			Method: http.MethodGet, Path: "/account/limits",
			Handler: GatewayAuth(gatewaySecret, validator, true, AccountLimitsHandler(ctx)),
		}}, rest.WithPrefix("/api/v3"))
}
