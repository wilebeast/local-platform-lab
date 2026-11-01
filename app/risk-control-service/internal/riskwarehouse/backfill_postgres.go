package riskwarehouse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresBackfillStore struct{ pool *pgxpool.Pool }

func NewPostgresBackfillStore(pool *pgxpool.Pool) *PostgresBackfillStore {
	return &PostgresBackfillStore{pool: pool}
}

func (s *PostgresBackfillStore) Start(ctx context.Context, command BackfillCommand, points []time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	fingerprint := backfillFingerprint(command)
	result, err := tx.Exec(ctx, `
		INSERT INTO risk_warehouse_backfills(
			backfill_id,start_as_of,end_as_of,step_seconds,status,attempt_count,total_count,
			pending_count,command_fingerprint,started_at)
		VALUES($1,$2,$3,$4,'running',1,$5,$5,$6,NOW())
		ON CONFLICT (backfill_id) DO UPDATE SET
			status=CASE WHEN risk_warehouse_backfills.status='failed' THEN 'running' ELSE risk_warehouse_backfills.status END,
			attempt_count=CASE WHEN risk_warehouse_backfills.status='failed' THEN risk_warehouse_backfills.attempt_count+1 ELSE risk_warehouse_backfills.attempt_count END,
			started_at=CASE WHEN risk_warehouse_backfills.status='failed' THEN NOW() ELSE risk_warehouse_backfills.started_at END,
			completed_at=CASE WHEN risk_warehouse_backfills.status='failed' THEN NULL ELSE risk_warehouse_backfills.completed_at END
		WHERE risk_warehouse_backfills.command_fingerprint=EXCLUDED.command_fingerprint`,
		command.BackfillID, command.Start.UTC(), command.End.UTC(), int64(command.Step/time.Second), len(points), fingerprint)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("backfill %q belongs to another command", command.BackfillID)
	}
	for _, point := range points {
		childRunID := fmt.Sprintf("%s:%d", command.BackfillID, point.UTC().Unix())
		if _, err := tx.Exec(ctx, `
			INSERT INTO risk_warehouse_backfill_points(backfill_id,as_of,child_run_id,status)
			VALUES($1,$2,$3,'pending')
			ON CONFLICT (backfill_id,as_of) DO NOTHING`, command.BackfillID, point.UTC(), childRunID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PostgresBackfillStore) PointStatuses(ctx context.Context, backfillID string) (map[time.Time]BackfillPointStatus, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT as_of,status FROM risk_warehouse_backfill_points
		WHERE backfill_id=$1 ORDER BY as_of`, backfillID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[time.Time]BackfillPointStatus)
	for rows.Next() {
		var point time.Time
		var status BackfillPointStatus
		if err := rows.Scan(&point, &status); err != nil {
			return nil, err
		}
		result[point.UTC()] = status
	}
	return result, rows.Err()
}

func (s *PostgresBackfillStore) SetPointStatus(
	ctx context.Context,
	backfillID string,
	point time.Time,
	status BackfillPointStatus,
	lastError string,
) error {
	if status != BackfillPointRun && status != BackfillPointOK && status != BackfillPointFail {
		return fmt.Errorf("unsupported backfill point status %q", status)
	}
	result, err := s.pool.Exec(ctx, `
		UPDATE risk_warehouse_backfill_points
		SET status=$3,
			attempt_count=CASE WHEN $3='running' THEN attempt_count+1 ELSE attempt_count END,
			last_error=$4,
			started_at=CASE WHEN $3='running' THEN NOW() ELSE started_at END,
			completed_at=CASE WHEN $3 IN ('succeeded','failed') THEN NOW() ELSE NULL END
		WHERE backfill_id=$1 AND as_of=$2`, backfillID, point.UTC(), status, lastError)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("backfill point %q at %s was not planned", backfillID, point.UTC().Format(time.RFC3339))
	}
	return nil
}

func (s *PostgresBackfillStore) Finalize(ctx context.Context, backfillID string, summary BackfillSummary) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE risk_warehouse_backfills
		SET status=$2,succeeded_count=$3,failed_count=$4,pending_count=$5,completed_at=NOW()
		WHERE backfill_id=$1`, backfillID, summary.Status, summary.SucceededCount, summary.FailedCount, summary.PendingCount)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("backfill %q was not registered", backfillID)
	}
	return nil
}

func backfillFingerprint(command BackfillCommand) string {
	value := fmt.Sprintf("%s\n%s\n%d", command.Start.UTC().Format(time.RFC3339Nano),
		command.End.UTC().Format(time.RFC3339Nano), int64(command.Step))
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
