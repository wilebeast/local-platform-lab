package featureplatform_test

import (
	"context"
	"testing"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/featureplatform"
)

func TestKindS6GovernanceAlertRollbackAndRetirementLifecycle(t *testing.T) {
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

	recorder := featureplatform.NewPostgresShadowComparisonRecorder(pool, schema)
	promotionStart := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	for index := 0; index < 3; index++ {
		recordS6Comparison(t, ctx, recorder, draft, "promotion", index,
			promotionStart.Add(time.Duration(index)*time.Hour+time.Minute),
			featureplatform.ShadowComparisonMatched)
	}
	promotionRepository := featureplatform.NewPostgresPromotionRepository(pool, schema)
	governanceRepository := featureplatform.NewPostgresFeatureServingGovernanceRepository(pool, schema)
	governance := featureplatform.NewFeatureServingGovernance(
		governanceRepository,
		featureplatform.NewPromotionController(promotionRepository, promotionRepository),
	)
	promotionRequest := featureplatform.PromotionRequest{
		RequestID: "s6-kind-promotion", FeatureSetKey: draft.Key,
		FeatureSetVersion: draft.Version, EvidenceStart: promotionStart,
		EvidenceEnd: promotionStart.Add(3 * time.Hour), BucketDuration: time.Hour,
		Criteria: featureplatform.PromotionCriteria{
			MinimumComparisons: 3, MinimumDistinctBuckets: 3,
			MaximumMismatchRate: 0, MaximumUnavailableRate: 0,
			MaximumCandidateFreshnessLag:    time.Minute,
			MaximumP95CandidateReadDuration: 20 * time.Millisecond,
		},
		Reason: "kind S6 promotion request",
	}
	created, err := governance.RequestPromotion(ctx, featureplatform.GovernancePrincipal{
		Subject: "operator", Roles: []featureplatform.GovernanceRole{featureplatform.GovernanceRoleOperator},
	}, promotionRequest)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := governance.ApprovePromotion(ctx, featureplatform.GovernancePrincipal{
		Subject: "reviewer", Roles: []featureplatform.GovernanceRole{featureplatform.GovernanceRoleReviewer},
	}, created.RequestID, "independent Kind review passed")
	if err != nil {
		t.Fatal(err)
	}
	if approved.Promotion.Route.Mode != featureplatform.ServingModeOnline {
		t.Fatalf("approved route=%+v", approved.Promotion.Route)
	}

	healthStart := time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
	recordS6Comparison(t, ctx, recorder, draft, "health", 0,
		healthStart.Add(time.Minute), featureplatform.ShadowComparisonMatched)
	recordS6Comparison(t, ctx, recorder, draft, "health", 1,
		healthStart.Add(6*time.Minute), featureplatform.ShadowComparisonMismatch)
	alertStore := featureplatform.NewPostgresFeatureServingHealthAlertStore(pool, schema)
	monitor := featureplatform.NewOnlineFeatureServingHealthMonitor(
		promotionRepository, promotionRepository, alertStore,
	)
	health, err := monitor.Evaluate(ctx, featureplatform.OnlineHealthCheckRequest{
		CheckID: "s6-kind-health", FeatureSetKey: draft.Key,
		FeatureSetVersion: draft.Version, EvidenceStart: healthStart,
		EvidenceEnd: healthStart.Add(10 * time.Minute), BucketDuration: 5 * time.Minute,
		Criteria: featureplatform.PromotionCriteria{
			MinimumComparisons: 2, MinimumDistinctBuckets: 2,
			MaximumMismatchRate: 0.01, MaximumUnavailableRate: 0,
			MaximumCandidateFreshnessLag:    time.Minute,
			MaximumP95CandidateReadDuration: 20 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if health.Healthy || health.Alert == nil ||
		health.Alert.RecommendedAction != featureplatform.HealthRecommendationRollback {
		t.Fatalf("health result=%+v", health)
	}

	route, err := governance.EmergencyRollback(ctx, featureplatform.GovernancePrincipal{
		Subject: "incident-commander",
		Roles:   []featureplatform.GovernanceRole{featureplatform.GovernanceRoleEmergencyOperator},
	}, featureplatform.ServingRollbackRequest{
		RequestID: "s6-kind-rollback", FeatureSetKey: draft.Key,
		FeatureSetVersion: draft.Version, Reason: "mismatch alert confirmed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if route.Mode != featureplatform.ServingModeShadow {
		t.Fatalf("rollback route=%+v", route)
	}
	if err := registry.DeprecateFeatureSet(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
	if err := registry.RetireFeatureSet(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
	retired, err := registry.GetFeatureSet(ctx, draft.Key, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if retired.Status != "retired" {
		t.Fatalf("feature status=%q, want retired", retired.Status)
	}

	var requests, alerts, routeEvents int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+schema+".feature_serving_change_requests").Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+schema+".feature_serving_health_alerts WHERE alert_status='open'").Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+schema+".feature_serving_route_events").Scan(&routeEvents); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || alerts != 1 || routeEvents != 2 {
		t.Fatalf("requests=%d alerts=%d route events=%d", requests, alerts, routeEvents)
	}
}

func recordS6Comparison(
	t *testing.T,
	ctx context.Context,
	recorder *featureplatform.PostgresShadowComparisonRecorder,
	draft featureplatform.FeatureSetDraft,
	prefix string,
	index int,
	comparedAt time.Time,
	status featureplatform.ShadowComparisonStatus,
) {
	t.Helper()
	if err := recorder.Record(ctx, []featureplatform.FeatureShadowComparison{{
		RequestID:     prefix + "-comparison-" + string(rune('a'+index)),
		FeatureSetKey: draft.Key, FeatureSetVersion: draft.Version,
		Subject: featureplatform.Subject{Type: "organization", Keys: map[string]string{
			"subject_id": "org-s6-kind", "currency": "USD",
		}},
		Cutoff: comparedAt, CandidateAsOf: comparedAt.Add(-10 * time.Second),
		ReferenceAsOf:         comparedAt.Add(-10 * time.Second),
		CandidateReadDuration: 5 * time.Millisecond,
		ReferenceReadDuration: 20 * time.Millisecond,
		Status:                status, Differences: s6Differences(status), ComparedAt: comparedAt,
	}}); err != nil {
		t.Fatal(err)
	}
}

func s6Differences(status featureplatform.ShadowComparisonStatus) []string {
	if status == featureplatform.ShadowComparisonMismatch {
		return []string{"value:outflow_ratio"}
	}
	return nil
}
