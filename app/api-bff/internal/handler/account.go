package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/accountctx"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/types"
)

func AccountSummaryHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return accountHandler(serviceContext, "get-account-summary", func(result map[string]any) (any, bool) {
		accountID, accountOK := result["accountId"].(string)
		status, statusOK := result["status"].(string)
		return types.AccountSummaryResponse{AccountID: accountID, Status: status, Source: "account-service-generic-grpc"}, accountOK && statusOK
	})
}

func AccountLimitsHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return accountHandler(serviceContext, "get-account-limits", func(result map[string]any) (any, bool) {
		limitText, limitOK := result["dailyLimitCents"].(string)
		limit, parseErr := strconv.ParseInt(limitText, 10, 64)
		currency, currencyOK := result["currency"].(string)
		return types.AccountLimitsResponse{DailyLimitCents: limit, Currency: currency, Source: "account-service-generic-grpc"}, limitOK && parseErr == nil && currencyOK
	})
}

func accountHandler(serviceContext *svc.ServiceContext, route string, decode func(map[string]any) (any, bool)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if serviceContext == nil || serviceContext.GenericRPC == nil || serviceContext.AccountContexts == nil || serviceContext.AccountContextSigner == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "account service is unavailable"})
			return
		}
		resolved, err := serviceContext.AccountContexts.Resolve(r.Context(), accountctx.Actor{UserID: actor.UserID, SessionID: actor.SessionID})
		if err != nil {
			writeAccountContextError(w, err)
			return
		}
		rpcContext, err := serviceContext.AccountContextSigner.Attach(r.Context(), resolved)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "account context signing failed"})
			return
		}
		result, err := serviceContext.GenericRPC.CallRoute(rpcContext, route, map[string]any{"userId": actor.UserID})
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "account service request failed"})
			return
		}
		response, valid := decode(result)
		if !valid {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "account service returned an invalid response"})
			return
		}
		writeJSON(w, http.StatusOK, response)
	}
}

type selectAccountContextRequest struct {
	AccountUID string `json:"accountUid"`
}

func SelectAccountContextHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorFromContext(r.Context())
		if !ok || actor.SessionID == "" {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		var request selectAccountContextRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.AccountUID == "" {
			writeError(w, http.StatusBadRequest, "accountUid is required")
			return
		}
		if serviceContext == nil || serviceContext.AccountContexts == nil {
			writeError(w, http.StatusServiceUnavailable, "account context is unavailable")
			return
		}
		value, err := serviceContext.AccountContexts.Select(r.Context(), accountctx.Actor{UserID: actor.UserID, SessionID: actor.SessionID}, request.AccountUID)
		if err != nil {
			writeAccountContextError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"selected": true, "accountUid": value.AccountUID, "accountType": value.AccountType,
			"organizationId": value.OrganizationID, "permissionVersion": value.PermissionVersion,
		})
	}
}

func writeAccountContextError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, accountctx.ErrSelectionNotFound):
		writeError(w, http.StatusConflict, "account selection required")
	case errors.Is(err, accountctx.ErrSelectionStale), errors.Is(err, accountctx.ErrProjectionNotFound):
		writeError(w, http.StatusConflict, "account context must be refreshed")
	case errors.Is(err, accountctx.ErrAccountNotAccessible):
		writeError(w, http.StatusForbidden, "account is not accessible")
	default:
		writeError(w, http.StatusBadGateway, "account context request failed")
	}
}
