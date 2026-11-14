package handler

import (
	"net/http"

	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/types"
)

func ProfileHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		userID := actor.UserID
		if serviceContext == nil || serviceContext.GenericRPC == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "user service is unavailable"})
			return
		}

		profile, err := serviceContext.GenericRPC.CallRoute(r.Context(), "get-profile", map[string]any{"userId": userID})
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "user service request failed"})
			return
		}

		profileUserID, userIDOK := profile["userId"].(string)
		verificationStatus, statusOK := profile["verificationStatus"].(string)
		if !userIDOK || !statusOK {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "user service returned an invalid profile"})
			return
		}

		writeJSON(w, http.StatusOK, types.ProfileResponse{UserID: profileUserID, VerificationStatus: verificationStatus, Source: "user-service-generic-grpc"})
	}
}
