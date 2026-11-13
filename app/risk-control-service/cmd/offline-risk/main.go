package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/risk"
)

func main() {
	ctx := context.Background()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		log.Fatal("POSTGRES_DSN is required")
	}
	policyKey := envOr("OFFLINE_POLICY_KEY", "rapid_fund_movement")
	policyVersion, err := strconv.Atoi(envOr("OFFLINE_POLICY_VERSION", "1"))
	if err != nil || policyVersion <= 0 {
		log.Fatal("OFFLINE_POLICY_VERSION must be a positive integer")
	}
	asOf := time.Now().UTC().Truncate(time.Hour)
	if raw := os.Getenv("OFFLINE_AS_OF"); raw != "" {
		asOf, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			log.Fatal("OFFLINE_AS_OF must use RFC3339")
		}
	}
	runID := os.Getenv("OFFLINE_RUN_ID")
	if runID == "" {
		runID = fmt.Sprintf("offline-%s-v%d-%s", policyKey, policyVersion, asOf.Format("20060102T150405Z"))
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	scanner := risk.NewOfflineScanner(risk.NewPostgresStore(pool))
	command := risk.OfflineRunCommand{
		RunID: runID, AsOf: asOf, PolicyKey: policyKey, PolicyVersion: policyVersion,
	}
	dryRun := envOr("OFFLINE_DRY_RUN", "false") == "true"
	var result risk.OfflineRunResult
	var replay bool
	if dryRun {
		result, err = scanner.Preview(ctx, command)
	} else {
		result, replay, err = scanner.Run(ctx, command)
	}
	if err != nil {
		log.Fatal(err)
	}
	output := struct {
		risk.OfflineRunResult
		IdempotentReplay bool `json:"idempotent_replay"`
		DryRun           bool `json:"dry_run"`
	}{OfflineRunResult: result, IdempotentReplay: replay, DryRun: dryRun}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
