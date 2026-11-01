package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/riskwarehouse"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func main() {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, mustEnv("POSTGRES_DSN"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	clickHouse, err := warehouse.NewClickHouse(
		mustEnv("CLICKHOUSE_HTTP_ENDPOINT"), envOr("CLICKHOUSE_USER", "default"),
		os.Getenv("CLICKHOUSE_PASSWORD"), &http.Client{Timeout: 10 * time.Second},
	)
	if err != nil {
		log.Fatal(err)
	}
	index, err := riskwarehouse.NewClickHouseFeatureSnapshotIndex(clickHouse)
	if err != nil {
		log.Fatal(err)
	}
	auditor := riskwarehouse.NewLineageAuditor(
		riskwarehouse.NewPostgresWarehouseLineageCatalog(pool), index,
	)
	result, err := auditor.AuditRun(ctx, mustEnv("DAG_RUN_ID"))
	if err != nil {
		log.Fatal(err)
	}
	if err := riskwarehouse.NewPostgresLineageAuditRecorder(pool).Save(ctx, result); err != nil {
		log.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		log.Fatal(err)
	}
	if result.Status == riskwarehouse.LineageBroken && envOr("FAIL_ON_BROKEN", "true") == "true" {
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func mustEnv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("%s is required", key)
	}
	return value
}
