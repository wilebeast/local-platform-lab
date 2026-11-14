package handler

import (
	"net/http"

	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/types"
)

func HealthHandler(ctx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, types.HealthResponse{
			Status:            "ok",
			Service:           ctx.Config.Name,
			DiscoveryProvider: ctx.Provider,
		})
	}
}
