package riskwarehouse

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

type PostgresWarehouseLineageCatalog struct{ pool *pgxpool.Pool }

func NewPostgresWarehouseLineageCatalog(pool *pgxpool.Pool) *PostgresWarehouseLineageCatalog {
	return &PostgresWarehouseLineageCatalog{pool: pool}
}

func (c *PostgresWarehouseLineageCatalog) LoadWarehouseRun(
	ctx context.Context,
	dagRunID string,
) (WarehouseRunLineage, bool, error) {
	var result WarehouseRunLineage
	err := c.pool.QueryRow(ctx, `SELECT dag_run_id,status FROM risk_warehouse_runs WHERE dag_run_id=$1`, dagRunID).
		Scan(&result.DAGRunID, &result.Status)
	if err == pgx.ErrNoRows {
		return WarehouseRunLineage{}, false, nil
	}
	return result, err == nil, err
}

func (c *PostgresWarehouseLineageCatalog) ListWarehouseEvaluations(
	ctx context.Context,
	dagRunID string,
) ([]WarehouseEvaluationReference, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT dag_run_id,feature_run_id FROM risk_warehouse_evaluations
		WHERE dag_run_id=$1
		ORDER BY subject_type,subject_id,currency,evaluated_at`, dagRunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]WarehouseEvaluationReference, 0)
	for rows.Next() {
		var result WarehouseEvaluationReference
		if err := rows.Scan(&result.DAGRunID, &result.FeatureRunID); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

type ClickHouseFeatureSnapshotIndex struct{ client *warehouse.ClickHouse }

func NewClickHouseFeatureSnapshotIndex(client *warehouse.ClickHouse) (*ClickHouseFeatureSnapshotIndex, error) {
	if client == nil {
		return nil, fmt.Errorf("ClickHouse client is required")
	}
	return &ClickHouseFeatureSnapshotIndex{client: client}, nil
}

func (i *ClickHouseFeatureSnapshotIndex) HasFeatureSnapshot(ctx context.Context, featureRunID string) (bool, error) {
	return i.client.HasFundActivitySnapshotSourceRun(ctx, featureRunID)
}

type PostgresLineageAuditRecorder struct{ pool *pgxpool.Pool }

func NewPostgresLineageAuditRecorder(pool *pgxpool.Pool) *PostgresLineageAuditRecorder {
	return &PostgresLineageAuditRecorder{pool: pool}
}

func (r *PostgresLineageAuditRecorder) Save(ctx context.Context, result LineageAuditResult) error {
	issues, err := json.Marshal(result.Issues)
	if err != nil {
		return err
	}
	featureRunIDs, err := json.Marshal(result.FeatureRunIDs)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO risk_warehouse_lineage_audits(
			dag_run_id,status,feature_run_id,feature_run_ids,evaluation_count,issues,audited_at)
		VALUES($1,$2,NULLIF($3,''),$4,$5,$6,NOW())`,
		result.DAGRunID, result.Status, result.FeatureRunID, featureRunIDs, result.EvaluationCount, issues)
	return err
}
