package featureplatform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresShadowComparisonRecorder struct {
	pool   *pgxpool.Pool
	schema string
}

func NewPostgresShadowComparisonRecorder(pool *pgxpool.Pool, schema string) *PostgresShadowComparisonRecorder {
	return &PostgresShadowComparisonRecorder{pool: pool, schema: schema}
}

func (r *PostgresShadowComparisonRecorder) Record(
	ctx context.Context,
	comparisons []FeatureShadowComparison,
) error {
	if r == nil || r.pool == nil || !schemaNamePattern.MatchString(r.schema) {
		return fmt.Errorf("PostgreSQL pool and safe schema are required")
	}
	for _, comparison := range comparisons {
		if err := r.recordOne(ctx, comparison); err != nil {
			return err
		}
	}
	return nil
}

func (r *PostgresShadowComparisonRecorder) recordOne(
	ctx context.Context,
	comparison FeatureShadowComparison,
) error {
	if comparison.RequestID == "" || comparison.FeatureSetKey == "" || comparison.FeatureSetVersion <= 0 ||
		comparison.Subject.Type == "" || len(comparison.Subject.Keys) == 0 || comparison.Status == "" || comparison.ComparedAt.IsZero() {
		return fmt.Errorf("complete shadow comparison identity and result are required")
	}
	differences := append([]string(nil), comparison.Differences...)
	sort.Strings(differences)
	subjectKeys, err := json.Marshal(comparison.Subject.Keys)
	if err != nil {
		return err
	}
	comparisonID := shadowComparisonID(comparison)
	contentFingerprint, err := fingerprint(struct {
		Cutoff        time.Time
		CandidateAsOf time.Time
		ReferenceAsOf time.Time
		Status        ShadowComparisonStatus
		Differences   []string
	}{
		Cutoff: comparison.Cutoff, CandidateAsOf: comparison.CandidateAsOf,
		ReferenceAsOf: comparison.ReferenceAsOf,
		Status:        comparison.Status, Differences: differences,
	})
	if err != nil {
		return err
	}
	candidateAsOf := comparison.CandidateAsOf
	if candidateAsOf.IsZero() {
		candidateAsOf = time.Unix(0, 0).UTC()
	}
	referenceAsOf := comparison.ReferenceAsOf
	if referenceAsOf.IsZero() {
		referenceAsOf = time.Unix(0, 0).UTC()
	}
	cutoff := comparison.Cutoff
	if cutoff.IsZero() {
		cutoff = time.Unix(0, 0).UTC()
	}
	differencesJSON, err := json.Marshal(differences)
	if err != nil {
		return err
	}
	table := pgx.Identifier{r.schema, "feature_serving_shadow_comparisons"}.Sanitize()
	_, err = r.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s(
			comparison_id,request_id,feature_set_key,feature_set_version,
			subject_type,subject_keys,cutoff_at,candidate_as_of,reference_as_of,
			candidate_latency_millis,reference_latency_millis,
			comparison_status,differences,content_fingerprint,compared_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT(comparison_id) DO NOTHING`, table),
		comparisonID, comparison.RequestID, comparison.FeatureSetKey, comparison.FeatureSetVersion,
		comparison.Subject.Type, subjectKeys, cutoff, candidateAsOf, referenceAsOf,
		comparison.CandidateReadDuration.Milliseconds(), comparison.ReferenceReadDuration.Milliseconds(),
		comparison.Status, differencesJSON, contentFingerprint, comparison.ComparedAt)
	if err != nil {
		return err
	}
	var storedFingerprint string
	if err := r.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT content_fingerprint FROM %s WHERE comparison_id=$1`, table), comparisonID).
		Scan(&storedFingerprint); err != nil {
		return err
	}
	if storedFingerprint != contentFingerprint {
		return ConflictError{Message: "shadow comparison request belongs to different content"}
	}
	return nil
}

func shadowComparisonID(comparison FeatureShadowComparison) string {
	identity := fmt.Sprintf("%s\x00%s\x00%d\x00%s", comparison.RequestID,
		comparison.FeatureSetKey, comparison.FeatureSetVersion, subjectIdentity(comparison.Subject))
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}
