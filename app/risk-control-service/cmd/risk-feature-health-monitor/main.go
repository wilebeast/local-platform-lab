package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/featureplatform"
)

type healthMonitorConfig struct {
	postgresDSN string
	schema      string
	request     featureplatform.OnlineHealthCheckRequest
}

func main() {
	config, err := loadHealthMonitorConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, config.postgresDSN)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatal(err)
	}
	promotion := featureplatform.NewPostgresPromotionRepository(pool, config.schema)
	alerts := featureplatform.NewPostgresFeatureServingHealthAlertStore(pool, config.schema)
	monitor := featureplatform.NewOnlineFeatureServingHealthMonitor(promotion, promotion, alerts)
	result, err := monitor.Evaluate(ctx, config.request)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		log.Fatal(err)
	}
}

func loadHealthMonitorConfig(getenv func(string) string) (healthMonitorConfig, error) {
	required := func(key string) (string, error) {
		value := getenv(key)
		if value == "" {
			return "", fmt.Errorf("%s is required", key)
		}
		return value, nil
	}
	postgresDSN, err := required("POSTGRES_DSN")
	if err != nil {
		return healthMonitorConfig{}, err
	}
	featureSetKey, err := required("FEATURE_SET_KEY")
	if err != nil {
		return healthMonitorConfig{}, err
	}
	versionText, err := required("FEATURE_SET_VERSION")
	if err != nil {
		return healthMonitorConfig{}, err
	}
	version, err := strconv.Atoi(versionText)
	if err != nil || version <= 0 {
		return healthMonitorConfig{}, fmt.Errorf("FEATURE_SET_VERSION must be a positive integer")
	}
	asOfText, err := required("FEATURE_HEALTH_CHECK_AS_OF")
	if err != nil {
		return healthMonitorConfig{}, err
	}
	asOf, err := time.Parse(time.RFC3339, asOfText)
	if err != nil {
		return healthMonitorConfig{}, fmt.Errorf("FEATURE_HEALTH_CHECK_AS_OF must use RFC3339: %w", err)
	}
	evidenceWindow, err := requiredDuration(getenv, "FEATURE_HEALTH_EVIDENCE_WINDOW")
	if err != nil {
		return healthMonitorConfig{}, err
	}
	bucketDuration, err := requiredDuration(getenv, "FEATURE_HEALTH_BUCKET_DURATION")
	if err != nil {
		return healthMonitorConfig{}, err
	}
	minimumComparisons, err := requiredInt64(getenv, "FEATURE_HEALTH_MIN_COMPARISONS")
	if err != nil {
		return healthMonitorConfig{}, err
	}
	minimumBuckets, err := requiredInt64(getenv, "FEATURE_HEALTH_MIN_BUCKETS")
	if err != nil {
		return healthMonitorConfig{}, err
	}
	maximumMismatchRate, err := requiredFloat(getenv, "FEATURE_HEALTH_MAX_MISMATCH_RATE")
	if err != nil {
		return healthMonitorConfig{}, err
	}
	maximumUnavailableRate, err := requiredFloat(getenv, "FEATURE_HEALTH_MAX_UNAVAILABLE_RATE")
	if err != nil {
		return healthMonitorConfig{}, err
	}
	maximumFreshnessLag, err := requiredDuration(getenv, "FEATURE_HEALTH_MAX_FRESHNESS_LAG")
	if err != nil {
		return healthMonitorConfig{}, err
	}
	maximumP95Duration, err := requiredDuration(getenv, "FEATURE_HEALTH_MAX_P95_CANDIDATE_DURATION")
	if err != nil {
		return healthMonitorConfig{}, err
	}
	schema := getenv("FEATURE_REGISTRY_SCHEMA")
	if schema == "" {
		schema = "risk_control"
	}
	asOf = asOf.UTC()
	return healthMonitorConfig{
		postgresDSN: postgresDSN,
		schema:      schema,
		request: featureplatform.OnlineHealthCheckRequest{
			CheckID:       fmt.Sprintf("%s-v%d-%s", featureSetKey, version, asOf.Format("20060102T150405Z")),
			FeatureSetKey: featureSetKey, FeatureSetVersion: version,
			EvidenceStart: asOf.Add(-evidenceWindow), EvidenceEnd: asOf,
			BucketDuration: bucketDuration,
			Criteria: featureplatform.PromotionCriteria{
				MinimumComparisons: minimumComparisons, MinimumDistinctBuckets: minimumBuckets,
				MaximumMismatchRate: maximumMismatchRate, MaximumUnavailableRate: maximumUnavailableRate,
				MaximumCandidateFreshnessLag:    maximumFreshnessLag,
				MaximumP95CandidateReadDuration: maximumP95Duration,
			},
		},
	}, nil
}

func requiredDuration(getenv func(string) string, key string) (time.Duration, error) {
	value := getenv(key)
	if value == "" {
		return 0, fmt.Errorf("%s is required", key)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return parsed, nil
}

func requiredInt64(getenv func(string) string, key string) (int64, error) {
	value := getenv(key)
	parsed, err := strconv.ParseInt(value, 10, 64)
	if value == "" || err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}

func requiredFloat(getenv func(string) string, key string) (float64, error) {
	value := getenv(key)
	parsed, err := strconv.ParseFloat(value, 64)
	if value == "" || err != nil || parsed < 0 || parsed > 1 {
		return 0, fmt.Errorf("%s must be between zero and one", key)
	}
	return parsed, nil
}
