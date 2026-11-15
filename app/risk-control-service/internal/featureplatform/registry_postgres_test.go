package featureplatform_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/featureplatform"
)

func TestPostgresRegistryPublishesValidatedFeatureSet(t *testing.T) {
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
	publication, err := registry.Publish(ctx, draft.Key, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(publication.ContentFingerprint) != 64 {
		t.Fatalf("content fingerprint length=%d", len(publication.ContentFingerprint))
	}
	replayedPublication, err := registry.Publish(ctx, draft.Key, draft.Version)
	if err != nil {
		t.Fatalf("publication retry must be idempotent: %v", err)
	}
	if replayedPublication.ContentFingerprint != publication.ContentFingerprint ||
		!replayedPublication.PublishedAt.Equal(publication.PublishedAt) {
		t.Fatalf("publication retry changed identity: first=%+v replay=%+v", publication, replayedPublication)
	}

	stored, err := registry.GetFeatureSet(ctx, draft.Key, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "published" || stored.ContentFingerprint != publication.ContentFingerprint {
		t.Fatalf("unexpected publication: %+v", stored)
	}
	if len(stored.Features) != 3 || stored.Features[2].Key != "outflow_ratio" {
		t.Fatalf("unexpected features: %+v", stored.Features)
	}
	if len(stored.Computation.Sources) != 1 || stored.Computation.Sources[0].Key != "dwd_fund_movements" {
		t.Fatalf("unexpected computation sources: %+v", stored.Computation.Sources)
	}
}

func TestPublishedFeatureSetContentIsImmutable(t *testing.T) {
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

	draft.Features[0].Description = "silently changed meaning"
	err := registry.SaveDraft(ctx, draft)
	var invalidState featureplatform.InvalidStateError
	if !errors.As(err, &invalidState) {
		t.Fatalf("SaveDraft error=%v, want InvalidStateError", err)
	}

	_, err = pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s.feature_definitions
		SET description='bypassed repository'
		WHERE feature_set_key='subject_fund_activity'
		  AND feature_set_version=1
		  AND feature_key='inflow_minor'`, schema))
	if err == nil {
		t.Fatal("expected PostgreSQL trigger to reject published definition mutation")
	}
}

func TestPostgresRegistryBindsFeatureFactorDirectlyToPublishedFeature(t *testing.T) {
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

	binding := featureplatform.FeatureFactorBinding{
		ConsumerType:    "risk_policy",
		ConsumerKey:     "withdrawal_risk",
		ConsumerVersion: 7,
		FactorKey:       "inflow_minor_24h",
		FeatureRef: featureplatform.FeatureRef{
			FeatureSetKey: draft.Key, FeatureSetVersion: 1, FeatureKey: "inflow_minor",
		},
	}
	if err := registry.BindFeatureFactor(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err := registry.BindFeatureFactor(ctx, binding); err != nil {
		t.Fatalf("exact binding replay must be idempotent: %v", err)
	}
}

func TestPostgresShadowComparisonRecorderIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool, schema := newRegistryTestPool(t, ctx)
	recorder := featureplatform.NewPostgresShadowComparisonRecorder(pool, schema)
	comparison := featureplatform.FeatureShadowComparison{
		RequestID: "shadow-request-001", FeatureSetKey: "subject_fund_activity",
		FeatureSetVersion: 1,
		Subject: featureplatform.Subject{Type: "organization", Keys: map[string]string{
			"subject_id": "org-001", "currency": "USD",
		}},
		Cutoff:                time.Date(2026, 11, 15, 12, 0, 2, 0, time.UTC),
		CandidateAsOf:         time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC),
		ReferenceAsOf:         time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC),
		CandidateReadDuration: 12 * time.Millisecond,
		ReferenceReadDuration: 28 * time.Millisecond,
		Status:                featureplatform.ShadowComparisonMatched,
		ComparedAt:            time.Date(2026, 11, 15, 12, 0, 1, 0, time.UTC),
	}
	if err := recorder.Record(ctx, []featureplatform.FeatureShadowComparison{comparison}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Record(ctx, []featureplatform.FeatureShadowComparison{comparison}); err != nil {
		t.Fatalf("exact comparison replay must be idempotent: %v", err)
	}
	var rowCount int
	if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s.feature_serving_shadow_comparisons`, schema)).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 1 {
		t.Fatalf("comparison rows=%d, want one", rowCount)
	}
	var cutoff time.Time
	var candidateLatency, referenceLatency int64
	if err := pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT cutoff_at,candidate_latency_millis,reference_latency_millis
		FROM %s.feature_serving_shadow_comparisons`, schema)).
		Scan(&cutoff, &candidateLatency, &referenceLatency); err != nil {
		t.Fatal(err)
	}
	if !cutoff.Equal(comparison.Cutoff) || candidateLatency != 12 || referenceLatency != 28 {
		t.Fatalf("stored serving evidence cutoff=%s candidate=%d reference=%d", cutoff, candidateLatency, referenceLatency)
	}
}

func TestFeatureSetRetirementRequiresNoActiveFeatureFactors(t *testing.T) {
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
	binding := featureplatform.FeatureFactorBinding{
		ConsumerType:    "risk_policy",
		ConsumerKey:     "withdrawal_risk",
		ConsumerVersion: 7,
		FactorKey:       "inflow_minor_24h",
		FeatureRef: featureplatform.FeatureRef{
			FeatureSetKey: draft.Key, FeatureSetVersion: 1, FeatureKey: "inflow_minor",
		},
	}
	if err := registry.BindFeatureFactor(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err := registry.DeprecateFeatureSet(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
	if err := registry.RetireFeatureSet(ctx, draft.Key, draft.Version); err == nil {
		t.Fatal("expected active feature factor binding to prevent retirement")
	}
	if err := registry.RetireFeatureFactorBinding(ctx, binding.ConsumerType, binding.ConsumerKey, binding.ConsumerVersion, binding.FactorKey); err != nil {
		t.Fatal(err)
	}
	if err := registry.RetireFeatureSet(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
	stored, err := registry.GetFeatureSet(ctx, draft.Key, draft.Version)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "retired" {
		t.Fatalf("status=%q, want retired", stored.Status)
	}
}

func TestFeatureSetRetirementRequiresShadowRouteAndNoPendingServingChange(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `INSERT INTO `+schema+`.feature_serving_routes(
		feature_set_key,feature_set_version,serving_mode,generation,changed_by,change_reason)
		VALUES($1,$2,'online',1,'reviewer','promotion approved')`, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
	if err := registry.DeprecateFeatureSet(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
	if err := registry.RetireFeatureSet(ctx, draft.Key, draft.Version); err == nil {
		t.Fatal("expected online serving route to prevent retirement")
	}
	if _, err := pool.Exec(ctx, `UPDATE `+schema+`.feature_serving_routes
		SET serving_mode='shadow',generation=2 WHERE feature_set_key=$1 AND feature_set_version=$2`,
		draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}

	governanceRepository := featureplatform.NewPostgresFeatureServingGovernanceRepository(pool, schema)
	request := featureplatform.FeatureServingChangeRequest{
		RequestID: "retirement-pending-promotion",
		Promotion: featureplatform.PromotionRequest{
			RequestID: "retirement-pending-promotion", FeatureSetKey: draft.Key,
			FeatureSetVersion: draft.Version,
		},
		Status: featureplatform.ChangeRequestPending, RequestedBy: "operator",
		CreatedAt: time.Date(2026, 11, 15, 15, 0, 0, 0, time.UTC),
	}
	if _, err := governanceRepository.CreatePromotionRequest(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := registry.RetireFeatureSet(ctx, draft.Key, draft.Version); err == nil {
		t.Fatal("expected pending serving change to prevent retirement")
	}
	if _, err := governanceRepository.CompleteReview(ctx, request.RequestID, featureplatform.ChangeRequestReview{
		Status: featureplatform.ChangeRequestRejected, ReviewedBy: "reviewer",
		Reason:     "version retirement requested",
		ReviewedAt: time.Date(2026, 11, 15, 15, 5, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RetireFeatureSet(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
}

func TestSaveDraftRejectsOutputSchemaTypeMismatch(t *testing.T) {
	ctx := context.Background()
	pool, schema := newRegistryTestPool(t, ctx)
	registry := featureplatform.NewPostgresRegistry(pool, schema)
	registerFundActivityPrerequisites(t, ctx, registry)
	draft := fundActivityDraft()
	draft.Computation.OutputSchema["inflow_minor"] = "double"

	if err := registry.SaveDraft(ctx, draft); err == nil {
		t.Fatal("expected output schema type mismatch to be rejected")
	}
}

func TestSubjectTypeRegistrationIsIdempotentButRejectsSemanticDrift(t *testing.T) {
	ctx := context.Background()
	pool, schema := newRegistryTestPool(t, ctx)
	registry := featureplatform.NewPostgresRegistry(pool, schema)
	subject := featureplatform.SubjectType{
		Key:           "organization",
		Description:   "organization risk subject",
		JoinKeySchema: map[string]string{"organization_id": "string"},
		Owner:         "risk-platform",
	}
	if err := registry.RegisterSubjectType(ctx, subject); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterSubjectType(ctx, subject); err != nil {
		t.Fatalf("exact subject registration replay must be idempotent: %v", err)
	}
	subject.JoinKeySchema = map[string]string{"legacy_id": "string"}
	err := registry.RegisterSubjectType(ctx, subject)
	var conflict featureplatform.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("registration error=%v, want ConflictError", err)
	}
}

func TestPostgresRegistryResolvesPublishedFeatureForServing(t *testing.T) {
	ctx := context.Background()
	pool, schema := newRegistryTestPool(t, ctx)
	registry := featureplatform.NewPostgresRegistry(pool, schema)
	registerFundActivityPrerequisites(t, ctx, registry)
	draft := fundActivityDraft()
	if err := registry.SaveDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	ref := featureplatform.FeatureRef{
		FeatureSetKey:     draft.Key,
		FeatureSetVersion: draft.Version,
		FeatureKey:        "inflow_minor",
	}
	if _, err := registry.ResolveFeatures(ctx, []featureplatform.FeatureRef{ref}); err == nil {
		t.Fatal("draft feature must not be available to Feature Serving")
	}
	if err := registry.Validate(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Publish(ctx, draft.Key, draft.Version); err != nil {
		t.Fatal(err)
	}
	definitions, err := registry.ResolveFeatures(ctx, []featureplatform.FeatureRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 1 || definitions[0].ValueType != "int" || definitions[0].MaxAge != 5*time.Minute {
		t.Fatalf("unexpected serving definition: %+v", definitions)
	}
}

func registerFundActivityPrerequisites(
	t *testing.T,
	ctx context.Context,
	registry *featureplatform.PostgresRegistry,
) {
	t.Helper()
	err := registry.RegisterSubjectType(ctx, featureplatform.SubjectType{
		Key:           "organization",
		Description:   "organization risk subject",
		JoinKeySchema: map[string]string{"organization_id": "string"},
		Owner:         "risk-platform",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = registry.RegisterSource(ctx, featureplatform.Source{
		Key:                "dwd_fund_movements",
		Version:            1,
		Domain:             "funds",
		Type:               "dwd_table",
		SchemaRef:          "risk_warehouse.dwd_fund_movements_v2",
		SchemaVersion:      2,
		EventTimeField:     "occurred_at",
		SubjectMapping:     map[string]string{"organization_id": "subject_id"},
		DataClassification: "confidential",
		Owner:              "risk-data",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func fundActivityDraft() featureplatform.FeatureSetDraft {
	return featureplatform.FeatureSetDraft{
		Key:         "subject_fund_activity",
		Version:     1,
		SubjectType: "organization",
		Description: "rolling fund activity for one organization",
		Owner:       "risk-data",
		WindowType:  "rolling",
		Features: []featureplatform.FeatureDefinition{
			{
				Key:                      "inflow_minor",
				ValueType:                "int",
				Description:              "total inflow in minor units",
				Unit:                     "minor_currency_unit",
				MissingSemantics:         "missing",
				MaxAgeSeconds:            300,
				SupportedEvaluationModes: []string{"realtime", "nearline", "offline"},
				DataClassification:       "confidential",
			},
			{
				Key:                      "outflow_minor",
				ValueType:                "int",
				Description:              "total outflow in minor units",
				Unit:                     "minor_currency_unit",
				MissingSemantics:         "missing",
				MaxAgeSeconds:            300,
				SupportedEvaluationModes: []string{"realtime", "nearline", "offline"},
				DataClassification:       "confidential",
			},
			{
				Key:                      "outflow_ratio",
				ValueType:                "double",
				Description:              "outflow divided by inflow when inflow is positive",
				Unit:                     "ratio",
				DefinedWhen:              "inflow_minor > 0",
				MissingSemantics:         "undefined",
				MaxAgeSeconds:            300,
				SupportedEvaluationModes: []string{"realtime", "nearline", "offline"},
				DataClassification:       "confidential",
			},
		},
		Computation: featureplatform.Computation{
			Key:            "subject_fund_activity_rolling_v1",
			Mode:           "batch",
			ArtifactRef:    "git://risk-feature-jobs/subject-fund-activity@v1",
			ArtifactDigest: strings.Repeat("a", 64),
			WindowSpec:     map[string]any{"type": "rolling", "duration": "24h"},
			OutputSchema: map[string]string{
				"inflow_minor":  "int",
				"outflow_minor": "int",
				"outflow_ratio": "double",
			},
			TriggerSpec:      map[string]any{"schedule": "0 * * * *"},
			SupportsBackfill: true,
			Sources: []featureplatform.SourceRef{
				{Key: "dwd_fund_movements", Version: 1},
			},
		},
		Quality: featureplatform.QualityContract{
			FreshnessSeconds:      300,
			MinimumCompleteness:   0.99,
			MaximumReconciliation: 0.001,
			RequiredChecks:        []string{"schema", "freshness", "batch_stream_reconciliation"},
		},
	}
}

func newRegistryTestPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, string) {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is required for PostgreSQL integration tests")
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)

	schema := fmt.Sprintf("feature_registry_test_%d", time.Now().UnixNano())
	migrationPath := filepath.Join("..", "..", "migrations", "postgres", "003_feature_registry.sql")
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	testMigration := strings.ReplaceAll(string(migration), "risk_control", schema)
	if _, err := admin.Exec(ctx, testMigration); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, testMigration); err != nil {
		t.Fatalf("feature registry migration must be idempotent: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })
	return admin, schema
}
