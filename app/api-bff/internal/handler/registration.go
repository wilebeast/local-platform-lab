package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type sendRegistrationCodeRequest struct {
	Email    string `json:"email"`
	CodeType int64  `json:"code_type"`
}

type v3Response struct {
	Code    int64 `json:"code"`
	Message any   `json:"message"`
	Data    any   `json:"data"`
}

func SendRegistrationCodeHandler(serviceContext *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if serviceContext == nil || serviceContext.UserClient == nil {
			writeV3Error(w, http.StatusServiceUnavailable, 500, "User 服务暂不可用")
			return
		}
		var request sendRegistrationCodeRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || strings.TrimSpace(request.Email) == "" || request.CodeType != 3 {
			writeV3Error(w, http.StatusBadRequest, 30013, "参数错误")
			return
		}

		result, err := serviceContext.UserClient.SendRegistrationCode(r.Context(), &userv1.SendRegistrationCodeRequest{
			Email: request.Email, CodeType: request.CodeType, Locale: registrationLocale(r),
		})
		if err != nil {
			writeRegistrationRPCError(w, err)
			return
		}
		if result == nil || result.GetVerifyToken() == "" {
			writeV3Error(w, http.StatusBadGateway, 500, "User 服务返回无效结果")
			return
		}
		writeV3Success(w, map[string]string{"verify_token": result.GetVerifyToken()})
	}
}

func registrationLocale(r *http.Request) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Accept-Language"))), "zh") {
		return "zh-CN"
	}
	return "en"
}

func writeRegistrationRPCError(w http.ResponseWriter, err error) {
	switch status.Code(err) {
	case codes.InvalidArgument:
		writeV3Error(w, http.StatusBadRequest, 30013, "参数错误")
	case codes.AlreadyExists:
		writeV3Error(w, http.StatusConflict, 10006, "该邮箱已注册")
	case codes.ResourceExhausted:
		writeV3Error(w, http.StatusTooManyRequests, 42901, "请求过于频繁")
	case codes.Unavailable:
		writeV3Error(w, http.StatusServiceUnavailable, 500, "验证码服务暂不可用")
	default:
		writeV3Error(w, http.StatusInternalServerError, 500, "内部错误")
	}
}

func writeV3Success(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, v3Response{Code: 200, Message: nil, Data: data})
}

func writeV3Error(w http.ResponseWriter, httpStatus int, code int64, message string) {
	writeJSON(w, httpStatus, v3Response{Code: code, Message: message, Data: nil})
}
