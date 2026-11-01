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

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/segmentio/kafka-go"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/rawarchive"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	secure, err := strconv.ParseBool(optional("MINIO_SECURE", "false"))
	if err != nil {
		log.Fatal("MINIO_SECURE must be a boolean")
	}
	client, err := minio.New(required("MINIO_ENDPOINT"), &minio.Options{
		Creds: credentials.NewStaticV4(
			required("MINIO_ACCESS_KEY"), required("MINIO_SECRET_KEY"), "",
		),
		Secure: secure,
	})
	if err != nil {
		log.Fatal(err)
	}
	store, err := rawarchive.NewMinIOObjectStore(client, required("MINIO_BUCKET"))
	if err != nil {
		log.Fatal(err)
	}
	for attempt := 1; attempt <= 30; attempt++ {
		err = store.EnsureBucket(ctx)
		if err == nil {
			break
		}
		if attempt == 30 {
			log.Fatalf("ensure raw bucket: %v", err)
		}
		time.Sleep(time.Second)
	}
	archive := rawarchive.NewArchive(store)
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:               []string{required("KAFKA_BROKER")},
		Topic:                 required("KAFKA_TOPIC"),
		GroupID:               required("KAFKA_GROUP_ID"),
		MinBytes:              1,
		MaxBytes:              10e6,
		WatchPartitionChanges: true,
	})
	defer reader.Close()

	log.Printf("risk raw sink started")
	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("fetch raw event: %v", err)
			time.Sleep(time.Second)
			continue
		}
		result, err := archive.Store(ctx, message.Value)
		if err != nil {
			if errors.Is(err, rawarchive.ErrInvalidEnvelope) {
				log.Printf("discard invalid raw event at offset %d: %v", message.Offset, err)
				commit(ctx, reader, message)
				continue
			}
			if errors.Is(err, rawarchive.ErrObjectConflict) {
				log.Printf("discard conflicting raw event at offset %d: %v", message.Offset, err)
				commit(ctx, reader, message)
				continue
			}
			log.Printf("archive raw event at offset %d: %v", message.Offset, err)
			time.Sleep(time.Second)
			continue
		}
		commit(ctx, reader, message)
		log.Printf(
			"archived raw event %s object=%s replay=%t",
			result.EventID, result.ObjectKey, result.Replay,
		)
	}
}

func commit(ctx context.Context, reader *kafka.Reader, message kafka.Message) {
	if err := reader.CommitMessages(ctx, message); err != nil && ctx.Err() == nil {
		log.Printf("commit raw event at offset %d: %v", message.Offset, err)
	}
}

func required(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

func optional(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
