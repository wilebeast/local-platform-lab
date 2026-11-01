package main

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/riskwarehouse"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	thresholds := riskwarehouse.ReconciliationThresholds{
		AmountMinor: int64Env("RECONCILIATION_AMOUNT_THRESHOLD_MINOR", 0),
		Ratio:       floatEnv("RECONCILIATION_RATIO_THRESHOLD", 0.000001),
	}
	reconciler := riskwarehouse.NewFeatureReconciler(thresholds)
	postgres, err := pgxpool.New(ctx, required("POSTGRES_DSN"))
	if err != nil {
		log.Fatal(err)
	}
	defer postgres.Close()
	store := riskwarehouse.NewPostgresFeatureReconciliationStore(postgres)
	if err := store.EnsureSchema(ctx); err != nil {
		log.Fatalf("ensure reconciliation schema: %v", err)
	}
	clickHouse, err := warehouse.NewClickHouse(
		required("CLICKHOUSE_HTTP_ENDPOINT"), envOr("CLICKHOUSE_USER", "default"), os.Getenv("CLICKHOUSE_PASSWORD"),
		&http.Client{Timeout: 30 * time.Second},
	)
	if err != nil {
		log.Fatal(err)
	}
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:               []string{required("KAFKA_BROKER")},
		Topic:                 required("KAFKA_TOPIC"),
		GroupID:               required("KAFKA_GROUP_ID"),
		MinBytes:              1,
		MaxBytes:              10e6,
		WatchPartitionChanges: true,
	})
	defer reader.Close()

	log.Printf("risk feature reconciler started")
	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("fetch shadow snapshot: %v", err)
			time.Sleep(time.Second)
			continue
		}
		var stream warehouse.SubjectFundActivitySnapshot
		if err := json.Unmarshal(message.Value, &stream); err != nil || stream.FeatureSetKey == "" || stream.AsOfMillis <= 0 {
			log.Printf("discard invalid shadow snapshot at offset %d: %v", message.Offset, err)
			commit(ctx, reader, message)
			continue
		}
		batch, found, err := clickHouse.ReadLatestFundActivitySnapshot(
			ctx, stream.FeatureSetVersion, stream.SubjectType, stream.SubjectID, stream.Currency,
			time.UnixMilli(stream.AsOfMillis).UTC(),
		)
		if err != nil {
			log.Printf("read batch snapshot for offset %d: %v", message.Offset, err)
			time.Sleep(time.Second)
			continue
		}
		var result riskwarehouse.FeatureReconciliationResult
		if !found {
			batch = missingBatchSnapshot(stream)
			result = riskwarehouse.MissingBatchReconciliation()
		} else {
			result, err = reconciler.Compare(stream, batch)
			if err != nil {
				log.Printf("compare shadow snapshot at offset %d: %v", message.Offset, err)
				time.Sleep(time.Second)
				continue
			}
		}
		record, err := riskwarehouse.NewFeatureReconciliationRecord(stream, batch, thresholds, result)
		if err != nil {
			log.Printf("build reconciliation at offset %d: %v", message.Offset, err)
			time.Sleep(time.Second)
			continue
		}
		replay, err := store.Save(ctx, record)
		if err != nil {
			log.Printf("save reconciliation %s: %v", record.ComparisonID, err)
			time.Sleep(time.Second)
			continue
		}
		commit(ctx, reader, message)
		log.Printf(
			"reconciled comparison=%s subject=%s/%s as_of=%s status=%s alert=%s replay=%t",
			record.ComparisonID, stream.SubjectType, stream.SubjectID,
			time.UnixMilli(stream.AsOfMillis).UTC().Format(time.RFC3339),
			result.Status, record.AlertStatus, replay,
		)
	}
}

func missingBatchSnapshot(stream warehouse.SubjectFundActivitySnapshot) warehouse.SubjectFundActivitySnapshot {
	return warehouse.SubjectFundActivitySnapshot{
		FeatureSetKey:     stream.FeatureSetKey,
		FeatureSetVersion: stream.FeatureSetVersion,
		SubjectType:       stream.SubjectType,
		SubjectID:         stream.SubjectID,
		Currency:          stream.Currency,
		WindowStartMillis: stream.WindowStartMillis,
		WindowEndMillis:   stream.WindowEndMillis,
		AsOfMillis:        stream.AsOfMillis,
		SourceRunID:       "batch-missing",
	}
}

func commit(ctx context.Context, reader *kafka.Reader, message kafka.Message) {
	if err := reader.CommitMessages(ctx, message); err != nil && ctx.Err() == nil {
		log.Printf("commit shadow snapshot at offset %d: %v", message.Offset, err)
	}
}

func required(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf("%s is required", key)
	}
	return value
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func int64Env(key string, fallback int64) int64 {
	value := envOr(key, strconv.FormatInt(fallback, 10))
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		log.Fatalf("%s must be a non-negative integer", key)
	}
	return parsed
}

func floatEnv(key string, fallback float64) float64 {
	value := envOr(key, strconv.FormatFloat(fallback, 'g', -1, 64))
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < 0 || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		log.Fatalf("%s must be a finite non-negative number", key)
	}
	return parsed
}
