package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRegisterHandlersExposesDocumentedRegistrationRoute(t *testing.T) {
	secret := "gateway-signature-secret-at-least-32-bytes"
	client := fakeUserClient{registrationCode: &userv1.SendRegistrationCodeResponse{VerifyToken: "registration-token"}}
	serviceContext := &svc.ServiceContext{UserClient: client, GatewaySignatureKey: secret, AuthAllowedOrigin: "https://localhost:18443"}
	server := kratoshttp.NewServer()
	RegisterHandlers(server, serviceContext)
	request := httptest.NewRequest(http.MethodPost, "/v3/uc/register/send-code", strings.NewReader(`{"email":"reader@example.com","code_type":3}`))
	request.Header.Set("Origin", "https://localhost:18443")
	signGatewayRequest(request, secret, time.Now().Unix())
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"verify_token":"registration-token"`) {
		t.Fatalf("documented route is not active: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSendRegistrationCodeHandlerUsesV3Contract(t *testing.T) {
	var forwarded *userv1.SendRegistrationCodeRequest
	client := fakeUserClient{
		registrationCode: &userv1.SendRegistrationCodeResponse{VerifyToken: "registration-token"},
		registrationCall: func(request *userv1.SendRegistrationCodeRequest) { forwarded = request },
	}
	request := httptest.NewRequest(http.MethodPost, "/v3/uc/register/send-code", strings.NewReader(`{
		"email":"Reader@Example.COM",
		"code_type":3
	}`))
	recorder := httptest.NewRecorder()

	SendRegistrationCodeHandler(&svc.ServiceContext{UserClient: client})(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Code    int64   `json:"code"`
		Message *string `json:"message"`
		Data    struct {
			VerifyToken string `json:"verify_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || response.Message != nil || response.Data.VerifyToken != "registration-token" {
		t.Fatalf("unexpected response: %+v", response)
	}
	if forwarded.GetEmail() != "Reader@Example.COM" || forwarded.GetCodeType() != 3 {
		t.Fatalf("unexpected user-service request: %+v", forwarded)
	}
}

func TestSendRegistrationCodeHandlerMapsRegisteredEmailConflict(t *testing.T) {
	client := fakeUserClient{registrationError: status.Error(codes.AlreadyExists, "email already registered")}
	request := httptest.NewRequest(http.MethodPost, "/v3/uc/register/send-code", strings.NewReader(`{
		"email":"registered@example.com",
		"code_type":3
	}`))
	recorder := httptest.NewRecorder()

	SendRegistrationCodeHandler(&svc.ServiceContext{UserClient: client})(recorder, request)

	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":10006`) || !strings.Contains(recorder.Body.String(), `"data":null`) {
		t.Fatalf("unexpected conflict response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
