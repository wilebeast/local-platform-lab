package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/risk"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		log.Fatal("POSTGRES_DSN is required")
	}
	maxAttempts := 5
	if configured := os.Getenv("RISK_ACTION_MAX_ATTEMPTS"); configured != "" {
		parsed, err := strconv.Atoi(configured)
		if err != nil || parsed <= 0 {
			log.Fatal("RISK_ACTION_MAX_ATTEMPTS must be a positive integer")
		}
		maxAttempts = parsed
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	executor, err := risk.NewPolicyActionExecutor(
		pool,
		[]risk.PolicyActionEffect{risk.NewActivateRestrictionEffect(pool)},
		maxAttempts,
	)
	if err != nil {
		log.Fatal(err)
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	log.Printf("risk action worker started")
	for {
		processed, job, runErr := executor.RunOnce(ctx)
		if runErr != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("action %s attempt failed: %v", job.ActionID, runErr)
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
