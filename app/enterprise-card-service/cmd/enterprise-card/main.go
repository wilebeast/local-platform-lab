package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/enterprise-card-service/internal/card"
)

//go:embed schema.sql
var schema string

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	mode := envOr("RUN_MODE", "api")
	if mode == "provider-simulator" {
		runHTTP(ctx, ":8080", card.NewProviderSimulatorHandler())
		return
	}
	pool, err := pgxpool.New(ctx, required("POSTGRES_DSN"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := waitForPostgres(ctx, pool); err != nil {
		log.Fatal(err)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		log.Fatal(err)
	}
	store := card.NewPostgresStore(pool)
	switch mode {
	case "api":
		mux := http.NewServeMux()
		cardHandler := card.NewHandler(store, time.Now, newID)
		mux.Handle("/api/v1/enterprise-cards", cardHandler)
		mux.Handle("/api/v3/card-lab/cards", cardHandler)
		mux.Handle("/api/v3/card-lab/provider-webhooks", card.NewWebhookHandler(store, []byte(required("PROVIDER_WEBHOOK_SECRET")), time.Now))
		mux.Handle("/api/v3/card-lab/accounts/fund", card.NewFinanceHandler(store))
		mux.Handle("/api/v3/card-lab/provider-authorizations", card.NewAuthorizationHandler(store, []byte(required("PROVIDER_WEBHOOK_SECRET")), time.Now))
		mux.Handle("/api/v3/card-lab/provider-authorization-reversals", card.NewAuthorizationReversalHandler(store, []byte(required("PROVIDER_WEBHOOK_SECRET")), time.Now))
		mux.Handle("/api/v3/card-lab/provider-authorization-settlements", card.NewAuthorizationSettlementHandler(store, []byte(required("PROVIDER_WEBHOOK_SECRET")), time.Now))
		mux.Handle("/api/v3/card-lab/provider-settlement-refunds", card.NewSettlementRefundHandler(store, []byte(required("PROVIDER_WEBHOOK_SECRET")), time.Now))
		mux.Handle("/api/v3/card-lab/ledger/reconciliation", card.NewOperationalLedgerReconciliationHandler(store))
		mux.HandleFunc("GET /healthz", databaseHealthHandler(pool))
		runHTTP(ctx, ":8080", mux)
	case "worker":
		provider := card.NewHTTPProvider(required("PROVIDER_BASE_URL"), &http.Client{Timeout: 5 * time.Second})
		processor := card.NewProcessor(store, provider, time.Now, 3)
		eventProcessor := card.NewProviderEventProcessor(store, time.Now)
		go runHTTP(ctx, ":8080", http.HandlerFunc(databaseHealthHandler(pool)))
		for ctx.Err() == nil {
			if processed, err := eventProcessor.ProcessNext(ctx); err != nil {
				log.Printf("process provider event: %v", err)
			} else if processed {
				continue
			}
			processed, err := processor.ProcessNext(ctx)
			if err != nil {
				log.Printf("process channel operation: %v", err)
				time.Sleep(time.Second)
			} else if !processed {
				time.Sleep(500 * time.Millisecond)
			}
		}
	default:
		log.Fatal("RUN_MODE must be api, worker, or provider-simulator")
	}
}

func databaseHealthHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		pingCtx, cancel := context.WithTimeout(request.Context(), time.Second)
		defer cancel()
		if err := pool.Ping(pingCtx); err != nil {
			http.Error(response, "postgres unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = response.Write([]byte("ok"))
	}
}

func runHTTP(ctx context.Context, address string, handler http.Handler) {
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("enterprise card lab listening on %s", address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func newID() string {
	random := make([]byte, 18)
	if _, err := rand.Read(random); err != nil {
		panic(err)
	}
	return "card_" + base64.RawURLEncoding.EncodeToString(random)
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
