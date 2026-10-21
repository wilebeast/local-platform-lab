package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	userv1 "github.com/wilebeast/local-platform-lab/api/user/v1"
	"github.com/wilebeast/local-platform-lab/app/user-service/internal/auth"
	userserver "github.com/wilebeast/local-platform-lab/app/user-service/internal/server"
	"github.com/wilebeast/local-platform-lab/pkg/serviceregistry"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
)

var configFile = flag.String("f", "etc/user-service.yaml", "configuration file")

func main() {
	flag.Parse()

	var c zrpc.RpcServerConf
	conf.MustLoad(*configFile, &c)

	redisAddress := envOr("REDIS_ADDR", "redis.redis.svc.cluster.local:6379")
	refreshStore := auth.NewRedisStore(redisAddress)
	defer refreshStore.Close()
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPing()
	if err := refreshStore.Ping(pingCtx); err != nil {
		log.Fatalf("connect to Redis at %s: %v", redisAddress, err)
	}
	authService, err := auth.NewService(auth.Config{
		JWTPrivateKey: mustEd25519PrivateKey("JWT_PRIVATE_KEY_B64"),
		JWTKeyID:      envOr("JWT_KEY_ID", "local-auth-1"),
		Issuer:        "local-platform-lab", Audience: "api-bff",
		AccessTTL: 30 * time.Minute, RefreshTTL: 7 * 24 * time.Hour,
		DemoEmail:          envOr("DEMO_USER_EMAIL", "demo@local.test"),
		DemoPassword:       envOr("DEMO_USER_PASSWORD", "local-demo-password"),
		DemoUserID:         envOr("DEMO_USER_ID", "local-user"),
		TOTPEncryptionKey:  mustEnv("TOTP_ENCRYPTION_KEY"),
		RecoveryCodePepper: mustEnv("RECOVERY_CODE_PEPPER"),
	}, refreshStore)
	if err != nil {
		log.Fatalf("configure auth: %v", err)
	}

	server := zrpc.MustNewServer(c, func(grpcServer *grpc.Server) {
		userv1.RegisterUserServiceServer(grpcServer, userserver.NewUserServer(authService))
	})
	defer server.Stop()

	podIP := os.Getenv("POD_IP")
	if podIP == "" {
		podIP = "127.0.0.1"
	}
	_, port, err := net.SplitHostPort(c.ListenOn)
	if err != nil {
		log.Fatalf("parse listen address: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	registration, err := serviceregistry.RegisterBoth(ctx, serviceregistry.Config{
		ServiceName:   "user-service",
		NacosAddress:  os.Getenv("NACOS_SERVER_ADDR"),
		EtcdEndpoints: os.Getenv("ETCD_ENDPOINTS"),
	}, net.JoinHostPort(podIP, port))
	if err != nil {
		log.Fatalf("register service: %v", err)
	}
	defer registration.Close()

	fmt.Printf("Starting %s at %s; registered %s:%s in Nacos and Etcd\n", c.Name, c.ListenOn, podIP, port)
	server.Start()
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
