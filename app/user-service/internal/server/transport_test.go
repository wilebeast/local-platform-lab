package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	kratosgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestSafeRequestLoggingNeverSerializesCredentials(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := safeRequestLogging(logger)(func(context.Context, any) (any, error) {
		return nil, errors.New("authentication failed")
	})
	_, _ = handler(context.Background(), &userv1.LoginRequest{
		Email: "person@example.test", Password: "do-not-log-this-password",
	})
	logged := output.String()
	for _, secret := range []string{"person@example.test", "do-not-log-this-password"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("credential leaked into log: %s", logged)
		}
	}
	if !strings.Contains(logged, `"grpc_code":"Unknown"`) {
		t.Fatalf("request outcome metadata missing from log: %s", logged)
	}
}

func TestKratosGRPCServerPreservesUserContractAndHealth(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := NewGRPCServer(listener, NewUserServer(nil, nil), slog.Default(), 3*time.Second)
	if _, ok := any(server).(*kratosgrpc.Server); !ok {
		t.Fatal("user transport must be a Kratos gRPC server")
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Start(context.Background()) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Stop(ctx)
		select {
		case <-serveErr:
		case <-time.After(time.Second):
			t.Error("gRPC server did not stop")
		}
	})

	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	profile, err := userv1.NewUserServiceClient(connection).GetProfile(ctx, &userv1.GetProfileRequest{UserId: "kratos-user"})
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if profile.GetUserId() != "kratos-user" {
		t.Fatalf("unexpected profile: %#v", profile)
	}
	health, err := grpc_health_v1.NewHealthClient(connection).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil || health.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("unexpected health result: response=%v err=%v", health, err)
	}
}
