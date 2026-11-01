package warehouse

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresFeatureRunRegistry struct {
	pool *pgxpool.Pool
}

func NewPostgresFeatureRunRegistry(pool *pgxpool.Pool) *PostgresFeatureRunRegistry {
	return &PostgresFeatureRunRegistry{pool: pool}
}

func (r *PostgresFeatureRunRegistry) Register(
	ctx context.Context,
	sourceRunID string,
	contentFingerprint string,
) error {
	if r == nil || r.pool == nil || sourceRunID == "" || contentFingerprint == "" {
		return fmt.Errorf("PostgreSQL pool, source_run_id and content fingerprint are required")
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO risk_warehouse_feature_run_fingerprints(
			source_run_id,content_fingerprint,registered_at)
		VALUES($1,$2,NOW())
		ON CONFLICT (source_run_id) DO NOTHING`, sourceRunID, contentFingerprint); err != nil {
		return fmt.Errorf("register feature run fingerprint: %w", err)
	}
	var registeredFingerprint string
	if err := r.pool.QueryRow(ctx, `
		SELECT content_fingerprint
		FROM risk_warehouse_feature_run_fingerprints
		WHERE source_run_id=$1`, sourceRunID).Scan(&registeredFingerprint); err != nil {
		return fmt.Errorf("read feature run fingerprint: %w", err)
	}
	if registeredFingerprint != contentFingerprint {
		return FeatureRunFingerprintConflictError{SourceRunID: sourceRunID}
	}
	return nil
}
