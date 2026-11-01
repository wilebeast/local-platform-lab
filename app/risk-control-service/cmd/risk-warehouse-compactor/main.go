package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func main() {
	ctx := context.Background()
	eventDate := envOr("COMPACTION_EVENT_DATE", time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly))
	client, err := minio.New(mustEnv("MINIO_ENDPOINT"), &minio.Options{
		Creds:  credentials.NewStaticV4(mustEnv("MINIO_ACCESS_KEY"), mustEnv("MINIO_SECRET_KEY"), ""),
		Secure: strings.EqualFold(os.Getenv("MINIO_SECURE"), "true"),
	})
	if err != nil {
		log.Fatal(err)
	}
	objectStore, err := warehouse.NewMinIOWarehouse(
		client, envOr("MINIO_RAW_BUCKET", "risk-raw-events"), envOr("MINIO_WAREHOUSE_BUCKET", "risk-warehouse"),
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := objectStore.EnsureWarehouseBucket(ctx); err != nil {
		log.Fatal(err)
	}
	objects, err := objectStore.LoadFundMovementPartition(ctx, eventDate)
	if err != nil {
		log.Fatal(err)
	}
	if len(objects) == 0 {
		log.Printf("no raw fund movement objects for event_date=%s", eventDate)
		return
	}
	result, err := warehouse.NewFundMovementCompactor().Compact(ctx, warehouse.CompactionRequest{
		EventDate: eventDate, Objects: objects,
	})
	if err != nil {
		log.Fatal(err)
	}
	parquetKey := result.ParquetObjectKey(eventDate)
	replay, err := objectStore.PutIfAbsent(ctx, parquetKey, result.Parquet, "application/vnd.apache.parquet", result.SHA256)
	if err != nil {
		log.Fatal(err)
	}
	manifest := struct {
		EventDate  string                     `json:"event_date"`
		ParquetKey string                     `json:"parquet_key"`
		SHA256     string                     `json:"sha256"`
		RowCount   int                        `json:"row_count"`
		Rejected   []warehouse.RejectedObject `json:"rejected"`
	}{eventDate, parquetKey, result.SHA256, result.RowCount, result.Rejected}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		log.Fatal(err)
	}
	manifestDigest := sha256.Sum256(manifestBytes)
	manifestSHA256 := hex.EncodeToString(manifestDigest[:])
	manifestKey := "manifests/v1/fact_fund_movements/event_date=" + eventDate +
		"/manifest_sha256=" + manifestSHA256 + "/manifest.json"
	if _, err := objectStore.PutIfAbsent(
		ctx, manifestKey, manifestBytes, "application/json", manifestSHA256,
	); err != nil {
		log.Fatal(err)
	}

	clickHouse, err := warehouse.NewClickHouse(
		mustEnv("CLICKHOUSE_HTTP_ENDPOINT"), envOr("CLICKHOUSE_USER", "default"), os.Getenv("CLICKHOUSE_PASSWORD"),
		&http.Client{Timeout: 60 * time.Second},
	)
	if err != nil {
		log.Fatal(err)
	}
	if err := clickHouse.EnsureSchema(ctx); err != nil {
		log.Fatal(err)
	}
	if result.RowCount > 0 {
		if err := clickHouse.LoadFundMovementParquet(ctx, result.Parquet); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf(
		"compacted event_date=%s raw_objects=%d rows=%d rejected=%d parquet=%s replay=%t sha256=%s",
		eventDate, len(objects), result.RowCount, len(result.Rejected), parquetKey, replay, result.SHA256,
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
