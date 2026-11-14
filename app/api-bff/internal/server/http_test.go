package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/config"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
)

func TestAccessLogNeverSerializesRequestBody(t *testing.T) {
	var output bytes.Buffer
	filter := accessLogFilter(slog.New(slog.NewJSONHandler(&output, nil)))
	handler := filter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v3/auth/login", bytes.NewBufferString(`{"password":"do-not-log-this-password"}`))
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if bytes.Contains(output.Bytes(), []byte("do-not-log-this-password")) {
		t.Fatalf("password leaked into access log: %s", output.String())
	}
}

func TestKratosHTTPServerPreservesHealthContractAndRejectsUnknownRoutes(t *testing.T) {
	cfg := config.Config{Name: "api-bff", Host: "127.0.0.1", Port: 0, Timeout: 3 * time.Second}
	server := NewHTTPServer(cfg, &svc.ServiceContext{Config: cfg, Provider: "dns"}, slog.Default())

	health := httptest.NewRecorder()
	server.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK || health.Body.String() != "{\"status\":\"ok\",\"service\":\"api-bff\",\"discoveryProvider\":\"dns\"}\n" {
		t.Fatalf("unexpected health response: status=%d body=%s", health.Code, health.Body.String())
	}

	unknown := httptest.NewRecorder()
	server.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/not-registered", nil))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown route must return 404, got %d", unknown.Code)
	}

	wrongMethod := httptest.NewRecorder()
	server.ServeHTTP(wrongMethod, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method must return 405, got %d", wrongMethod.Code)
	}
}
