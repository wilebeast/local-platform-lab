package featureplatform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var schemaNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

type PostgresRegistry struct {
	pool   *pgxpool.Pool
	schema string
}

func NewPostgresRegistry(pool *pgxpool.Pool, schema string) *PostgresRegistry {
	return &PostgresRegistry{pool: pool, schema: schema}
}

func (r *PostgresRegistry) RegisterSubjectType(ctx context.Context, subject SubjectType) error {
	if err := r.ready(); err != nil {
		return err
	}
	if subject.Key == "" || subject.Description == "" || len(subject.JoinKeySchema) == 0 || subject.Owner == "" {
		return fmt.Errorf("subject type key, description, join key schema and owner are required")
	}
	joinKeySchema, err := json.Marshal(subject.JoinKeySchema)
	if err != nil {
		return fmt.Errorf("encode join key schema: %w", err)
	}
	contentFingerprint, err := fingerprint(subject)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s(
			subject_type_key, description, join_key_schema, owner_key, content_fingerprint)
		VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(subject_type_key) DO NOTHING`, r.table("feature_subject_types")),
		subject.Key, subject.Description, joinKeySchema, subject.Owner, contentFingerprint)
	if err != nil {
		return err
	}
	var storedFingerprint string
	if err := r.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT content_fingerprint FROM %s WHERE subject_type_key=$1`,
		r.table("feature_subject_types")), subject.Key).Scan(&storedFingerprint); err != nil {
		return err
	}
	if storedFingerprint != contentFingerprint {
		return ConflictError{Message: "feature subject type belongs to different content"}
	}
	return nil
}

func (r *PostgresRegistry) RegisterSource(ctx context.Context, source Source) error {
	if err := r.ready(); err != nil {
		return err
	}
	if source.Key == "" || source.Version <= 0 || source.Domain == "" || source.Type == "" ||
		source.SchemaRef == "" || source.SchemaVersion <= 0 || source.EventTimeField == "" ||
		len(source.SubjectMapping) == 0 || source.DataClassification == "" || source.Owner == "" {
		return fmt.Errorf("complete versioned feature source metadata is required")
	}
	fingerprint, err := fingerprint(source)
	if err != nil {
		return err
	}
	subjectMapping, err := json.Marshal(source.SubjectMapping)
	if err != nil {
		return fmt.Errorf("encode source subject mapping: %w", err)
	}
	_, err = r.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s(
			source_key, source_version, source_domain, source_type, schema_ref,
			schema_version, event_time_field, subject_mapping, data_classification,
			owner_key, content_fingerprint)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT(source_key, source_version) DO NOTHING`, r.table("feature_sources")),
		source.Key, source.Version, source.Domain, source.Type, source.SchemaRef,
		source.SchemaVersion, source.EventTimeField, subjectMapping, source.DataClassification,
		source.Owner, fingerprint)
	if err != nil {
		return fmt.Errorf("register feature source: %w", err)
	}
	var storedFingerprint string
	if err := r.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT content_fingerprint FROM %s
		WHERE source_key=$1 AND source_version=$2`, r.table("feature_sources")),
		source.Key, source.Version).Scan(&storedFingerprint); err != nil {
		return fmt.Errorf("read feature source fingerprint: %w", err)
	}
	if storedFingerprint != fingerprint {
		return ConflictError{Message: "feature source version belongs to different content"}
	}
	return nil
}

func (r *PostgresRegistry) SaveDraft(ctx context.Context, draft FeatureSetDraft) error {
	if err := r.ready(); err != nil {
		return err
	}
	if err := validateDraftShape(draft); err != nil {
		return err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, $2))`, draft.Key, draft.Version); err != nil {
		return err
	}
	var status string
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT status FROM %s
		WHERE feature_set_key=$1 AND feature_set_version=$2
		FOR UPDATE`, r.table("feature_set_versions")), draft.Key, draft.Version).Scan(&status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		_, err = tx.Exec(ctx, fmt.Sprintf(`
			INSERT INTO %s(
				feature_set_key, feature_set_version, subject_type_key,
				description, owner_key, window_type, status)
			VALUES($1,$2,$3,$4,$5,$6,'draft')`, r.table("feature_set_versions")),
			draft.Key, draft.Version, draft.SubjectType, draft.Description, draft.Owner, draft.WindowType)
	case err != nil:
		return err
	case status == "draft" || status == "validated":
		_, err = tx.Exec(ctx, fmt.Sprintf(`
			UPDATE %s SET
				subject_type_key=$3, description=$4, owner_key=$5, window_type=$6,
				status='draft', content_fingerprint='', validated_at=NULL, updated_at=NOW()
			WHERE feature_set_key=$1 AND feature_set_version=$2`, r.table("feature_set_versions")),
			draft.Key, draft.Version, draft.SubjectType, draft.Description, draft.Owner, draft.WindowType)
	default:
		return InvalidStateError{Message: "only draft or validated feature set versions can be edited"}
	}
	if err != nil {
		return fmt.Errorf("save feature set version: %w", err)
	}

	for _, table := range []string{"feature_computation_sources", "feature_definitions", "feature_quality_contracts", "feature_computations"} {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE feature_set_key=$1 AND feature_set_version=$2`, r.table(table)), draft.Key, draft.Version); err != nil {
			return fmt.Errorf("replace %s: %w", table, err)
		}
	}
	for index, feature := range draft.Features {
		modes, _ := json.Marshal(feature.SupportedEvaluationModes)
		if _, err := tx.Exec(ctx, fmt.Sprintf(`
			INSERT INTO %s(
				feature_set_key, feature_set_version, feature_key, ordinal, value_type,
				description, unit, nullable, defined_when, missing_semantics,
				max_age_seconds, supported_evaluation_modes, data_classification)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, r.table("feature_definitions")),
			draft.Key, draft.Version, feature.Key, index+1, feature.ValueType,
			feature.Description, feature.Unit, feature.Nullable, feature.DefinedWhen,
			feature.MissingSemantics, feature.MaxAgeSeconds, modes, feature.DataClassification); err != nil {
			return fmt.Errorf("save feature definition %q: %w", feature.Key, err)
		}
	}
	windowSpec, _ := json.Marshal(draft.Computation.WindowSpec)
	outputSchema, _ := json.Marshal(draft.Computation.OutputSchema)
	triggerSpec, _ := json.Marshal(draft.Computation.TriggerSpec)
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s(
			feature_set_key, feature_set_version, computation_key, computation_mode,
			artifact_ref, artifact_digest, window_spec, output_schema, trigger_spec,
			supports_backfill)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, r.table("feature_computations")),
		draft.Key, draft.Version, draft.Computation.Key, draft.Computation.Mode,
		draft.Computation.ArtifactRef, draft.Computation.ArtifactDigest, windowSpec,
		outputSchema, triggerSpec, draft.Computation.SupportsBackfill); err != nil {
		return fmt.Errorf("save feature computation: %w", err)
	}
	for index, source := range draft.Computation.Sources {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`
			INSERT INTO %s(
				feature_set_key, feature_set_version, source_key, source_version, ordinal)
			VALUES($1,$2,$3,$4,$5)`, r.table("feature_computation_sources")),
			draft.Key, draft.Version, source.Key, source.Version, index+1); err != nil {
			return fmt.Errorf("save feature computation source: %w", err)
		}
	}
	requiredChecks, _ := json.Marshal(draft.Quality.RequiredChecks)
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s(
			feature_set_key, feature_set_version, freshness_seconds,
			minimum_completeness, maximum_reconciliation, required_checks)
		VALUES($1,$2,$3,$4,$5,$6)`, r.table("feature_quality_contracts")),
		draft.Key, draft.Version, draft.Quality.FreshnessSeconds,
		draft.Quality.MinimumCompleteness, draft.Quality.MaximumReconciliation,
		requiredChecks); err != nil {
		return fmt.Errorf("save feature quality contract: %w", err)
	}
	return tx.Commit(ctx)
}

func (r *PostgresRegistry) Validate(ctx context.Context, key string, version int) error {
	if err := r.ready(); err != nil {
		return err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	var featureCount, computationCount, sourceCount, qualityCount int
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT v.status,
			(SELECT count(*) FROM %s d WHERE d.feature_set_key=v.feature_set_key AND d.feature_set_version=v.feature_set_version),
			(SELECT count(*) FROM %s c WHERE c.feature_set_key=v.feature_set_key AND c.feature_set_version=v.feature_set_version),
			(SELECT count(*) FROM %s s WHERE s.feature_set_key=v.feature_set_key AND s.feature_set_version=v.feature_set_version),
			(SELECT count(*) FROM %s q WHERE q.feature_set_key=v.feature_set_key AND q.feature_set_version=v.feature_set_version)
		FROM %s v
		WHERE v.feature_set_key=$1 AND v.feature_set_version=$2
		FOR UPDATE`, r.table("feature_definitions"), r.table("feature_computations"),
		r.table("feature_computation_sources"), r.table("feature_quality_contracts"),
		r.table("feature_set_versions")), key, version).
		Scan(&status, &featureCount, &computationCount, &sourceCount, &qualityCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotFoundError{Message: "feature set version not found"}
	}
	if err != nil {
		return err
	}
	if status != "draft" {
		return InvalidStateError{Message: "only a draft feature set version can be validated"}
	}
	if featureCount == 0 || computationCount != 1 || sourceCount == 0 || qualityCount != 1 {
		return fmt.Errorf("feature set validation requires definitions, one computation, sources and one quality contract")
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`
		UPDATE %s SET status='validated', validated_at=NOW(), updated_at=NOW()
		WHERE feature_set_key=$1 AND feature_set_version=$2`, r.table("feature_set_versions")), key, version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PostgresRegistry) Publish(ctx context.Context, key string, version int) (Publication, error) {
	if err := r.ready(); err != nil {
		return Publication{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Publication{}, err
	}
	defer tx.Rollback(ctx)
	var status string
	err = tx.QueryRow(ctx, fmt.Sprintf(`
		SELECT status FROM %s
		WHERE feature_set_key=$1 AND feature_set_version=$2
		FOR UPDATE`, r.table("feature_set_versions")), key, version).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Publication{}, NotFoundError{Message: "feature set version not found"}
	}
	if err != nil {
		return Publication{}, err
	}
	if status == "published" {
		stored, err := r.loadFeatureSet(ctx, tx, key, version)
		if err != nil {
			return Publication{}, err
		}
		if stored.PublishedAt == nil || len(stored.ContentFingerprint) != 64 {
			return Publication{}, fmt.Errorf("published feature set version has incomplete publication metadata")
		}
		if err := tx.Commit(ctx); err != nil {
			return Publication{}, err
		}
		return Publication{
			FeatureSetKey:      key,
			FeatureSetVersion:  version,
			ContentFingerprint: stored.ContentFingerprint,
			PublishedAt:        *stored.PublishedAt,
		}, nil
	}
	if status != "validated" {
		return Publication{}, InvalidStateError{Message: "only a validated feature set version can be published"}
	}
	stored, err := r.loadFeatureSet(ctx, tx, key, version)
	if err != nil {
		return Publication{}, err
	}
	contentFingerprint, err := fingerprint(stored.FeatureSetDraft)
	if err != nil {
		return Publication{}, err
	}
	publishedAt := time.Now().UTC()
	commandTag, err := tx.Exec(ctx, fmt.Sprintf(`
		UPDATE %s SET
			status='published', content_fingerprint=$3, published_at=$4, updated_at=NOW()
		WHERE feature_set_key=$1 AND feature_set_version=$2 AND status='validated'`, r.table("feature_set_versions")),
		key, version, contentFingerprint, publishedAt)
	if err != nil {
		return Publication{}, err
	}
	if commandTag.RowsAffected() != 1 {
		return Publication{}, ConflictError{Message: "feature set version changed during publication"}
	}
	if err := tx.Commit(ctx); err != nil {
		return Publication{}, err
	}
	return Publication{
		FeatureSetKey:      key,
		FeatureSetVersion:  version,
		ContentFingerprint: contentFingerprint,
		PublishedAt:        publishedAt,
	}, nil
}

func (r *PostgresRegistry) GetFeatureSet(ctx context.Context, key string, version int) (FeatureSetVersion, error) {
	if err := r.ready(); err != nil {
		return FeatureSetVersion{}, err
	}
	return r.loadFeatureSet(ctx, r.pool, key, version)
}

type registryQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (r *PostgresRegistry) loadFeatureSet(ctx context.Context, q registryQuerier, key string, version int) (FeatureSetVersion, error) {
	var stored FeatureSetVersion
	stored.Key = key
	stored.Version = version
	if err := q.QueryRow(ctx, fmt.Sprintf(`
		SELECT subject_type_key, description, owner_key, window_type, status,
			content_fingerprint, validated_at, published_at
		FROM %s WHERE feature_set_key=$1 AND feature_set_version=$2`, r.table("feature_set_versions")), key, version).
		Scan(&stored.SubjectType, &stored.Description, &stored.Owner, &stored.WindowType,
			&stored.Status, &stored.ContentFingerprint, &stored.ValidatedAt, &stored.PublishedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FeatureSetVersion{}, NotFoundError{Message: "feature set version not found"}
		}
		return FeatureSetVersion{}, err
	}
	rows, err := q.Query(ctx, fmt.Sprintf(`
		SELECT feature_key, value_type, description, unit, nullable, defined_when,
			missing_semantics, max_age_seconds, supported_evaluation_modes, data_classification
		FROM %s WHERE feature_set_key=$1 AND feature_set_version=$2
		ORDER BY ordinal`, r.table("feature_definitions")), key, version)
	if err != nil {
		return FeatureSetVersion{}, err
	}
	for rows.Next() {
		var feature FeatureDefinition
		var modes []byte
		if err := rows.Scan(&feature.Key, &feature.ValueType, &feature.Description,
			&feature.Unit, &feature.Nullable, &feature.DefinedWhen, &feature.MissingSemantics,
			&feature.MaxAgeSeconds, &modes, &feature.DataClassification); err != nil {
			rows.Close()
			return FeatureSetVersion{}, err
		}
		if err := json.Unmarshal(modes, &feature.SupportedEvaluationModes); err != nil {
			rows.Close()
			return FeatureSetVersion{}, err
		}
		stored.Features = append(stored.Features, feature)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return FeatureSetVersion{}, err
	}
	rows.Close()

	var windowSpec, outputSchema, triggerSpec []byte
	err = q.QueryRow(ctx, fmt.Sprintf(`
		SELECT computation_key, computation_mode, artifact_ref, artifact_digest,
			window_spec, output_schema, trigger_spec, supports_backfill
		FROM %s WHERE feature_set_key=$1 AND feature_set_version=$2`, r.table("feature_computations")), key, version).
		Scan(&stored.Computation.Key, &stored.Computation.Mode, &stored.Computation.ArtifactRef,
			&stored.Computation.ArtifactDigest, &windowSpec, &outputSchema, &triggerSpec,
			&stored.Computation.SupportsBackfill)
	if err != nil {
		return FeatureSetVersion{}, err
	}
	if err := json.Unmarshal(windowSpec, &stored.Computation.WindowSpec); err != nil {
		return FeatureSetVersion{}, err
	}
	if err := json.Unmarshal(outputSchema, &stored.Computation.OutputSchema); err != nil {
		return FeatureSetVersion{}, err
	}
	if err := json.Unmarshal(triggerSpec, &stored.Computation.TriggerSpec); err != nil {
		return FeatureSetVersion{}, err
	}
	rows, err = q.Query(ctx, fmt.Sprintf(`
		SELECT source_key, source_version FROM %s
		WHERE feature_set_key=$1 AND feature_set_version=$2 ORDER BY ordinal`, r.table("feature_computation_sources")), key, version)
	if err != nil {
		return FeatureSetVersion{}, err
	}
	for rows.Next() {
		var source SourceRef
		if err := rows.Scan(&source.Key, &source.Version); err != nil {
			rows.Close()
			return FeatureSetVersion{}, err
		}
		stored.Computation.Sources = append(stored.Computation.Sources, source)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return FeatureSetVersion{}, err
	}
	rows.Close()
	var requiredChecks []byte
	if err := q.QueryRow(ctx, fmt.Sprintf(`
		SELECT freshness_seconds, minimum_completeness, maximum_reconciliation, required_checks
		FROM %s WHERE feature_set_key=$1 AND feature_set_version=$2`, r.table("feature_quality_contracts")), key, version).
		Scan(&stored.Quality.FreshnessSeconds, &stored.Quality.MinimumCompleteness,
			&stored.Quality.MaximumReconciliation, &requiredChecks); err != nil {
		return FeatureSetVersion{}, err
	}
	if err := json.Unmarshal(requiredChecks, &stored.Quality.RequiredChecks); err != nil {
		return FeatureSetVersion{}, err
	}
	return stored, nil
}

func (r *PostgresRegistry) ready() error {
	if r == nil || r.pool == nil {
		return fmt.Errorf("PostgreSQL pool is required")
	}
	if !schemaNamePattern.MatchString(r.schema) {
		return fmt.Errorf("safe PostgreSQL schema name is required")
	}
	return nil
}

func (r *PostgresRegistry) table(name string) string {
	return pgx.Identifier{r.schema, name}.Sanitize()
}

func fingerprint(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode fingerprint content: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func validateDraftShape(draft FeatureSetDraft) error {
	if draft.Key == "" || draft.Version <= 0 || draft.SubjectType == "" || draft.Description == "" || draft.Owner == "" || draft.WindowType == "" {
		return fmt.Errorf("feature set key, positive version, subject type, description, owner and window type are required")
	}
	if len(draft.Features) == 0 {
		return fmt.Errorf("at least one feature definition is required")
	}
	seenFeatures := make(map[string]struct{}, len(draft.Features))
	for _, feature := range draft.Features {
		if feature.Key == "" || feature.ValueType == "" || feature.Description == "" ||
			feature.MissingSemantics == "" || feature.MaxAgeSeconds <= 0 ||
			len(feature.SupportedEvaluationModes) == 0 || feature.DataClassification == "" {
			return fmt.Errorf("complete feature definition metadata is required")
		}
		if _, exists := seenFeatures[feature.Key]; exists {
			return fmt.Errorf("duplicate feature key %q", feature.Key)
		}
		seenFeatures[feature.Key] = struct{}{}
	}
	if draft.Computation.Key == "" || draft.Computation.Mode == "" ||
		draft.Computation.ArtifactRef == "" || len(draft.Computation.ArtifactDigest) != 64 ||
		len(draft.Computation.WindowSpec) == 0 || len(draft.Computation.OutputSchema) == 0 ||
		len(draft.Computation.Sources) == 0 {
		return fmt.Errorf("complete feature computation metadata and sources are required")
	}
	for _, feature := range draft.Features {
		outputType, exists := draft.Computation.OutputSchema[feature.Key]
		if !exists {
			return fmt.Errorf("output schema is missing feature %q", feature.Key)
		}
		if outputType != feature.ValueType {
			return fmt.Errorf("output schema type %q does not match feature %q type %q", outputType, feature.Key, feature.ValueType)
		}
	}
	if draft.Quality.FreshnessSeconds <= 0 || draft.Quality.MinimumCompleteness < 0 ||
		draft.Quality.MinimumCompleteness > 1 || draft.Quality.MaximumReconciliation < 0 ||
		draft.Quality.MaximumReconciliation > 1 || len(draft.Quality.RequiredChecks) == 0 {
		return fmt.Errorf("valid feature quality contract is required")
	}
	return nil
}
