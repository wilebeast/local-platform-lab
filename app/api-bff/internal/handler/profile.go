package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func ProfileHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		userID := actor.UserID

		if serviceContext == nil || serviceContext.GenericRPC == nil || serviceContext.ProfileMethod == "" {
			httpx.WriteJsonCtx(r.Context(), w, http.StatusServiceUnavailable, map[string]string{"error": "user service is unavailable"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		profile, err := serviceContext.GenericRPC.Call(ctx, serviceContext.ProfileMethod, map[string]any{"userId": userID})
		if err != nil {
			httpx.WriteJsonCtx(r.Context(), w, http.StatusBadGateway, map[string]string{"error": "user service request failed"})
			return
		}

		profileUserID, userIDOK := profile["userId"].(string)
		verificationStatus, statusOK := profile["verificationStatus"].(string)
		if !userIDOK || !statusOK {
			httpx.WriteJsonCtx(r.Context(), w, http.StatusBadGateway, map[string]string{"error": "user service returned an invalid profile"})
			return
		}

		httpx.OkJson(w, types.ProfileResponse{UserID: profileUserID, VerificationStatus: verificationStatus, Source: "user-service-generic-grpc"})
	}
}
