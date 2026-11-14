package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"flag"
	"log"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	kratos "github.com/go-kratos/kratos/v3"
	"github.com/redis/go-redis/v9"
	notificationv1 "github.com/wilebeast/local-platform-lab/api/notification/v1"
	"github.com/wilebeast/local-platform-lab/app/user-service/internal/auth"
	userconfig "github.com/wilebeast/local-platform-lab/app/user-service/internal/config"
	"github.com/wilebeast/local-platform-lab/app/user-service/internal/identity"
	"github.com/wilebeast/local-platform-lab/app/user-service/internal/rbac"
	"github.com/wilebeast/local-platform-lab/app/user-service/internal/registration"
	userserver "github.com/wilebeast/local-platform-lab/app/user-service/internal/server"
	"github.com/wilebeast/local-platform-lab/pkg/serviceregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var configFile = flag.String("f", "etc/user-service.yaml", "configuration file")

func main() {
	flag.Parse()

	c, err := userconfig.Load(*configFile)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	redisAddress := envOr("REDIS_ADDR", "redis.redis.svc.cluster.local:6379")
	redisClient := redis.NewClient(&redis.Options{Addr: redisAddress})
	refreshStore := auth.NewRedisStoreWithClient(redisClient)
	defer refreshStore.Close()
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPing()
	if err := refreshStore.Ping(pingCtx); err != nil {
		log.Fatalf("connect to Redis at %s: %v", redisAddress, err)
	}
	postgresDSN := envOr("POSTGRES_DSN", "postgres://local_platform:local_platform@postgres.postgres.svc.cluster.local:5432/local_platform?sslmode=disable")
	postgresCtx, cancelPostgres := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelPostgres()
	db, err := rbac.OpenPostgres(postgresCtx, postgresDSN)
	if err != nil {
		log.Fatalf("connect to PostgreSQL: %v", err)
	}
	defer db.Close()
	if err := rbac.Migrate(postgresCtx, db); err != nil {
		log.Fatalf("migrate RBAC schema: %v", err)
	}
	if err := identity.Migrate(postgresCtx, db); err != nil {
		log.Fatalf("migrate identity schema: %v", err)
	}
	if bootstrapIdentityEnabled(os.Getenv("BOOTSTRAP_DEMO_IDENTITY")) {
		if err := identity.EnsureBootstrapUser(postgresCtx, db, identity.BootstrapUser{
			UserID:   envOr("DEMO_USER_ID", "local-user"),
			Email:    envOr("DEMO_USER_EMAIL", "demo@local.test"),
			Password: envOr("DEMO_USER_PASSWORD", "local-demo-password"),
		}); err != nil {
			log.Fatalf("bootstrap local identity: %v", err)
		}
	}
	credentialStore := identity.NewPostgresRepository(db)
	notificationTarget := envOr("NOTIFICATION_GRPC_TARGET", "dns:///notification-service.notification-lab.svc.cluster.local:9003")
	notificationConnection, err := grpc.NewClient(notificationTarget, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("configure notification client: %v", err)
	}
	defer notificationConnection.Close()
	registrationService := registration.NewCodeService(registration.CodeServiceConfig{
		ChallengeTTL:  10 * time.Minute,
		DefaultLocale: "en",
	}, credentialStore, registration.NewRedisChallengeStore(redisClient), registration.NewNotificationCodeIssuer(notificationv1.NewNotificationServiceClient(notificationConnection)))
	authService, err := auth.NewService(auth.Config{
		JWTPrivateKey: mustEd25519PrivateKey("JWT_PRIVATE_KEY_B64"),
		JWTKeyID:      envOr("JWT_KEY_ID", "local-auth-1"),
		Issuer:        "local-platform-lab", Audience: "api-bff",
		AccessTTL: 30 * time.Minute, RefreshTTL: 7 * 24 * time.Hour,
		TOTPEncryptionKey:  mustEnv("TOTP_ENCRYPTION_KEY"),
		RecoveryCodePepper: mustEnv("RECOVERY_CODE_PEPPER"),
		PasskeyRPID:        envOr("PASSKEY_RP_ID", "localhost"),
		PasskeyRPName:      envOr("PASSKEY_RP_NAME", "Local Platform Lab"),
		PasskeyOrigins:     splitCSV(envOr("PASSKEY_ORIGINS", "https://localhost:18443")),
	}, refreshStore, credentialStore)
	if err != nil {
		log.Fatalf("configure auth: %v", err)
	}
	authorizationService := rbac.NewService(rbac.NewPostgresRepository(db))

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	server := userserver.NewGRPCServerAt(c.GRPC.Address, userserver.NewUserServer(authService, authorizationService, registrationService), logger, c.GRPC.Timeout)

	podIP := os.Getenv("POD_IP")
	if podIP == "" {
		podIP = "127.0.0.1"
	}
	if registrationEnabled(os.Getenv("SERVICE_REGISTRATION_MODE")) {
		registration, registerErr := serviceregistry.RegisterBoth(context.Background(), serviceregistry.Config{
			ServiceName:   "user-service",
			NacosAddress:  os.Getenv("NACOS_SERVER_ADDR"),
			EtcdEndpoints: os.Getenv("ETCD_ENDPOINTS"),
		}, joinAdvertisedAddress(podIP, c.GRPC.Address))
		if registerErr != nil {
			log.Fatalf("register service: %v", registerErr)
		}
		defer registration.Close()
	}

	app := kratos.New(
		kratos.Name(c.Name),
		kratos.Version("v3"),
		kratos.Logger(logger),
		kratos.Server(server),
		kratos.StopTimeout(10*time.Second),
	)
	if err := app.Run(); err != nil {
		log.Fatalf("run %s: %v", c.Name, err)
	}
}

func registrationEnabled(mode string) bool {
	return !strings.EqualFold(strings.TrimSpace(mode), "disabled")
}

func bootstrapIdentityEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "enabled":
		return true
	default:
		return false
	}
}

func joinAdvertisedAddress(host, listenAddress string) string {
	_, port, err := net.SplitHostPort(listenAddress)
	if err != nil {
		log.Fatalf("parse listen address: %v", err)
	}
	return net.JoinHostPort(host, port)
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func mustEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

func mustEd25519PrivateKey(name string) ed25519.PrivateKey {
	encoded := mustEnv(name)
	der, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		log.Fatalf("decode %s: %v", name, err)
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		log.Fatalf("parse %s: %v", name, err)
	}
	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		log.Fatalf("%s is not an Ed25519 private key", name)
	}
	return privateKey
}
