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
			Method: http.MethodPost, Path: "/auth/refresh",
			Handler: GatewayAuth(gatewaySecret, false, BrowserOrigin(allowedOrigin, CSRF(RefreshHandler(ctx)))),
		}, {
			Method: http.MethodPost, Path: "/auth/logout",
			Handler: GatewayAuth(gatewaySecret, false, BrowserOrigin(allowedOrigin, CSRF(LogoutHandler(ctx)))),
		}, {
			Method:  http.MethodGet,
			Path:    "/profile",
			Handler: GatewayAuth(gatewaySecret, true, ProfileHandler(ctx)),
		}}, rest.WithPrefix("/api/v3"))
}
