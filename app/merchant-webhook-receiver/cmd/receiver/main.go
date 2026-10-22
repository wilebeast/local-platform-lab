package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/wilebeast/local-platform-lab/app/merchant-webhook-receiver/internal/webhook"
)

func main() {
	secret := os.Getenv("WEBHOOK_SECRET")
	if secret == "" {
		log.Fatal("WEBHOOK_SECRET is required")
	}
	store := webhook.NewMemoryStore()
	mux := http.NewServeMux()
	mux.Handle("POST /webhooks/platform", webhook.NewHandler(secret, store, time.Now))
	mux.HandleFunc("GET /received-events", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{"events": store.Events()})
	})
	mux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusOK) })
	log.Printf("merchant webhook receiver listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
