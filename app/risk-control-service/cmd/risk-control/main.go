package main

import (
	"context"
	_ "embed"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/risk"
)

//go:embed schema.sql
var schema string

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		log.Fatal("POSTGRES_DSN is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	for i := 0; i < 30; i++ {
		if err = pool.Ping(ctx); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		log.Fatal(err)
	}
	if _, err = pool.Exec(ctx, schema); err != nil {
		log.Fatal(err)
	}
	store := risk.NewPostgresStore(pool)
	apiHandler := risk.NewHandler(store)
	policyKey := os.Getenv("RISK_POLICY_KEY")
	policyVersionText := os.Getenv("RISK_POLICY_VERSION")
	if policyKey != "" || policyVersionText != "" {
		if policyKey == "" || policyVersionText == "" {
			log.Fatal("RISK_POLICY_KEY and RISK_POLICY_VERSION must be configured together")
		}
		policyVersion, parseErr := strconv.Atoi(policyVersionText)
		if parseErr != nil || policyVersion <= 0 {
			log.Fatal("RISK_POLICY_VERSION must be a positive integer")
		}
		loader := risk.NewPolicySnapshotLoader(store, risk.NewRPCFactorAdapterRegistry())
		runtime, loadErr := risk.LoadPolicySnapshotRuntime(ctx, loader, policyKey, policyVersion)
		if loadErr != nil {
			log.Fatalf("load configured risk policy: %v", loadErr)
		}
		apiHandler = risk.NewHandlerWithRealtimeDecider(
			store,
			risk.NewRealtimeDecisionService(pool, runtime),
		)
		log.Printf("loaded risk policy %s version %d", policyKey, policyVersion)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", apiHandler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "postgres unavailable", 503)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("risk control lab listening on :8080")
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
