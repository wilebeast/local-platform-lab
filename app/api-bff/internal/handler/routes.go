package handler

import (
	"net/http"

	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"github.com/zeromicro/go-zero/rest"
)

func RegisterHandlers(server *rest.Server, ctx *svc.ServiceContext) {
	gatewaySecret := ctx.GatewaySignatureKey
	allowedOrigin := normalizeOrigin(ctx.AuthAllowedOrigin)
	server.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/healthz",
		Handler: HealthHandler(ctx),
	})

	server.AddRoutes([]rest.Route{
		{
			Method: http.MethodPost, Path: "/auth/login",
			Handler: GatewayAuth(gatewaySecret, false, BrowserOrigin(allowedOrigin, LoginHandler(ctx))),
		}, {
			Method: http.MethodPost, Path: "/auth/login/prepare",
			Handler: GatewayAuth(gatewaySecret, false, BrowserOrigin(allowedOrigin, LoginHandler(ctx))),
		}, {
			Method: http.MethodPost, Path: "/auth/login/verify-2fa",
			Handler: GatewayAuth(gatewaySecret, false, BrowserOrigin(allowedOrigin, VerifyLogin2FAHandler(ctx))),
		}, {
			Method: http.MethodPost, Path: "/auth/2fa/totp/options",
			Handler: GatewayAuth(gatewaySecret, true, BrowserOrigin(allowedOrigin, CSRF(TOTPOptionsHandler(ctx)))),
		}, {
			Method: http.MethodPost, Path: "/auth/2fa/totp/confirm",
			Handler: GatewayAuth(gatewaySecret, true, BrowserOrigin(allowedOrigin, CSRF(ConfirmTOTPHandler(ctx)))),
		}, {
			Method: http.MethodPost, Path: "/auth/2fa/unbind",
			Handler: GatewayAuth(gatewaySecret, true, BrowserOrigin(allowedOrigin, CSRF(UnbindTOTPHandler(ctx)))),
		}, {
			Method: http.MethodPost, Path: "/auth/2fa/recovery-codes/regenerate",
			Handler: GatewayAuth(gatewaySecret, true, BrowserOrigin(allowedOrigin, CSRF(RegenerateRecoveryCodesHandler(ctx)))),
		}, {
			Method: http.MethodPost, Path: "/auth/refresh",
			Handler: GatewayAuth(gatewaySecret, false, BrowserOrigin(allowedOrigin, CSRF(RefreshHandler(ctx)))),
		}, {
			Method: http.MethodPost, Path: "/auth/logout",
			Handler: GatewayAuth(gatewaySecret, false, BrowserOrigin(allowedOrigin, CSRF(LogoutHandler(ctx)))),
		}, {
			Method:  http.MethodGet,
			Path:    "/profile",
			Handler: GatewayAuth(gatewaySecret, true, ProfileHandler(ctx)),
		}, {
			Method: http.MethodGet, Path: "/account/summary",
			Handler: GatewayAuth(gatewaySecret, true, AccountSummaryHandler(ctx)),
		}, {
			Method: http.MethodGet, Path: "/account/limits",
			Handler: GatewayAuth(gatewaySecret, true, AccountLimitsHandler(ctx)),
		}}, rest.WithPrefix("/api/v3"))
}
