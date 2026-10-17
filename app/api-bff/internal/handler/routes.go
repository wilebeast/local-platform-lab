package handler

import (
	"net/http"

	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"github.com/zeromicro/go-zero/rest"
)

func RegisterHandlers(server *rest.Server, ctx *svc.ServiceContext) {
	server.AddRoute(rest.Route{
		Method:  http.MethodGet,
		Path:    "/healthz",
		Handler: HealthHandler(ctx),
	})

	server.AddRoutes([]rest.Route{{
		Method:  http.MethodGet,
		Path:    "/profile",
		Handler: ProfileHandler(ctx),
	}}, rest.WithPrefix("/api/v3"))
}
