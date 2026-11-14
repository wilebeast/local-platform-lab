package server

import (
	"log/slog"
	"net/http"

	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/config"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/handler"
	"github.com/wilebeast/local-platform-lab/app/api-bff/internal/svc"
)

func NewHTTPServer(cfg config.Config, serviceContext *svc.ServiceContext, logger *slog.Logger) *kratoshttp.Server {
	notFound := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
	})
	methodNotAllowed := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	})
	server := kratoshttp.NewServer(
		kratoshttp.Address(cfg.Address()),
		kratoshttp.Timeout(cfg.Timeout),
		kratoshttp.StrictSlash(false),
		kratoshttp.NotFoundHandler(notFound),
		kratoshttp.MethodNotAllowedHandler(methodNotAllowed),
		kratoshttp.Filter(accessLogFilter(logger)),
	)
	handler.RegisterHandlers(server, serviceContext)
	return server
}

func accessLogFilter(logger *slog.Logger) kratoshttp.FilterFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Deliberately log only routing metadata. BFF requests can contain
			// passwords, OTP codes, refresh tokens and WebAuthn assertions.
			logger.InfoContext(r.Context(), "http request", "method", r.Method, "path", r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
}
