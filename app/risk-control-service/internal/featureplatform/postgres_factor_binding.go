package featureplatform

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// BindFeatureFactor records the direct dependency from a versioned Factor
// Definition to one published Feature Definition. Rules and strategies are not
// part of this relationship.
func (r *PostgresRegistry) BindFeatureFactor(ctx context.Context, binding FeatureFactorBinding) error {
	if err := r.ready(); err != nil {
		return err
	}
	if binding.ConsumerType == "" || binding.ConsumerKey == "" || binding.ConsumerVersion <= 0 ||
		binding.FactorKey == "" || binding.FeatureRef.FeatureSetKey == "" ||
		binding.FeatureRef.FeatureSetVersion <= 0 || binding.FeatureRef.FeatureKey == "" {
		return fmt.Errorf("complete factor and feature identities are required")
	}
	_, err := r.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s(
			consumer_type, consumer_key, consumer_version, factor_key,
			feature_set_key, feature_set_version, feature_key)
		SELECT $1,$2,$3,$4,$5,$6,$7
		WHERE EXISTS(
			SELECT 1 FROM %s d
			JOIN %s v
			  ON v.feature_set_key=d.feature_set_key
			 AND v.feature_set_version=d.feature_set_version
			WHERE d.feature_set_key=$5 AND d.feature_set_version=$6
			  AND d.feature_key=$7 AND v.status='published'
		)
		ON CONFLICT(consumer_type, consumer_key, consumer_version, factor_key) DO NOTHING`,
		r.table("feature_factor_bindings"), r.table("feature_definitions"), r.table("feature_set_versions")),
		binding.ConsumerType, binding.ConsumerKey, binding.ConsumerVersion, binding.FactorKey,
		binding.FeatureRef.FeatureSetKey, binding.FeatureRef.FeatureSetVersion, binding.FeatureRef.FeatureKey)
	if err != nil {
		return fmt.Errorf("bind feature factor: %w", err)
	}
	var stored FeatureRef
	err = r.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT feature_set_key, feature_set_version, feature_key
		FROM %s
		WHERE consumer_type=$1 AND consumer_key=$2 AND consumer_version=$3 AND factor_key=$4`,
		r.table("feature_factor_bindings")),
		binding.ConsumerType, binding.ConsumerKey, binding.ConsumerVersion, binding.FactorKey).
		Scan(&stored.FeatureSetKey, &stored.FeatureSetVersion, &stored.FeatureKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvalidStateError{Message: "feature factor can only reference a published feature"}
	}
	if err != nil {
		return err
	}
	if stored.FeatureSetKey != binding.FeatureRef.FeatureSetKey ||
		stored.FeatureSetVersion != binding.FeatureRef.FeatureSetVersion ||
		stored.FeatureKey != binding.FeatureRef.FeatureKey {
		return ConflictError{Message: "factor version is already bound to another feature"}
	}
	return nil
}

func (r *PostgresRegistry) RetireFeatureFactorBinding(
	ctx context.Context,
	consumerType string,
	consumerKey string,
	consumerVersion int,
	factorKey string,
) error {
	if err := r.ready(); err != nil {
		return err
	}
	if consumerType == "" || consumerKey == "" || consumerVersion <= 0 || factorKey == "" {
		return fmt.Errorf("complete factor binding identity is required")
	}
	commandTag, err := r.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s SET status='retired', retired_at=NOW()
		WHERE consumer_type=$1 AND consumer_key=$2 AND consumer_version=$3
		  AND factor_key=$4 AND status='active'`, r.table("feature_factor_bindings")),
		consumerType, consumerKey, consumerVersion, factorKey)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() == 1 {
		return nil
	}
	var status string
	err = r.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT status FROM %s
		WHERE consumer_type=$1 AND consumer_key=$2 AND consumer_version=$3 AND factor_key=$4`,
		r.table("feature_factor_bindings")), consumerType, consumerKey, consumerVersion, factorKey).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotFoundError{Message: "feature factor binding not found"}
	}
	if err != nil {
		return err
	}
	if status == "retired" {
		return nil
	}
	return InvalidStateError{Message: "feature factor binding cannot be retired from its current state"}
}
