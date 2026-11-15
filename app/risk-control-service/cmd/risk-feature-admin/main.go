package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/featureplatform"
)

type adminConfig struct {
	postgresDSN   string
	schema        string
	listenAddress string
	credentials   []featureplatform.GovernanceBearerCredential
}

func main() {
	config, err := loadAdminConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, config.postgresDSN)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	principalResolver, err := featureplatform.NewBearerGovernancePrincipalResolver(config.credentials)
	if err != nil {
		log.Fatal(err)
	}
	promotion := featureplatform.NewPostgresPromotionRepository(pool, config.schema)
	governanceRepository := featureplatform.NewPostgresFeatureServingGovernanceRepository(pool, config.schema)
	governance := featureplatform.NewFeatureServingGovernance(
		governanceRepository,
		featureplatform.NewPromotionController(promotion, promotion),
	)
	mux := http.NewServeMux()
	mux.Handle("/admin/v1/feature-serving/", featureplatform.NewFeatureServingGovernanceHTTPHandler(governance, principalResolver))
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, request *http.Request) {
		if err := pool.Ping(request.Context()); err != nil {
			http.Error(writer, "postgres unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = writer.Write([]byte("ok"))
	})
	server := &http.Server{
		Addr: config.listenAddress, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("feature serving admin listening on %s", config.listenAddress)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func loadAdminConfig(getenv func(string) string) (adminConfig, error) {
	postgresDSN := getenv("POSTGRES_DSN")
	if postgresDSN == "" {
		return adminConfig{}, fmt.Errorf("POSTGRES_DSN is required")
	}
	credentialsJSON := getenv("FEATURE_ADMIN_CREDENTIALS_JSON")
	if credentialsJSON == "" {
		return adminConfig{}, fmt.Errorf("FEATURE_ADMIN_CREDENTIALS_JSON is required")
	}
	var credentials []featureplatform.GovernanceBearerCredential
	if err := json.Unmarshal([]byte(credentialsJSON), &credentials); err != nil {
		return adminConfig{}, fmt.Errorf("decode FEATURE_ADMIN_CREDENTIALS_JSON: %w", err)
	}
	if _, err := featureplatform.NewBearerGovernancePrincipalResolver(credentials); err != nil {
		return adminConfig{}, err
	}
	schema := getenv("FEATURE_REGISTRY_SCHEMA")
	if schema == "" {
		schema = "risk_control"
	}
	listenAddress := getenv("FEATURE_ADMIN_LISTEN_ADDRESS")
	if listenAddress == "" {
		listenAddress = ":8081"
	}
	return adminConfig{
		postgresDSN: postgresDSN, schema: schema,
		listenAddress: listenAddress, credentials: credentials,
	}, nil
}
