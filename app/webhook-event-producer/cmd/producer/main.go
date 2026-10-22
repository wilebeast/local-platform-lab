package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/segmentio/kafka-go"
)

type profileUpdated struct {
	UserID             string `json:"user_id"`
	VerificationStatus string `json:"verification_status"`
}

type eventEnvelope struct {
	ID         string         `json:"id"`
	Event      string         `json:"event"`
	Version    int            `json:"version"`
	OccurredAt int64          `json:"occurred_at"`
	Data       profileUpdated `json:"data"`
}

func main() {
	writer := &kafka.Writer{Addr: kafka.TCP(required("KAFKA_BROKER")), Topic: required("KAFKA_TOPIC"), Balancer: &kafka.LeastBytes{}}
	defer writer.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /events/profile-updated", func(response http.ResponseWriter, request *http.Request) {
		var data profileUpdated
		if err := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20)).Decode(&data); err != nil || data.UserID == "" || data.VerificationStatus == "" {
			http.Error(response, "user_id and verification_status are required", http.StatusBadRequest)
			return
		}
		event := eventEnvelope{ID: randomID(), Event: "profile.updated", Version: 1, OccurredAt: time.Now().Unix(), Data: data}
		body, _ := json.Marshal(event)
		if err := writer.WriteMessages(request.Context(), kafka.Message{Key: []byte(event.ID), Value: body}); err != nil {
			http.Error(response, "failed to publish event", http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(response).Encode(event)
	})
	mux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusOK) })
	log.Printf("webhook event producer listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func required(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

func randomID() string {
	buffer := make([]byte, 18)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return "evt_" + base64.RawURLEncoding.EncodeToString(buffer)
}
