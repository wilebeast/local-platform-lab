package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/wilebeast/local-platform-lab/app/webhook-sender/internal/delivery"
)

type eventEnvelope struct {
	ID string `json:"id"`
}

func main() {
	broker := required("KAFKA_BROKER")
	topic := required("KAFKA_TOPIC")
	client := delivery.NewClient(required("WEBHOOK_ENDPOINT"), required("WEBHOOK_SECRET"), time.Now)
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{broker}, Topic: topic, GroupID: "webhook-sender-v1",
		MinBytes: 1, MaxBytes: 10e6, WatchPartitionChanges: true,
	})
	defer reader.Close()
	log.Printf("webhook sender consuming %s from %s", topic, broker)
	for {
		message, err := reader.FetchMessage(context.Background())
		if err != nil {
			log.Printf("fetch event: %v", err)
			continue
		}
		var event eventEnvelope
		if err := json.Unmarshal(message.Value, &event); err != nil || event.ID == "" {
			log.Printf("skip invalid event at offset %d", message.Offset)
			_ = reader.CommitMessages(context.Background(), message)
			continue
		}
		if err := client.Deliver(context.Background(), event.ID, message.Value); err != nil {
			log.Printf("deliver %s: %v", event.ID, err)
			time.Sleep(time.Second)
			continue
		}
		if err := reader.CommitMessages(context.Background(), message); err != nil {
			log.Printf("commit %s: %v", event.ID, err)
			continue
		}
		log.Printf("delivered event %s", event.ID)
	}
}

func required(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}
