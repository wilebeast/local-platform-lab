package handler

import (
	"context"
	"net/http"
	"time"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

func ProfileHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-User-ID")
		if userID == "" {
			userID = "local-user"
		}

		if serviceContext == nil || serviceContext.UserClient == nil {
			httpx.WriteJsonCtx(r.Context(), w, http.StatusServiceUnavailable, map[string]string{"error": "user service is unavailable"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		profile, err := serviceContext.UserClient.GetProfile(ctx, &userv1.GetProfileRequest{UserId: userID})
		if err != nil {
			httpx.WriteJsonCtx(r.Context(), w, http.StatusBadGateway, map[string]string{"error": "user service request failed"})
			return
		}

		httpx.OkJson(w, types.ProfileResponse{
			UserID:             profile.GetUserId(),
			VerificationStatus: profile.GetVerificationStatus(),
			Source:             "user-service-grpc",
		})
	}
}
