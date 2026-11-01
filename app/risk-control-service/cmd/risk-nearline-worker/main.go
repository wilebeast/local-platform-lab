package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/risk"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	policyVersion, err := strconv.Atoi(required("RISK_POLICY_VERSION"))
	if err != nil || policyVersion <= 0 {
		log.Fatal("RISK_POLICY_VERSION must be a positive integer")
	}
	pool, err := pgxpool.New(ctx, required("POSTGRES_DSN"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}

	store := risk.NewPostgresStore(pool)
	loader := risk.NewPolicySnapshotLoader(store, risk.NewRPCFactorAdapterRegistry())
	runtime, err := risk.LoadPolicySnapshotRuntime(
		ctx, loader, required("RISK_POLICY_KEY"), policyVersion,
	)
	if err != nil {
		log.Fatalf("load configured risk policy: %v", err)
	}
	processor := risk.NewNearlineProcessor(risk.NewRealtimeDecisionService(pool, runtime))
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:               []string{required("KAFKA_BROKER")},
		Topic:                 required("KAFKA_TOPIC"),
		GroupID:               required("KAFKA_GROUP_ID"),
		MinBytes:              1,
		MaxBytes:              10e6,
		WatchPartitionChanges: true,
	})
	defer reader.Close()

	log.Printf("risk nearline worker started")
	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("fetch nearline event: %v", err)
			time.Sleep(time.Second)
			continue
		}
		event, err := risk.DecodeNearlineDecisionEvent(message.Value)
		if err != nil {
			log.Printf("discard invalid nearline event at offset %d: %v", message.Offset, err)
			commit(ctx, reader, message)
			continue
		}
		result, err := processor.Handle(ctx, event)
		if err != nil {
			var conflict risk.ConflictError
			if errors.As(err, &conflict) {
				log.Printf("discard conflicting nearline event %s: %v", event.EventID, err)
				commit(ctx, reader, message)
				continue
			}
			log.Printf("process nearline event %s: %v", event.EventID, err)
			time.Sleep(time.Second)
			continue
		}
		commit(ctx, reader, message)
		log.Printf(
			"processed nearline event %s evaluation %s decision %s replay=%t",
			event.EventID, result.Decision.EvaluationID, result.Decision.Decision, result.Replay,
		)
	}
}

func commit(ctx context.Context, reader *kafka.Reader, message kafka.Message) {
	if err := reader.CommitMessages(ctx, message); err != nil && ctx.Err() == nil {
		log.Printf("commit nearline event at offset %d: %v", message.Offset, err)
	}
}

func required(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}
