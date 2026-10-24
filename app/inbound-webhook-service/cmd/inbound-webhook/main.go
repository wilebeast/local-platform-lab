package main

import (
	"context"
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/inbound-webhook-service/internal/provider"
	"github.com/wilebeast/local-platform-lab/app/inbound-webhook-service/internal/receiver"
	"github.com/wilebeast/local-platform-lab/app/inbound-webhook-service/internal/storage"
	"github.com/wilebeast/local-platform-lab/app/inbound-webhook-service/internal/worker"
)

//go:embed schema.sql
var schema string

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, required("POSTGRES_DSN"))
	if err != nil {
		log.Fatalf("connect postgres: %v", err)
	}
	defer pool.Close()
	if err := waitForPostgres(ctx, pool); err != nil {
		log.Fatalf("wait for postgres: %v", err)
	}
	if err := applySchema(ctx, pool); err != nil {
		log.Fatalf("apply schema: %v", err)
	}
	repository := storage.NewPostgres(pool)
	switch envOr("RUN_MODE", "receiver") {
	case "receiver":
		runReceiver(ctx, pool, repository)
	case "worker":
		runWorker(ctx, pool, repository)
	default:
		log.Fatal("RUN_MODE must be receiver or worker")
	}
}

func applySchema(ctx context.Context, pool *pgxpool.Pool) error {
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock(73418201)"); err != nil {
		return err
	}
	defer func() { _, _ = connection.Exec(context.Background(), "SELECT pg_advisory_unlock(73418201)") }()
	_, err = connection.Exec(ctx, schema)
	return err
}

func runReceiver(ctx context.Context, pool *pgxpool.Pool, repository *storage.Postgres) {
	cardKeys, err := cardCredentials(
		required("DEMO_CARD_ED25519_PUBLIC_KEY"),
		required("DEMO_CARD_ED25519_PUBLIC_KEY_V2"),
		envOr("DEMO_CARD_ACTIVE_KEY_IDS", "card-v1,card-v2"),
	)
	if err != nil {
		log.Fatal(err)
	}
	integrations := map[string]receiver.Integration{
		"demo-payments": {
			Code: "demo-payments", Domain: "payment", EventPrefix: "payment.", Adapter: provider.HMACAdapter{},
			Credentials: map[string]provider.Credential{"payments-v1": {KeyID: "payments-v1", HMACSecret: []byte(required("DEMO_PAYMENTS_HMAC_SECRET"))}},
		},
		"demo-card": {
			Code: "demo-card", Domain: "card", EventPrefix: "card.", Adapter: provider.Ed25519Adapter{},
			Credentials: cardKeys,
		},
		"demo-verification": {
			Code: "demo-verification", Domain: "verification", EventPrefix: "verification.", Adapter: provider.HMACAdapter{},
			Credentials: map[string]provider.Credential{"verification-v1": {KeyID: "verification-v1", HMACSecret: []byte(required("DEMO_VERIFICATION_HMAC_SECRET"))}},
		},
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v3/provider-webhooks/", receiver.NewHandler(integrations, repository, time.Now))
	mux.HandleFunc("GET /healthz", healthHandler(pool))
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go shutdownHTTP(ctx, server)
	log.Print("inbound webhook receiver listening on :8080")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func cardCredentials(v1Encoded, v2Encoded, activeKeyIDs string) (map[string]provider.Credential, error) {
	v1, err := decodeCardPublicKey("card-v1", v1Encoded)
	if err != nil {
		return nil, err
	}
	v2, err := decodeCardPublicKey("card-v2", v2Encoded)
	if err != nil {
		return nil, err
	}
	available := map[string]provider.Credential{
		"card-v1": {KeyID: "card-v1", Ed25519PublicKey: v1},
		"card-v2": {KeyID: "card-v2", Ed25519PublicKey: v2},
	}
	active := make(map[string]provider.Credential)
	for _, keyID := range strings.Split(activeKeyIDs, ",") {
		keyID = strings.TrimSpace(keyID)
		credential, exists := available[keyID]
		if !exists {
			return nil, fmt.Errorf("unknown active card key %q", keyID)
		}
		active[keyID] = credential
	}
	if len(active) == 0 {
		return nil, fmt.Errorf("at least one active card key is required")
	}
	return active, nil
}

func decodeCardPublicKey(keyID, encoded string) (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%s must be a base64 Ed25519 public key", keyID)
	}
	return ed25519.PublicKey(decoded), nil
}

func runWorker(ctx context.Context, pool *pgxpool.Pool, repository *storage.Postgres) {
	processor := worker.NewProcessor(repository, repository, time.Now, 3)
	healthServer := &http.Server{Addr: ":8080", Handler: http.HandlerFunc(healthHandler(pool)), ReadHeaderTimeout: 3 * time.Second}
	go func() {
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("worker health server: %v", err)
		}
	}()
	go shutdownHTTP(ctx, healthServer)
	log.Print("inbound webhook worker started")
	for ctx.Err() == nil {
		processed, err := processor.ProcessNext(ctx)
		if err != nil {
			log.Printf("process webhook: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if !processed {
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func healthHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			http.Error(response, "postgres unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = response.Write([]byte("ok"))
	}
}

func shutdownHTTP(ctx context.Context, server *http.Server) {
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
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

func required(name string) string {
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
