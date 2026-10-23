package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	notificationv1 "github.com/wilebeast/local-platform-lab/api/notification/v1"
	notification "github.com/wilebeast/local-platform-lab/app/notification-service/internal/notification"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

//go:embed schema.sql
var schema string

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	postgresDSN := requiredEnv("POSTGRES_DSN")
	redisAddress := envOr("REDIS_ADDRESS", "redis.redis.svc.cluster.local:6379")
	smtpAddress := envOr("SMTP_ADDRESS", "mailpit.notification-lab.svc.cluster.local:1025")
	fromAddress := envOr("MAIL_FROM", "notifications@local.test")
	pepper := []byte(requiredEnv("EMAIL_CODE_PEPPER"))
	if len(pepper) < 32 {
		log.Fatal("EMAIL_CODE_PEPPER must contain at least 32 bytes")
	}

	pool, err := pgxpool.New(ctx, postgresDSN)
	if err != nil {
		log.Fatalf("connect postgres: %v", err)
	}
	defer pool.Close()
	if err := waitForPostgres(ctx, pool); err != nil {
		log.Fatalf("initialize postgres: %v", err)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		log.Fatalf("apply notification schema: %v", err)
	}

	redisClient := redis.NewClient(&redis.Options{Addr: redisAddress})
	defer redisClient.Close()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("connect redis: %v", err)
	}

	templates := notification.NewPostgresTemplateRepository(pool)
	mailer := notification.NewSMTPMailer(smtpAddress, fromAddress)
	codeManager := notification.NewCodeManager(
		notification.NewRedisCodeStore(redisClient),
		notification.NewTemplateCodeSender(templates, mailer),
		pepper,
		nil,
	)
	service := notification.NewService(templates, mailer, codeManager)

	grpcAddress := envOr("GRPC_ADDRESS", ":9003")
	listener, err := net.Listen("tcp", grpcAddress)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	metrics := newRPCMetrics()
	server := grpc.NewServer(grpc.UnaryInterceptor(metrics.interceptor))
	notificationv1.RegisterNotificationServiceServer(server, service)

	healthServer := &http.Server{Addr: ":8080", Handler: serviceHTTPHandler(pool, redisClient, metrics), ReadHeaderTimeout: 3 * time.Second}
	go func() {
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("health server: %v", err)
			stop()
		}
	}()
	go func() {
		<-ctx.Done()
		server.GracefulStop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = healthServer.Shutdown(shutdownCtx)
	}()

	log.Printf("notification-service listening on %s", grpcAddress)
	if err := server.Serve(listener); err != nil {
		log.Fatalf("serve grpc: %v", err)
	}
}

func waitForPostgres(ctx context.Context, pool *pgxpool.Pool) error {
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		if err := pool.Ping(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return lastErr
}

func serviceHTTPHandler(pool *pgxpool.Pool, redisClient *redis.Client, metrics *rpcMetrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics)
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			http.Error(writer, "postgres unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := redisClient.Ping(ctx).Err(); err != nil {
			http.Error(writer, "redis unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok"))
	})
	return mux
}

type rpcMetrics struct {
	mu     sync.Mutex
	counts map[string]uint64
}

func newRPCMetrics() *rpcMetrics {
	return &rpcMetrics{counts: make(map[string]uint64)}
}

func (m *rpcMetrics) interceptor(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	response, err := handler(ctx, request)
	key := info.FullMethod + "\x00" + status.Code(err).String()
	m.mu.Lock()
	m.counts[key]++
	m.mu.Unlock()
	return response, err
}

func (m *rpcMetrics) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
	m.mu.Lock()
	defer m.mu.Unlock()
	_, _ = fmt.Fprintln(writer, "# HELP notification_grpc_requests_total Total notification gRPC requests.")
	_, _ = fmt.Fprintln(writer, "# TYPE notification_grpc_requests_total counter")
	for key, count := range m.counts {
		parts := strings.SplitN(key, "\x00", 2)
		_, _ = fmt.Fprintf(writer, "notification_grpc_requests_total{method=%q,status=%q} %d\n", parts[0], parts[1], count)
	}
}

func requiredEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
