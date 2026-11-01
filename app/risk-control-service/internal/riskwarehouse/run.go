package riskwarehouse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type WarehouseRunCommand struct {
	DAGRunID       string
	TriggerType    string
	RunReason      string
	AsOf           time.Time
	FeatureSetKey  string
	FeatureVersion int
	PolicyKey      string
	PolicyVersion  int
}

type PostgresWarehouseRunRecorder struct{ pool *pgxpool.Pool }

func NewPostgresWarehouseRunRecorder(pool *pgxpool.Pool) *PostgresWarehouseRunRecorder {
	return &PostgresWarehouseRunRecorder{pool: pool}
}

func ResolveWarehouseRunStatus(taskStates []string) string {
	if len(taskStates) == 0 {
		return "failed"
	}
	for _, state := range taskStates {
		if state != "success" {
			return "failed"
		}
	}
	return "succeeded"
}

func (r *PostgresWarehouseRunRecorder) Start(ctx context.Context, command WarehouseRunCommand) error {
	if err := validateWarehouseRunCommand(command); err != nil {
		return err
	}
	fingerprint := warehouseRunFingerprint(command)
	result, err := r.pool.Exec(ctx, `
		INSERT INTO risk_warehouse_runs(
			dag_run_id,trigger_type,run_reason,as_of,feature_set_key,feature_set_version,
			policy_key,policy_version,status,attempt_count,command_fingerprint,started_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'running',1,$9,NOW())
		ON CONFLICT (dag_run_id) DO UPDATE SET
			status=CASE WHEN risk_warehouse_runs.status='failed' THEN 'running' ELSE risk_warehouse_runs.status END,
			attempt_count=CASE WHEN risk_warehouse_runs.status='failed' THEN risk_warehouse_runs.attempt_count+1 ELSE risk_warehouse_runs.attempt_count END,
			started_at=CASE WHEN risk_warehouse_runs.status='failed' THEN NOW() ELSE risk_warehouse_runs.started_at END,
			completed_at=CASE WHEN risk_warehouse_runs.status='failed' THEN NULL ELSE risk_warehouse_runs.completed_at END
		WHERE risk_warehouse_runs.command_fingerprint=EXCLUDED.command_fingerprint`,
		command.DAGRunID, command.TriggerType, command.RunReason, command.AsOf.UTC(),
		command.FeatureSetKey, command.FeatureVersion, command.PolicyKey, command.PolicyVersion, fingerprint,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("warehouse run %q belongs to another command", command.DAGRunID)
	}
	return nil
}

func (r *PostgresWarehouseRunRecorder) Finalize(
	ctx context.Context,
	dagRunID string,
	taskStates []string,
) error {
	if dagRunID == "" {
		return fmt.Errorf("dag_run_id is required")
	}
	if len(taskStates) != 3 {
		return fmt.Errorf("build, validation and evaluation task states are required")
	}
	status := ResolveWarehouseRunStatus(taskStates)
	failures := 0
	for _, state := range taskStates {
		if state != "success" {
			failures++
		}
	}
	snapshots := 0
	if taskStates[1] == "success" {
		snapshots = 1
	}
	return r.FinalizeOutcome(ctx, dagRunID, status, snapshots, failures)
}

func (r *PostgresWarehouseRunRecorder) FinalizeOutcome(
	ctx context.Context,
	dagRunID string,
	status string,
	snapshotCount int,
	failureCount int,
) error {
	if dagRunID == "" {
		return fmt.Errorf("dag_run_id is required")
	}
	if status != "succeeded" && status != "failed" {
		return fmt.Errorf("warehouse run status must be succeeded or failed")
	}
	if snapshotCount < 0 || failureCount < 0 {
		return fmt.Errorf("warehouse run counts cannot be negative")
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE risk_warehouse_runs
		SET status=$2,
			snapshot_count=$3,
			decision_count=(SELECT count(*) FROM risk_warehouse_evaluations WHERE dag_run_id=$1),
			failure_count=$4,
			completed_at=NOW()
		WHERE dag_run_id=$1`, dagRunID, status, snapshotCount, failureCount)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("warehouse run %q was not registered", dagRunID)
	}
	return nil
}

func validateWarehouseRunCommand(command WarehouseRunCommand) error {
	if command.DAGRunID == "" || command.TriggerType == "" || command.AsOf.IsZero() ||
		command.FeatureSetKey == "" || command.FeatureVersion <= 0 ||
		command.PolicyKey == "" || command.PolicyVersion <= 0 {
		return fmt.Errorf("complete warehouse run metadata is required")
	}
	return nil
}

func warehouseRunFingerprint(command WarehouseRunCommand) string {
	value := fmt.Sprintf("%s\n%s\n%s\n%s\n%d\n%s\n%d", command.TriggerType, command.RunReason,
		command.AsOf.UTC().Format(time.RFC3339Nano), command.FeatureSetKey, command.FeatureVersion,
		command.PolicyKey, command.PolicyVersion)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
