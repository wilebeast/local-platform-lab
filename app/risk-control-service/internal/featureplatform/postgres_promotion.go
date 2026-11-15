package featureplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresPromotionRepository struct {
	pool   *pgxpool.Pool
	schema string
}

func NewPostgresPromotionRepository(pool *pgxpool.Pool, schema string) *PostgresPromotionRepository {
	return &PostgresPromotionRepository{pool: pool, schema: schema}
}

func (r *PostgresPromotionRepository) SummarizePromotionEvidence(
	ctx context.Context,
	query PromotionEvidenceQuery,
) (PromotionEvidence, error) {
	if err := r.ready(); err != nil {
		return PromotionEvidence{}, err
	}
	if query.FeatureSetKey == "" || query.FeatureSetVersion <= 0 || query.Start.IsZero() ||
		!query.End.After(query.Start) || query.BucketDuration <= 0 {
		return PromotionEvidence{}, InvalidRequestError{Message: "complete promotion evidence query is required"}
	}
	table := pgx.Identifier{r.schema, "feature_serving_shadow_comparisons"}.Sanitize()
	var evidence PromotionEvidence
	var maximumFreshnessMillis, p95LatencyMillis float64
	err := r.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT
			count(*),
			count(DISTINCT floor(extract(epoch FROM compared_at) / $5)),
			count(*) FILTER (WHERE comparison_status='matched'),
			count(*) FILTER (WHERE comparison_status='mismatch'),
			count(*) FILTER (WHERE comparison_status='missing_candidate'),
			count(*) FILTER (WHERE comparison_status='missing_reference'),
			count(*) FILTER (WHERE comparison_status='candidate_error'),
			count(*) FILTER (WHERE comparison_status='reference_error'),
			COALESCE(max(CASE
				WHEN candidate_as_of > '1970-01-01T00:00:00Z'::timestamptz
				THEN greatest(0, extract(epoch FROM (cutoff_at-candidate_as_of))*1000)
				ELSE 0 END), 0),
			COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY candidate_latency_millis)
				FILTER (WHERE comparison_status IN ('matched','mismatch')), 0)
		FROM %s
		WHERE feature_set_key=$1 AND feature_set_version=$2
		  AND compared_at >= $3 AND compared_at < $4`, table),
		query.FeatureSetKey, query.FeatureSetVersion, query.Start, query.End,
		query.BucketDuration.Seconds()).Scan(
		&evidence.TotalComparisons, &evidence.DistinctBuckets,
		&evidence.MatchedComparisons, &evidence.MismatchComparisons,
		&evidence.MissingCandidateComparisons, &evidence.MissingReferenceComparisons,
		&evidence.CandidateErrorComparisons, &evidence.ReferenceErrorComparisons,
		&maximumFreshnessMillis, &p95LatencyMillis,
	)
	if err != nil {
		return PromotionEvidence{}, err
	}
	evidence.MaximumCandidateFreshnessLag = time.Duration(maximumFreshnessMillis * float64(time.Millisecond))
	evidence.P95CandidateReadDuration = time.Duration(p95LatencyMillis * float64(time.Millisecond))
	return evidence, nil
}

func (r *PostgresPromotionRepository) ApplyPromotionAssessment(
	ctx context.Context,
	request PromotionRequest,
	assessment PromotionAssessment,
) (ServingRoute, error) {
	if err := r.ready(); err != nil {
		return ServingRoute{}, err
	}
	criteriaJSON, err := json.Marshal(assessment.Criteria)
	if err != nil {
		return ServingRoute{}, err
	}
	evidenceJSON, err := json.Marshal(assessment.Evidence)
	if err != nil {
		return ServingRoute{}, err
	}
	reasonsJSON, err := json.Marshal(assessment.Reasons)
	if err != nil {
		return ServingRoute{}, err
	}
	contentFingerprint, err := fingerprint(struct {
		RequestID         string
		FeatureSetKey     string
		FeatureSetVersion int
		EvidenceStart     time.Time
		EvidenceEnd       time.Time
		BucketDuration    time.Duration
		Criteria          PromotionCriteria
		Evidence          PromotionEvidence
		Eligible          bool
		Reasons           []string
		RequestedBy       string
		Reason            string
	}{
		RequestID: request.RequestID, FeatureSetKey: request.FeatureSetKey,
		FeatureSetVersion: request.FeatureSetVersion, EvidenceStart: request.EvidenceStart,
		EvidenceEnd: request.EvidenceEnd, BucketDuration: request.BucketDuration,
		Criteria: assessment.Criteria, Evidence: assessment.Evidence, Eligible: assessment.Eligible,
		Reasons: assessment.Reasons, RequestedBy: request.RequestedBy, Reason: request.Reason,
	})
	if err != nil {
		return ServingRoute{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ServingRoute{}, err
	}
	defer tx.Rollback(ctx)
	assessmentTable := pgx.Identifier{r.schema, "feature_serving_promotion_assessments"}.Sanitize()
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s(
			assessment_id,request_id,feature_set_key,feature_set_version,
			evidence_start,evidence_end,bucket_seconds,criteria,evidence,eligible,
			reasons,requested_by,request_reason,content_fingerprint,assessed_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT(assessment_id) DO NOTHING`, assessmentTable),
		assessment.AssessmentID, request.RequestID, request.FeatureSetKey, request.FeatureSetVersion,
		request.EvidenceStart, request.EvidenceEnd, int64(request.BucketDuration.Seconds()),
		criteriaJSON, evidenceJSON, assessment.Eligible, reasonsJSON, request.RequestedBy,
		request.Reason, contentFingerprint, assessment.AssessedAt)
	if err != nil {
		return ServingRoute{}, err
	}
	var storedFingerprint string
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT content_fingerprint FROM %s WHERE assessment_id=$1`, assessmentTable),
		assessment.AssessmentID).Scan(&storedFingerprint); err != nil {
		return ServingRoute{}, err
	}
	if storedFingerprint != contentFingerprint {
		return ServingRoute{}, ConflictError{Message: "promotion request belongs to different evidence or criteria"}
	}
	if assessment.Eligible {
		if _, err := r.applyRouteChange(ctx, tx, request.RequestID, request.FeatureSetKey,
			request.FeatureSetVersion, "promotion", ServingModeOnline, request.RequestedBy, request.Reason); err != nil {
			return ServingRoute{}, err
		}
	}
	route, err := r.getServingRoute(ctx, tx, request.FeatureSetKey, request.FeatureSetVersion, false)
	if err != nil {
		return ServingRoute{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServingRoute{}, err
	}
	return route, nil
}

func (r *PostgresPromotionRepository) RollbackServingRoute(
	ctx context.Context,
	request ServingRollbackRequest,
) (ServingRoute, error) {
	if err := r.ready(); err != nil {
		return ServingRoute{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ServingRoute{}, err
	}
	defer tx.Rollback(ctx)
	route, err := r.applyRouteChange(ctx, tx, request.RequestID, request.FeatureSetKey,
		request.FeatureSetVersion, "rollback", ServingModeShadow, request.RequestedBy, request.Reason)
	if err != nil {
		return ServingRoute{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServingRoute{}, err
	}
	return route, nil
}

func (r *PostgresPromotionRepository) GetServingRoute(
	ctx context.Context,
	featureSetKey string,
	featureSetVersion int,
) (ServingRoute, error) {
	if err := r.ready(); err != nil {
		return ServingRoute{}, err
	}
	return r.getServingRoute(ctx, r.pool, featureSetKey, featureSetVersion, false)
}

type routeQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (r *PostgresPromotionRepository) getServingRoute(
	ctx context.Context,
	query routeQuerier,
	featureSetKey string,
	featureSetVersion int,
	forUpdate bool,
) (ServingRoute, error) {
	table := pgx.Identifier{r.schema, "feature_serving_routes"}.Sanitize()
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	route := ServingRoute{FeatureSetKey: featureSetKey, FeatureSetVersion: featureSetVersion, Mode: ServingModeShadow}
	err := query.QueryRow(ctx, fmt.Sprintf(`
		SELECT serving_mode,generation,changed_by,change_reason,changed_at
		FROM %s WHERE feature_set_key=$1 AND feature_set_version=$2%s`, table, lock),
		featureSetKey, featureSetVersion).Scan(
		&route.Mode, &route.Generation, &route.ChangedBy, &route.ChangeReason, &route.ChangedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return route, nil
	}
	return route, err
}

func (r *PostgresPromotionRepository) applyRouteChange(
	ctx context.Context,
	tx pgx.Tx,
	requestID, featureSetKey string,
	featureSetVersion int,
	changeType string,
	toMode ServingMode,
	changedBy, reason string,
) (ServingRoute, error) {
	eventID := stableServingChangeID(changeType, requestID, featureSetKey, featureSetVersion)
	eventsTable := pgx.Identifier{r.schema, "feature_serving_route_events"}.Sanitize()
	eventFingerprint, err := fingerprint(struct {
		RequestID         string
		FeatureSetKey     string
		FeatureSetVersion int
		ChangeType        string
		ToMode            ServingMode
		ChangedBy         string
		Reason            string
	}{requestID, featureSetKey, featureSetVersion, changeType, toMode, changedBy, reason})
	if err != nil {
		return ServingRoute{}, err
	}
	var existingFingerprint string
	err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT content_fingerprint FROM %s WHERE event_id=$1`, eventsTable), eventID).
		Scan(&existingFingerprint)
	if err == nil {
		if existingFingerprint != eventFingerprint {
			return ServingRoute{}, ConflictError{Message: "serving route request belongs to different content"}
		}
		return r.getServingRoute(ctx, tx, featureSetKey, featureSetVersion, false)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ServingRoute{}, err
	}
	current, err := r.getServingRoute(ctx, tx, featureSetKey, featureSetVersion, true)
	if err != nil {
		return ServingRoute{}, err
	}
	generation := current.Generation
	if current.Mode != toMode {
		generation++
	}
	changedAt := time.Now().UTC()
	routesTable := pgx.Identifier{r.schema, "feature_serving_routes"}.Sanitize()
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s(feature_set_key,feature_set_version,serving_mode,generation,last_event_id,changed_by,change_reason,changed_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT(feature_set_key,feature_set_version) DO UPDATE SET
			serving_mode=EXCLUDED.serving_mode,generation=EXCLUDED.generation,
			last_event_id=EXCLUDED.last_event_id,changed_by=EXCLUDED.changed_by,
			change_reason=EXCLUDED.change_reason,changed_at=EXCLUDED.changed_at`, routesTable),
		featureSetKey, featureSetVersion, toMode, generation, eventID, changedBy, reason, changedAt)
	if err != nil {
		return ServingRoute{}, err
	}
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s(
			event_id,request_id,feature_set_key,feature_set_version,change_type,
			from_mode,to_mode,generation,changed_by,change_reason,content_fingerprint,changed_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, eventsTable),
		eventID, requestID, featureSetKey, featureSetVersion, changeType,
		current.Mode, toMode, generation, changedBy, reason, eventFingerprint, changedAt)
	if err != nil {
		return ServingRoute{}, err
	}
	return ServingRoute{
		FeatureSetKey: featureSetKey, FeatureSetVersion: featureSetVersion,
		Mode: toMode, Generation: generation, ChangedBy: changedBy,
		ChangeReason: reason, ChangedAt: changedAt,
	}, nil
}

func (r *PostgresPromotionRepository) ready() error {
	if r == nil || r.pool == nil || !schemaNamePattern.MatchString(r.schema) {
		return fmt.Errorf("PostgreSQL pool and safe schema are required")
	}
	return nil
}
