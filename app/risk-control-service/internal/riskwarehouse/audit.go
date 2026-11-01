package riskwarehouse

import (
	"context"
	"fmt"
)

type LineageStatus string

const (
	LineageHealthy LineageStatus = "healthy"
	LineageBroken  LineageStatus = "broken"
)

type LineageIssue struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	FeatureRunID string `json:"feature_run_id,omitempty"`
}

type LineageAuditResult struct {
	DAGRunID        string         `json:"dag_run_id"`
	FeatureRunID    string         `json:"feature_run_id,omitempty"`
	FeatureRunIDs   []string       `json:"feature_run_ids"`
	EvaluationCount int            `json:"evaluation_count"`
	Status          LineageStatus  `json:"status"`
	Issues          []LineageIssue `json:"issues"`
}

type WarehouseRunLineage struct {
	DAGRunID string
	Status   string
}

type WarehouseEvaluationReference struct {
	DAGRunID     string
	FeatureRunID string
}

type WarehouseLineageCatalog interface {
	LoadWarehouseRun(context.Context, string) (WarehouseRunLineage, bool, error)
	ListWarehouseEvaluations(context.Context, string) ([]WarehouseEvaluationReference, error)
}

type FeatureSnapshotIndex interface {
	HasFeatureSnapshot(context.Context, string) (bool, error)
}

type LineageAuditor struct {
	catalog   WarehouseLineageCatalog
	snapshots FeatureSnapshotIndex
}

func NewLineageAuditor(catalog WarehouseLineageCatalog, snapshots FeatureSnapshotIndex) *LineageAuditor {
	return &LineageAuditor{catalog: catalog, snapshots: snapshots}
}

func (a *LineageAuditor) AuditRun(ctx context.Context, dagRunID string) (LineageAuditResult, error) {
	if dagRunID == "" || a.catalog == nil || a.snapshots == nil {
		return LineageAuditResult{}, fmt.Errorf("dag run, lineage catalog and snapshot index are required")
	}
	result := LineageAuditResult{
		DAGRunID: dagRunID, Status: LineageHealthy,
		FeatureRunIDs: []string{}, Issues: []LineageIssue{},
	}
	run, found, err := a.catalog.LoadWarehouseRun(ctx, dagRunID)
	if err != nil {
		return LineageAuditResult{}, err
	}
	if !found {
		return broken(result, "warehouse_run_missing", "warehouse run was not found"), nil
	}
	if run.Status != "succeeded" {
		return broken(result, "warehouse_run_not_succeeded", "warehouse run is not succeeded"), nil
	}
	evaluations, err := a.catalog.ListWarehouseEvaluations(ctx, dagRunID)
	if err != nil {
		return LineageAuditResult{}, err
	}
	if len(evaluations) == 0 {
		return broken(result, "warehouse_evaluation_missing", "warehouse evaluation was not found"), nil
	}
	result.EvaluationCount = len(evaluations)
	for _, evaluation := range evaluations {
		if result.FeatureRunID == "" {
			result.FeatureRunID = evaluation.FeatureRunID
		}
		result.FeatureRunIDs = append(result.FeatureRunIDs, evaluation.FeatureRunID)
		found, err = a.snapshots.HasFeatureSnapshot(ctx, evaluation.FeatureRunID)
		if err != nil {
			return LineageAuditResult{}, err
		}
		if !found {
			result.Status = LineageBroken
			result.Issues = append(result.Issues, LineageIssue{
				Code: "feature_snapshot_missing", Message: "feature snapshot was not found",
				FeatureRunID: evaluation.FeatureRunID,
			})
		}
	}
	return result, nil
}

func broken(result LineageAuditResult, code, message string) LineageAuditResult {
	result.Status = LineageBroken
	result.Issues = append(result.Issues, LineageIssue{Code: code, Message: message})
	return result
}
