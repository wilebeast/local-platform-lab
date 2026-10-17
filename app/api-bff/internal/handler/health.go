package handler

import (
	"net/http"

	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func HealthHandler(ctx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		httpx.OkJson(w, types.HealthResponse{
			Status:            "ok",
			Service:           ctx.Config.Name,
			DiscoveryProvider: ctx.Provider,
		})
	}
}
