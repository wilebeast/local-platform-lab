package featureplatform_test

import (
	"context"
	"testing"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/featureplatform"
)

func TestPostgresHealthAlertStoreIsIdempotentAndResolvesRecovery(t *testing.T) {
	ctx := context.Background()
	pool, schema := newRegistryTestPool(t, ctx)
	registry := featureplatform.NewPostgresRegistry(pool, schema)
	registerFundActivityPrerequisites(t, ctx, registry)
	draft := fundActivityDraft()
	if err := registry.SaveDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Publish(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}

	store := featureplatform.NewPostgresFeatureServingHealthAlertStore(pool, schema)
	createdAt := time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
	alert := featureplatform.FeatureServingHealthAlert{
		AlertID: "0fc0bd80c2f96b26bc9111eb8d8808268a6b17487ac1fb34448a62b21975d817",
		CheckID: "s6-health-check", FeatureSetKey: draft.Key,
		FeatureSetVersion: draft.Version, RouteGeneration: 3,
		Status:      featureplatform.HealthAlertOpen,
		ReasonCodes: []string{"mismatch_rate_exceeded"},
		Evidence: featureplatform.PromotionEvidence{
			TotalComparisons: 100, DistinctBuckets: 10,
			MatchedComparisons: 98, MismatchComparisons: 2,
		},
		Criteria: featureplatform.PromotionCriteria{
			MinimumComparisons: 100, MinimumDistinctBuckets: 10,
			MaximumMismatchRate: 0.01, MaximumUnavailableRate: 0,
			MaximumCandidateFreshnessLag:    time.Minute,
			MaximumP95CandidateReadDuration: 20 * time.Millisecond,
		},
		RecommendedAction: featureplatform.HealthRecommendationRollback,
		CreatedAt:         createdAt,
	}
	first, err := store.RecordHealthAlert(ctx, alert)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.RecordHealthAlert(ctx, alert)
	if err != nil {
		t.Fatal(err)
	}
	if first.AlertID != second.AlertID {
		t.Fatalf("replay created different alert: first=%+v second=%+v", first, second)
	}
	resolved, err := store.ResolveOpenHealthAlerts(ctx, draft.Key, draft.Version, 3, createdAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != 1 {
		t.Fatalf("resolved alerts=%d, want 1", resolved)
	}
	var rows int
	var status string
	if err := pool.QueryRow(ctx, "SELECT count(*),min(alert_status) FROM "+schema+".feature_serving_health_alerts").Scan(&rows, &status); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || status != "resolved" {
		t.Fatalf("alert rows=%d status=%q", rows, status)
	}
}
