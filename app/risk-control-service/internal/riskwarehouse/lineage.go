package riskwarehouse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/risk"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

type WarehouseEvaluationLineage struct {
	DAGRunID        string
	TriggerType     string
	RunReason       string
	SubjectType     string
	SubjectID       string
	Currency        string
	AsOf            time.Time
	FeatureSnapshot warehouse.SubjectFundActivitySnapshot
	Evaluation      risk.PolicyEvaluation
}

type WarehouseEvaluationRecorder interface {
	SaveWarehouseEvaluation(context.Context, WarehouseEvaluationLineage) (bool, error)
}

func RecordWarehouseEvaluation(
	ctx context.Context,
	recorder WarehouseEvaluationRecorder,
	lineage WarehouseEvaluationLineage,
) (bool, error) {
	if recorder == nil {
		return false, fmt.Errorf("warehouse evaluation recorder is required")
	}
	if lineage.DAGRunID == "" || lineage.TriggerType == "" || lineage.SubjectType == "" ||
		lineage.SubjectID == "" || lineage.Currency == "" || lineage.AsOf.IsZero() {
		return false, fmt.Errorf("dag run, trigger, subject, currency and as_of are required")
	}
	if lineage.FeatureSnapshot.SourceRunID == "" || lineage.FeatureSnapshot.AsOfMillis != lineage.AsOf.UTC().UnixMilli() {
		return false, fmt.Errorf("exact feature run lineage is required")
	}
	if lineage.Evaluation.PolicyKey == "" || lineage.Evaluation.PolicyVersion <= 0 || lineage.Evaluation.Decision == "" {
		return false, fmt.Errorf("policy evaluation lineage is required")
	}
	return recorder.SaveWarehouseEvaluation(ctx, lineage)
}

type PostgresWarehouseEvaluationRecorder struct{ pool *pgxpool.Pool }

func NewPostgresWarehouseEvaluationRecorder(pool *pgxpool.Pool) *PostgresWarehouseEvaluationRecorder {
	return &PostgresWarehouseEvaluationRecorder{pool: pool}
}

func (r *PostgresWarehouseEvaluationRecorder) SaveWarehouseEvaluation(
	ctx context.Context,
	lineage WarehouseEvaluationLineage,
) (bool, error) {
	if r == nil || r.pool == nil {
		return false, fmt.Errorf("PostgreSQL pool is required")
	}
	evaluationID := warehouseEvaluationID(lineage)
	fingerprint, err := warehouseEvaluationFingerprint(lineage)
	if err != nil {
		return false, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, evaluationID); err != nil {
		return false, err
	}
	var existingFingerprint string
	err = tx.QueryRow(ctx, `SELECT command_fingerprint FROM risk_warehouse_evaluations WHERE evaluation_id=$1`, evaluationID).Scan(&existingFingerprint)
	if err == nil {
		if existingFingerprint != fingerprint {
			return false, risk.ConflictError{Message: "warehouse evaluation id belongs to another result"}
		}
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	reasons, _ := json.Marshal(lineage.Evaluation.ReasonCodes)
	actions, _ := json.Marshal(lineage.Evaluation.Actions)
	factors, _ := json.Marshal(lineage.Evaluation.Factors.Results())
	snapshot := lineage.FeatureSnapshot
	_, err = tx.Exec(ctx, `
		INSERT INTO risk_warehouse_evaluations(
			evaluation_id,dag_run_id,trigger_type,run_reason,
			feature_run_id,feature_set_key,feature_set_version,as_of,window_start,window_end,
			subject_type,subject_id,currency,policy_key,policy_version,decision,
			matched_strategy_key,matched_rule_key,reason_codes,actions,factors,command_fingerprint)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,
			NULLIF($17,''),NULLIF($18,''),$19,$20,$21,$22)`,
		evaluationID, lineage.DAGRunID, lineage.TriggerType, lineage.RunReason,
		snapshot.SourceRunID, snapshot.FeatureSetKey, snapshot.FeatureSetVersion, lineage.AsOf.UTC(),
		time.UnixMilli(snapshot.WindowStartMillis).UTC(), time.UnixMilli(snapshot.WindowEndMillis).UTC(),
		lineage.SubjectType, lineage.SubjectID, lineage.Currency,
		lineage.Evaluation.PolicyKey, lineage.Evaluation.PolicyVersion, lineage.Evaluation.Decision,
		lineage.Evaluation.MatchedStrategyKey, lineage.Evaluation.MatchedRuleKey,
		reasons, actions, factors, fingerprint,
	)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return false, nil
}

func warehouseEvaluationID(lineage WarehouseEvaluationLineage) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%s\n%s", lineage.DAGRunID, lineage.SubjectType, lineage.SubjectID, lineage.Currency)))
	return "warehouse-evaluation-" + hex.EncodeToString(sum[:12])
}

func warehouseEvaluationFingerprint(lineage WarehouseEvaluationLineage) (string, error) {
	payload := struct {
		DAGRunID       string
		TriggerType    string
		RunReason      string
		SubjectType    string
		SubjectID      string
		Currency       string
		AsOfMillis     int64
		FeatureRunID   string
		FeatureVersion int32
		PolicyKey      string
		PolicyVersion  int
		Decision       string
		ReasonCodes    []string
	}{
		lineage.DAGRunID, lineage.TriggerType, lineage.RunReason,
		lineage.SubjectType, lineage.SubjectID, lineage.Currency, lineage.AsOf.UTC().UnixMilli(),
		lineage.FeatureSnapshot.SourceRunID, lineage.FeatureSnapshot.FeatureSetVersion,
		lineage.Evaluation.PolicyKey, lineage.Evaluation.PolicyVersion,
		lineage.Evaluation.Decision, lineage.Evaluation.ReasonCodes,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
