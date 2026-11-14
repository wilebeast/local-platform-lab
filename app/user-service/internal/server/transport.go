package server

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/middleware/recovery"
	"github.com/go-kratos/kratos/v3/transport"
	kratosgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"google.golang.org/grpc/status"
)

func NewGRPCServer(listener net.Listener, service *UserServer, logger *slog.Logger, timeout time.Duration) *kratosgrpc.Server {
	return newGRPCServer([]kratosgrpc.ServerOption{kratosgrpc.Listener(listener)}, service, logger, timeout)
}

func NewGRPCServerAt(address string, service *UserServer, logger *slog.Logger, timeout time.Duration) *kratosgrpc.Server {
	return newGRPCServer([]kratosgrpc.ServerOption{kratosgrpc.Address(address)}, service, logger, timeout)
}

func newGRPCServer(options []kratosgrpc.ServerOption, service *UserServer, logger *slog.Logger, timeout time.Duration) *kratosgrpc.Server {
	options = append(options,
		kratosgrpc.Timeout(timeout),
		kratosgrpc.Middleware(
			safeRequestLogging(logger),
			recovery.Recovery(recovery.WithLogger(logger)),
		),
	)
	server := kratosgrpc.NewServer(options...)
	userv1.RegisterUserServiceServer(server, service)
	return server
}

// safeRequestLogging deliberately records no request or response payload. Auth
// RPCs contain passwords, recovery codes, WebAuthn assertions and refresh
// tokens, so the stock Kratos logging middleware is not safe for this service.
func safeRequestLogging(logger *slog.Logger) middleware.Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, request any) (any, error) {
			started := time.Now()
			operation := ""
			if info, ok := transport.FromServerContext(ctx); ok {
				operation = info.Operation()
			}
			reply, err := next(ctx, request)
			logger.InfoContext(ctx, "server request",
				"operation", operation,
				"grpc_code", status.Code(err).String(),
				"latency", time.Since(started),
			)
			return reply, err
		}
	}
}
