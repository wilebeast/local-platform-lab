package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func main() {
	ctx := context.Background()
	asOf := time.Now().UTC().Truncate(time.Hour)
	if value := os.Getenv("FEATURE_AS_OF"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			log.Fatalf("FEATURE_AS_OF must use RFC3339: %v", err)
		}
		asOf = parsed.UTC()
	}
	window := 24 * time.Hour
	clickHouse, err := warehouse.NewClickHouse(
		mustEnv("CLICKHOUSE_HTTP_ENDPOINT"), envOr("CLICKHOUSE_USER", "default"), os.Getenv("CLICKHOUSE_PASSWORD"),
		&http.Client{Timeout: 60 * time.Second},
	)
	if err != nil {
		log.Fatal(err)
	}
	postgres, err := pgxpool.New(ctx, mustEnv("POSTGRES_DSN"))
	if err != nil {
		log.Fatal(err)
	}
	defer postgres.Close()
	facts, err := clickHouse.ReadFundMovementFacts(ctx, asOf.Add(-window), asOf)
	if err != nil {
		log.Fatal(err)
	}
	snapshots, err := warehouse.NewFundActivityAggregator(window).Aggregate(ctx, asOf, facts)
	if err != nil {
		log.Fatal(err)
	}
	computedAt := time.Now().UTC()
	runID := envOr("FEATURE_RUN_ID", fmt.Sprintf("subject-fund-activity-v1-%d", asOf.UnixMilli()))
	for index := range snapshots {
		snapshots[index].SourceRunID = runID
		snapshots[index].ComputedAtMillis = computedAt.UnixMilli()
	}
	publisher := warehouse.NewFundActivitySnapshotPublisher(
		warehouse.NewPostgresFeatureRunRegistry(postgres),
		clickHouse,
	)
	if err := publisher.Publish(ctx, snapshots); err != nil {
		log.Fatal(err)
	}
	log.Printf(
		"feature_set=subject_fund_activity_24h version=1 as_of=%s run_id=%s facts=%d snapshots=%d",
		asOf.Format(time.RFC3339), runID, len(facts), len(snapshots),
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
