package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	stepSeconds, err := strconv.ParseInt(mustEnv("BACKFILL_STEP_SECONDS"), 10, 64)
	if err != nil || stepSeconds <= 0 {
		log.Fatal("BACKFILL_STEP_SECONDS must be a positive integer")
	}
	start := mustTime("BACKFILL_START_AS_OF")
	end := mustTime("BACKFILL_END_AS_OF")
	coordinator := riskwarehouse.NewBackfillCoordinator(
		riskwarehouse.NewPostgresBackfillStore(pool),
		&commandTimepointRunner{},
	)
	summary, runErr := coordinator.Run(ctx, riskwarehouse.BackfillCommand{
		BackfillID: mustEnv("BACKFILL_ID"), Start: start, End: end,
		Step: time.Duration(stepSeconds) * time.Second,
	})
	if err := json.NewEncoder(os.Stdout).Encode(summary); err != nil {
		log.Fatal(err)
	}
	if runErr != nil {
		log.Fatal(runErr)
	}
}

type commandTimepointRunner struct{}

func (r *commandTimepointRunner) Run(ctx context.Context, backfillID string, point time.Time) error {
	childRunID := fmt.Sprintf("%s:%d", backfillID, point.UTC().Unix())
	common := map[string]string{
		"FEATURE_AS_OF":       point.UTC().Format(time.RFC3339),
		"FEATURE_RUN_ID":      childRunID + ":subject_fund_activity_24h:v1",
		"DAG_RUN_ID":          childRunID,
		"FEATURE_SET_VERSION": envOr("FEATURE_SET_VERSION", "1"),
		"RISK_POLICY_KEY":     envOr("RISK_POLICY_KEY", "warehouse_rapid_outflow"),
		"RISK_POLICY_VERSION": envOr("RISK_POLICY_VERSION", "1"),
		"RISK_TRIGGER_TYPE":   "backfill-child",
		"RISK_RUN_REASON":     "parent=" + backfillID,
	}
	if err := runCommand(ctx, "/risk-warehouse-run", merge(common, map[string]string{"RUN_OPERATION": "start"})); err != nil {
		return err
	}
	steps := []string{"/risk-feature-snapshot", "/risk-feature-validator", "/risk-feature-evaluator"}
	for _, binary := range steps {
		if err := runCommand(ctx, binary, common); err != nil {
			_ = runCommand(ctx, "/risk-warehouse-run", merge(common, map[string]string{
				"RUN_OPERATION": "finalize", "RUN_STATUS": "failed",
				"RUN_SNAPSHOT_COUNT": "0", "RUN_FAILURE_COUNT": "1",
			}))
			return fmt.Errorf("%s at %s: %w", binary, point.UTC().Format(time.RFC3339), err)
		}
	}
	return runCommand(ctx, "/risk-warehouse-run", merge(common, map[string]string{
		"RUN_OPERATION": "finalize", "RUN_STATUS": "succeeded",
		"RUN_SNAPSHOT_COUNT": "1", "RUN_FAILURE_COUNT": "0",
	}))
}

func runCommand(ctx context.Context, binary string, overrides map[string]string) error {
	command := exec.CommandContext(ctx, binary)
	command.Env = mergedEnvironment(overrides)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func mergedEnvironment(overrides map[string]string) []string {
	values := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, found := strings.Cut(item, "=")
		if found {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func merge(left, right map[string]string) map[string]string {
	result := make(map[string]string, len(left)+len(right))
	for key, value := range left {
		result[key] = value
	}
	for key, value := range right {
		result[key] = value
	}
	return result
}

func mustTime(key string) time.Time {
	value, err := time.Parse(time.RFC3339, mustEnv(key))
	if err != nil {
		log.Fatalf("%s must use RFC3339", key)
	}
	return value.UTC()
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
