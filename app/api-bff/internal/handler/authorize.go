package handler

import (
	"net/http"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
)

func Authorize(serviceContext *svc.ServiceContext, permissionKey string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
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
