package handler

import (
	"net/http"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/apikey"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
)

func Authorize(serviceContext *svc.ServiceContext, permissionKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if actor.Type == ActorTypeAPIKey {
			if serviceContext == nil || serviceContext.APIKeyValidator == nil {
				writeError(w, http.StatusServiceUnavailable, "API key authorization service is unavailable")
				return
			}
			result, err := serviceContext.APIKeyValidator.Validate(r.Context(), apikey.ValidationRequest{
				MerchantID: actor.MerchantID, KeyID: actor.KeyID,
				RequiredPermission: permissionKey, ClientIP: requestClientIP(r),
			})
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "API key permission check failed")
				return
			}
			if result.Forbidden {
				writeError(w, http.StatusForbidden, "permission denied")
				return
			}
			if !result.Valid {
				writeError(w, http.StatusUnauthorized, "invalid API key")
				return
			}
			next(w, r)
			return
		}
		if serviceContext == nil || serviceContext.UserClient == nil {
			writeError(w, http.StatusServiceUnavailable, "authorization service is unavailable")
			return
		}
		permission, err := serviceContext.UserClient.CheckPermission(r.Context(), &userv1.CheckPermissionRequest{
			UserId:         actor.UserID,
			OrganizationId: serviceContext.RBACOrganizationID,
			PermissionKey:  permissionKey,
		})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "permission check failed")
			return
		}
		if !permission.GetAllowed() {
			writeError(w, http.StatusForbidden, "permission denied")
			return
		}
		next(w, r)
	}
}

func requestClientIP(r *http.Request) string {
	if value := r.Header.Get("X-Real-IP"); value != "" {
		return value
	}
	return r.RemoteAddr
}
