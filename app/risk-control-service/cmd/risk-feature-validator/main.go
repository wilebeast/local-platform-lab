package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/riskwarehouse"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func main() {
	ctx := context.Background()
	asOf, err := time.Parse(time.RFC3339, mustEnv("FEATURE_AS_OF"))
	if err != nil {
		log.Fatal("FEATURE_AS_OF must use RFC3339")
	}
	version, err := strconv.Atoi(envOr("FEATURE_SET_VERSION", "1"))
	if err != nil || version <= 0 {
		log.Fatal("FEATURE_SET_VERSION must be a positive integer")
	}
	clickHouse, err := warehouse.NewClickHouse(
		mustEnv("CLICKHOUSE_HTTP_ENDPOINT"), envOr("CLICKHOUSE_USER", "default"),
		os.Getenv("CLICKHOUSE_PASSWORD"), &http.Client{Timeout: 10 * time.Second},
	)
	if err != nil {
		log.Fatal(err)
	}
	snapshot, found, err := clickHouse.ReadLatestFundActivitySnapshot(
		ctx, int32(version), mustEnv("RISK_SUBJECT_TYPE"), mustEnv("RISK_SUBJECT_ID"),
		mustEnv("RISK_CURRENCY"), asOf.UTC(),
	)
	if err != nil {
		log.Fatal(err)
	}
	if !found {
		log.Fatal("feature_snapshot_missing")
	}
	if err := riskwarehouse.ValidateFundActivitySnapshot(snapshot, asOf); err != nil {
		log.Fatal(err)
	}
	log.Printf(
		"quality_gate=passed feature_set=%s version=%d subject=%s/%s currency=%s as_of=%s",
		snapshot.FeatureSetKey, snapshot.FeatureSetVersion, snapshot.SubjectType,
		snapshot.SubjectID, snapshot.Currency, asOf.UTC().Format(time.RFC3339),
	)
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
