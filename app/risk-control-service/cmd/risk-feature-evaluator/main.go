package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/risk"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/riskwarehouse"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func main() {
	ctx := context.Background()
	asOf, err := time.Parse(time.RFC3339, mustEnv("FEATURE_AS_OF"))
	if err != nil {
		log.Fatal("FEATURE_AS_OF must use RFC3339")
	}
	version, err := strconv.Atoi(envOr("RISK_POLICY_VERSION", "1"))
	if err != nil || version <= 0 {
		log.Fatal("RISK_POLICY_VERSION must be a positive integer")
	}
	pool, err := pgxpool.New(ctx, mustEnv("POSTGRES_DSN"))
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	clickHouse, err := warehouse.NewClickHouse(
		mustEnv("CLICKHOUSE_HTTP_ENDPOINT"), envOr("CLICKHOUSE_USER", "default"),
		os.Getenv("CLICKHOUSE_PASSWORD"), &http.Client{Timeout: 10 * time.Second},
	)
	if err != nil {
		log.Fatal(err)
	}
	reader, err := riskwarehouse.NewClickHouseSnapshotReader(clickHouse)
	if err != nil {
		log.Fatal(err)
	}
	adapters := riskwarehouse.NewFeatureAdapterRegistry()
	for _, adapter := range []riskwarehouse.FeatureAdapter{
		riskwarehouse.NewFundActivityInflowMinorAdapter(1, time.Hour),
		riskwarehouse.NewFundActivityOutflowRatioAdapter(1, time.Hour),
	} {
		if err := adapters.Register(adapter); err != nil {
			log.Fatal(err)
		}
	}
	provider := riskwarehouse.NewWarehouseFeatureFactorProvider(reader, adapters)
	loader := risk.NewPolicySnapshotLoaderWithProviders(
		risk.NewPostgresStore(pool), risk.NewRPCFactorAdapterRegistry(),
		[]risk.FactorProvider{provider},
	)
	policy, err := loader.LoadPublished(ctx, envOr("RISK_POLICY_KEY", "warehouse_rapid_outflow"), version)
	if err != nil {
		log.Fatal(err)
	}
	evaluation, err := policy.Evaluate(ctx, map[string]any{
		"subject_type": mustEnv("RISK_SUBJECT_TYPE"),
		"subject_id":   mustEnv("RISK_SUBJECT_ID"),
		"currency":     mustEnv("RISK_CURRENCY"),
		"as_of":        asOf.UTC(),
	})
	if err != nil {
		log.Fatal(err)
	}
	featureSnapshot, found, err := reader.ReadFundActivitySnapshot(ctx, riskwarehouse.FeatureSnapshotQuery{
		FeatureSetVersion: 1,
		SubjectType:       mustEnv("RISK_SUBJECT_TYPE"),
		SubjectID:         mustEnv("RISK_SUBJECT_ID"),
		Currency:          mustEnv("RISK_CURRENCY"),
		AsOf:              asOf.UTC(),
	})
	if err != nil {
		log.Fatal(err)
	}
	if !found || featureSnapshot.AsOfMillis != asOf.UTC().UnixMilli() ||
		featureSnapshot.SourceRunID != mustEnv("FEATURE_RUN_ID") {
		log.Fatal("exact feature snapshot is required for evaluation lineage")
	}
	replay, err := riskwarehouse.RecordWarehouseEvaluation(
		ctx,
		riskwarehouse.NewPostgresWarehouseEvaluationRecorder(pool),
		riskwarehouse.WarehouseEvaluationLineage{
			DAGRunID:        mustEnv("DAG_RUN_ID"),
			TriggerType:     envOr("RISK_TRIGGER_TYPE", "manual"),
			RunReason:       os.Getenv("RISK_RUN_REASON"),
			SubjectType:     mustEnv("RISK_SUBJECT_TYPE"),
			SubjectID:       mustEnv("RISK_SUBJECT_ID"),
			Currency:        mustEnv("RISK_CURRENCY"),
			AsOf:            asOf.UTC(),
			FeatureSnapshot: featureSnapshot,
			Evaluation:      evaluation,
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	output := struct {
		DAGRunID           string                       `json:"dag_run_id"`
		FeatureRunID       string                       `json:"feature_run_id"`
		PolicyKey          string                       `json:"policy_key"`
		PolicyVersion      int                          `json:"policy_version"`
		Decision           string                       `json:"decision"`
		ReasonCodes        []string                     `json:"reason_codes"`
		MatchedStrategyKey string                       `json:"matched_strategy_key"`
		MatchedRuleKey     string                       `json:"matched_rule_key"`
		Actions            []risk.PolicyAction          `json:"actions"`
		Factors            map[string]risk.FactorResult `json:"factors"`
		Replay             bool                         `json:"replay"`
	}{
		mustEnv("DAG_RUN_ID"), featureSnapshot.SourceRunID,
		evaluation.PolicyKey, evaluation.PolicyVersion, evaluation.Decision,
		evaluation.ReasonCodes, evaluation.MatchedStrategyKey, evaluation.MatchedRuleKey,
		evaluation.Actions, evaluation.Factors.Results(), replay,
	}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
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
