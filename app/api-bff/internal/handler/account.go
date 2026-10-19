package handler

import (
	"net/http"
	"strconv"

	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
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
		if serviceContext == nil || serviceContext.GenericRPC == nil {
			httpx.WriteJsonCtx(r.Context(), w, http.StatusServiceUnavailable, map[string]string{"error": "account service is unavailable"})
			return
		}
		result, err := serviceContext.GenericRPC.CallRoute(r.Context(), route, map[string]any{"userId": actor.UserID})
		if err != nil {
			httpx.WriteJsonCtx(r.Context(), w, http.StatusBadGateway, map[string]string{"error": "account service request failed"})
			return
		}
		response, valid := decode(result)
		if !valid {
			httpx.WriteJsonCtx(r.Context(), w, http.StatusBadGateway, map[string]string{"error": "account service returned an invalid response"})
			return
		}
		httpx.OkJson(w, response)
	}
}
