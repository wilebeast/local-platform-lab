package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/acquiring-service/internal/acquiring"
)

func main() {
	ctx := context.Background()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		log.Fatal("POSTGRES_DSN is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	for attempt := 0; attempt < 30; attempt++ {
		if err = pool.Ping(ctx); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		log.Fatal(err)
	}
	if err := acquiring.EnsureAcquiringSchema(ctx, pool); err != nil {
		log.Fatal(err)
	}
	store := acquiring.NewPostgresPaymentStore(pool)
	mode := os.Getenv("RUN_MODE")
	if mode == "" {
		mode = "api"
	}
	if mode == "worker" {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := store.ProcessNextProviderEvent(ctx); err != nil {
				log.Printf("process provider event: %v", err)
			}
		}
		return
	}
	providerSecrets := map[string]string{
		"demo-card": os.Getenv("DEMO_CARD_WEBHOOK_SECRET"),
		"demo-bank": os.Getenv("DEMO_BANK_WEBHOOK_SECRET"),
	}
	if providerSecrets["demo-card"] == "" || providerSecrets["demo-bank"] == "" {
		log.Fatal("provider webhook secrets are required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.Handle("POST /api/v1/payments", acquiring.NewCreatePaymentHandler(store))
	mux.Handle("POST /api/v1/refunds", acquiring.NewRefundPaymentHandler(store))
	mux.Handle("POST /api/v1/channel-settlements", acquiring.NewChannelNetSettlementHandler(store))
	mux.Handle("POST /api/v1/channel-cash-settlements", acquiring.NewChannelCashSettlementHandler(store))
	mux.Handle("POST /api/v1/merchant-payouts", acquiring.NewMerchantPayoutHandler(store))
	mux.Handle("POST /api/v1/reconciliation/payouts", acquiring.NewPayoutReconciliationHandler(store))
	mux.Handle("POST /api/v1/reconciliation/issues/resolve", acquiring.NewReconciliationIssueResolutionHandler(store))
	mux.Handle("POST /api/v1/reconciliation/issues/correct-missing-payout", acquiring.NewMissingProviderPayoutCorrectionHandler(store))
	mux.Handle("POST /api/v1/provider-events/{provider}", acquiring.NewProviderEventHandler(store, providerSecrets))
	log.Fatal(http.ListenAndServe(":8080", mux))
}
