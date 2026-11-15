package featureplatform

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (r *PostgresRegistry) DeprecateFeatureSet(ctx context.Context, key string, version int) error {
	return r.transitionFeatureSet(ctx, key, version, "published", "deprecated", "deprecated_at=NOW()")
}

func (r *PostgresRegistry) RestoreFeatureSet(ctx context.Context, key string, version int) error {
	return r.transitionFeatureSet(ctx, key, version, "deprecated", "published", "deprecated_at=NULL")
}

func (r *PostgresRegistry) RetireFeatureSet(ctx context.Context, key string, version int) error {
	return r.transitionFeatureSet(ctx, key, version, "deprecated", "retired", "retired_at=NOW()")
}

func (r *PostgresRegistry) transitionFeatureSet(
	ctx context.Context,
	key string,
	version int,
	from string,
	to string,
	timestampAssignment string,
) error {
	if err := r.ready(); err != nil {
		return err
	}
	if key == "" || version <= 0 {
		return fmt.Errorf("feature set key and positive version are required")
	}
	commandTag, err := r.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s SET status=$3, %s, updated_at=NOW()
		WHERE feature_set_key=$1 AND feature_set_version=$2 AND status=$4`,
		r.table("feature_set_versions"), timestampAssignment), key, version, to, from)
	if err != nil {
		return fmt.Errorf("transition feature set version from %s to %s: %w", from, to, err)
	}
	if commandTag.RowsAffected() == 1 {
		return nil
	}
	var status string
	err = r.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT status FROM %s WHERE feature_set_key=$1 AND feature_set_version=$2`,
		r.table("feature_set_versions")), key, version).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotFoundError{Message: "feature set version not found"}
	}
	if err != nil {
		return err
	}
	return InvalidStateError{Message: fmt.Sprintf("feature set version is %s, expected %s", status, from)}
}
