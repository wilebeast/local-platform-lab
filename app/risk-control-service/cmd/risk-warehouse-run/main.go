package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/riskwarehouse"
)

func main() {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, mustEnv("POSTGRES_DSN"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	recorder := riskwarehouse.NewPostgresWarehouseRunRecorder(pool)
	operation := mustEnv("RUN_OPERATION")
	switch operation {
	case "start":
		asOf, err := time.Parse(time.RFC3339, mustEnv("FEATURE_AS_OF"))
		if err != nil {
			log.Fatal("FEATURE_AS_OF must use RFC3339")
		}
		featureVersion, err := positiveInt("FEATURE_SET_VERSION")
		if err != nil {
			log.Fatal(err)
		}
		policyVersion, err := positiveInt("RISK_POLICY_VERSION")
		if err != nil {
			log.Fatal(err)
		}
		err = recorder.Start(ctx, riskwarehouse.WarehouseRunCommand{
			DAGRunID: mustEnv("DAG_RUN_ID"), TriggerType: envOr("RISK_TRIGGER_TYPE", "manual"),
			RunReason: os.Getenv("RISK_RUN_REASON"), AsOf: asOf.UTC(),
			FeatureSetKey: "subject_fund_activity_24h", FeatureVersion: featureVersion,
			PolicyKey: mustEnv("RISK_POLICY_KEY"), PolicyVersion: policyVersion,
		})
		if err != nil {
			log.Fatal(err)
		}
		writeJSON(map[string]any{"dag_run_id": mustEnv("DAG_RUN_ID"), "status": "running"})
	case "finalize":
		status := mustEnv("RUN_STATUS")
		snapshotCount, err := nonNegativeInt("RUN_SNAPSHOT_COUNT")
		if err != nil {
			log.Fatal(err)
		}
		failureCount, err := nonNegativeInt("RUN_FAILURE_COUNT")
		if err != nil {
			log.Fatal(err)
		}
		if err := recorder.FinalizeOutcome(ctx, mustEnv("DAG_RUN_ID"), status, snapshotCount, failureCount); err != nil {
			log.Fatal(err)
		}
		writeJSON(map[string]any{"dag_run_id": mustEnv("DAG_RUN_ID"), "status": status})
		if os.Getenv("FAIL_AFTER_FINALIZE") == "true" {
			os.Exit(1)
		}
	default:
		log.Fatalf("unsupported RUN_OPERATION %q", operation)
	}
}

func nonNegativeInt(key string) (int, error) {
	value, err := strconv.Atoi(mustEnv(key))
	if err != nil || value < 0 {
		return 0, &nonNegativeValueError{key}
	}
	return value, nil
}

type nonNegativeValueError struct{ key string }

func (e *nonNegativeValueError) Error() string { return e.key + " must be a non-negative integer" }

func positiveInt(key string) (int, error) {
	value, err := strconv.Atoi(mustEnv(key))
	if err != nil || value <= 0 {
		return 0, &valueError{key}
	}
	return value, nil
}

type valueError struct{ key string }

func (e *valueError) Error() string { return e.key + " must be a positive integer" }

func writeJSON(value any) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		log.Fatal(err)
	}
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
