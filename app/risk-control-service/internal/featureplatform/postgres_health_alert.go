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

type PostgresFeatureServingHealthAlertStore struct {
	pool   *pgxpool.Pool
	schema string
}

func NewPostgresFeatureServingHealthAlertStore(
	pool *pgxpool.Pool,
	schema string,
) *PostgresFeatureServingHealthAlertStore {
	return &PostgresFeatureServingHealthAlertStore{pool: pool, schema: schema}
}

func (s *PostgresFeatureServingHealthAlertStore) RecordHealthAlert(
	ctx context.Context,
	alert FeatureServingHealthAlert,
) (FeatureServingHealthAlert, error) {
	if err := s.ready(); err != nil {
		return FeatureServingHealthAlert{}, err
	}
	if err := validateFeatureServingHealthAlert(alert); err != nil {
		return FeatureServingHealthAlert{}, err
	}
	reasons, err := json.Marshal(alert.ReasonCodes)
	if err != nil {
		return FeatureServingHealthAlert{}, err
	}
	evidence, err := json.Marshal(alert.Evidence)
	if err != nil {
		return FeatureServingHealthAlert{}, err
	}
	criteria, err := json.Marshal(alert.Criteria)
	if err != nil {
		return FeatureServingHealthAlert{}, err
	}
	contentFingerprint, err := fingerprint(struct {
		CheckID           string
		FeatureSetKey     string
		FeatureSetVersion int
		RouteGeneration   int64
		ReasonCodes       []string
		Evidence          PromotionEvidence
		Criteria          PromotionCriteria
		Recommendation    HealthRecommendation
	}{
		alert.CheckID, alert.FeatureSetKey, alert.FeatureSetVersion,
		alert.RouteGeneration, alert.ReasonCodes, alert.Evidence,
		alert.Criteria, alert.RecommendedAction,
	})
	if err != nil {
		return FeatureServingHealthAlert{}, err
	}
	table := pgx.Identifier{s.schema, "feature_serving_health_alerts"}.Sanitize()
	_, err = s.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s(
			alert_id,check_id,feature_set_key,feature_set_version,route_generation,
			alert_status,reason_codes,evidence,criteria,recommended_action,
			content_fingerprint,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT(alert_id) DO NOTHING`, table),
		alert.AlertID, alert.CheckID, alert.FeatureSetKey, alert.FeatureSetVersion,
		alert.RouteGeneration, alert.Status, reasons, evidence, criteria,
		alert.RecommendedAction, contentFingerprint, alert.CreatedAt)
	if err != nil {
		return FeatureServingHealthAlert{}, fmt.Errorf("record feature serving health alert: %w", err)
	}
	stored, storedFingerprint, err := s.get(ctx, alert.AlertID)
	if err != nil {
		return FeatureServingHealthAlert{}, err
	}
	if storedFingerprint != contentFingerprint {
		return FeatureServingHealthAlert{}, ConflictError{Message: "health alert ID belongs to different evidence"}
	}
	return stored, nil
}

func (s *PostgresFeatureServingHealthAlertStore) ResolveOpenHealthAlerts(
	ctx context.Context,
	featureSetKey string,
	featureSetVersion int,
	routeGeneration int64,
	resolvedAt time.Time,
) (int64, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	if featureSetKey == "" || featureSetVersion <= 0 || routeGeneration < 0 || resolvedAt.IsZero() {
		return 0, InvalidRequestError{Message: "complete health alert resolution identity is required"}
	}
	table := pgx.Identifier{s.schema, "feature_serving_health_alerts"}.Sanitize()
	tag, err := s.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s SET alert_status='resolved',resolved_at=$4
		WHERE feature_set_key=$1 AND feature_set_version=$2 AND route_generation=$3
		  AND alert_status='open'`, table),
		featureSetKey, featureSetVersion, routeGeneration, resolvedAt)
	if err != nil {
		return 0, fmt.Errorf("resolve feature serving health alerts: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *PostgresFeatureServingHealthAlertStore) get(
	ctx context.Context,
	alertID string,
) (FeatureServingHealthAlert, string, error) {
	table := pgx.Identifier{s.schema, "feature_serving_health_alerts"}.Sanitize()
	var alert FeatureServingHealthAlert
	var reasons, evidence, criteria []byte
	var contentFingerprint string
	var resolvedAt *time.Time
	err := s.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT alert_id,check_id,feature_set_key,feature_set_version,route_generation,
			alert_status,reason_codes,evidence,criteria,recommended_action,
			content_fingerprint,created_at,resolved_at
		FROM %s WHERE alert_id=$1`, table), alertID).Scan(
		&alert.AlertID, &alert.CheckID, &alert.FeatureSetKey, &alert.FeatureSetVersion,
		&alert.RouteGeneration, &alert.Status, &reasons, &evidence, &criteria,
		&alert.RecommendedAction, &contentFingerprint, &alert.CreatedAt, &resolvedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return FeatureServingHealthAlert{}, "", NotFoundError{Message: "feature serving health alert not found"}
	}
	if err != nil {
		return FeatureServingHealthAlert{}, "", err
	}
	if err := json.Unmarshal(reasons, &alert.ReasonCodes); err != nil {
		return FeatureServingHealthAlert{}, "", err
	}
	if err := json.Unmarshal(evidence, &alert.Evidence); err != nil {
		return FeatureServingHealthAlert{}, "", err
	}
	if err := json.Unmarshal(criteria, &alert.Criteria); err != nil {
		return FeatureServingHealthAlert{}, "", err
	}
	return alert, contentFingerprint, nil
}

func (s *PostgresFeatureServingHealthAlertStore) ready() error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("postgres pool is required")
	}
	if !schemaNamePattern.MatchString(s.schema) {
		return fmt.Errorf("invalid postgres schema name %q", s.schema)
	}
	return nil
}

func validateFeatureServingHealthAlert(alert FeatureServingHealthAlert) error {
	if len(alert.AlertID) != 64 || alert.CheckID == "" || alert.FeatureSetKey == "" ||
		alert.FeatureSetVersion <= 0 || alert.RouteGeneration < 0 ||
		alert.Status != HealthAlertOpen || len(alert.ReasonCodes) == 0 ||
		alert.RecommendedAction != HealthRecommendationRollback || alert.CreatedAt.IsZero() {
		return InvalidRequestError{Message: "complete open feature serving health alert is required"}
	}
	return nil
}
