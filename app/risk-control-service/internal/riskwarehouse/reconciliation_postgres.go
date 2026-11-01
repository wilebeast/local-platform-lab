package riskwarehouse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

const featureReconciliationTableDDL = `CREATE TABLE IF NOT EXISTS risk_feature_reconciliations (
    comparison_id TEXT PRIMARY KEY,
    feature_set_key TEXT NOT NULL,
    feature_set_version INTEGER NOT NULL CHECK (feature_set_version > 0),
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    currency TEXT NOT NULL,
    as_of TIMESTAMPTZ NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    stream_source_run_id TEXT NOT NULL,
    batch_source_run_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('matched', 'drift', 'not_comparable', 'batch_missing')),
    reason TEXT NOT NULL,
    inflow_delta_minor BIGINT NOT NULL,
    outflow_delta_minor BIGINT NOT NULL,
    ratio_delta DOUBLE PRECISION NOT NULL,
    source_event_count_delta BIGINT NOT NULL,
    amount_threshold_minor BIGINT NOT NULL CHECK (amount_threshold_minor >= 0),
    ratio_threshold DOUBLE PRECISION NOT NULL CHECK (ratio_threshold >= 0),
    alert_status TEXT NOT NULL CHECK (alert_status IN ('none', 'open')),
    stream_snapshot JSONB NOT NULL,
    batch_snapshot JSONB NOT NULL,
    compared_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`

type FeatureReconciliationRecord struct {
	ComparisonID       string
	Stream             warehouse.SubjectFundActivitySnapshot
	Batch              warehouse.SubjectFundActivitySnapshot
	Thresholds         ReconciliationThresholds
	Result             FeatureReconciliationResult
	StreamSnapshotJSON string
	BatchSnapshotJSON  string
	AlertStatus        string
}

func NewFeatureReconciliationRecord(
	stream warehouse.SubjectFundActivitySnapshot,
	batch warehouse.SubjectFundActivitySnapshot,
	thresholds ReconciliationThresholds,
	result FeatureReconciliationResult,
) (FeatureReconciliationRecord, error) {
	stream.ContentFingerprint = strings.TrimRight(stream.ContentFingerprint, "\x00")
	batch.ContentFingerprint = strings.TrimRight(batch.ContentFingerprint, "\x00")
	streamJSON, err := json.Marshal(stream)
	if err != nil {
		return FeatureReconciliationRecord{}, fmt.Errorf("encode stream snapshot: %w", err)
	}
	batchJSON, err := json.Marshal(batch)
	if err != nil {
		return FeatureReconciliationRecord{}, fmt.Errorf("encode batch snapshot: %w", err)
	}
	fingerprintInput, err := json.Marshal(struct {
		Stream     json.RawMessage          `json:"stream"`
		Batch      json.RawMessage          `json:"batch"`
		Thresholds ReconciliationThresholds `json:"thresholds"`
	}{streamJSON, batchJSON, thresholds})
	if err != nil {
		return FeatureReconciliationRecord{}, fmt.Errorf("encode reconciliation fingerprint: %w", err)
	}
	sum := sha256.Sum256(fingerprintInput)
	alertStatus := "none"
	if result.AlertRequired {
		alertStatus = "open"
	}
	return FeatureReconciliationRecord{
		ComparisonID:       hex.EncodeToString(sum[:]),
		Stream:             stream,
		Batch:              batch,
		Thresholds:         thresholds,
		Result:             result,
		StreamSnapshotJSON: string(streamJSON),
		BatchSnapshotJSON:  string(batchJSON),
		AlertStatus:        alertStatus,
	}, nil
}

type PostgresFeatureReconciliationStore struct{ pool *pgxpool.Pool }

func NewPostgresFeatureReconciliationStore(pool *pgxpool.Pool) *PostgresFeatureReconciliationStore {
	return &PostgresFeatureReconciliationStore{pool: pool}
}

func (s *PostgresFeatureReconciliationStore) EnsureSchema(ctx context.Context) error {
	if s.pool == nil {
		return fmt.Errorf("PostgreSQL pool is required")
	}
	_, err := s.pool.Exec(ctx, featureReconciliationTableDDL)
	return err
}

func (s *PostgresFeatureReconciliationStore) Save(
	ctx context.Context,
	record FeatureReconciliationRecord,
) (bool, error) {
	if s.pool == nil || record.ComparisonID == "" {
		return false, fmt.Errorf("PostgreSQL pool and comparison_id are required")
	}
	result, err := s.pool.Exec(ctx, `
		INSERT INTO risk_feature_reconciliations(
			comparison_id,feature_set_key,feature_set_version,subject_type,subject_id,currency,
			as_of,window_start,window_end,stream_source_run_id,batch_source_run_id,status,reason,
			inflow_delta_minor,outflow_delta_minor,ratio_delta,source_event_count_delta,
			amount_threshold_minor,ratio_threshold,alert_status,stream_snapshot,batch_snapshot)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21::jsonb,$22::jsonb)
		ON CONFLICT (comparison_id) DO NOTHING`,
		record.ComparisonID,
		record.Stream.FeatureSetKey, record.Stream.FeatureSetVersion,
		record.Stream.SubjectType, record.Stream.SubjectID, record.Stream.Currency,
		time.UnixMilli(record.Stream.AsOfMillis).UTC(),
		time.UnixMilli(record.Stream.WindowStartMillis).UTC(),
		time.UnixMilli(record.Stream.WindowEndMillis).UTC(),
		record.Stream.SourceRunID, record.Batch.SourceRunID,
		record.Result.Status, record.Result.Reason,
		record.Result.InflowDeltaMinor, record.Result.OutflowDeltaMinor,
		record.Result.RatioDelta, record.Result.SourceEventCountDelta,
		record.Thresholds.AmountMinor, record.Thresholds.Ratio,
		record.AlertStatus, record.StreamSnapshotJSON, record.BatchSnapshotJSON)
	if err != nil {
		return false, err
	}
	return result.RowsAffected() == 0, nil
}
