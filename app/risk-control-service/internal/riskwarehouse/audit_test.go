package riskwarehouse

import (
	"context"
	"testing"
)

func TestLineageAuditorReportsMissingFeatureSnapshot(t *testing.T) {
	catalog := &auditCatalogStub{
		run: WarehouseRunLineage{DAGRunID: "run-001", Status: "succeeded"},
		evaluation: WarehouseEvaluationReference{
			DAGRunID: "run-001", FeatureRunID: "feature-run-001",
		},
	}
	auditor := NewLineageAuditor(catalog, &snapshotIndexStub{found: false})

	result, err := auditor.AuditRun(context.Background(), "run-001")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LineageBroken || len(result.Issues) != 1 || result.Issues[0].Code != "feature_snapshot_missing" {
		t.Fatalf("result = %+v", result)
	}
}

func TestLineageAuditorAcceptsCompleteRunEvaluationAndSnapshotChain(t *testing.T) {
	catalog := &auditCatalogStub{
		run: WarehouseRunLineage{DAGRunID: "run-001", Status: "succeeded"},
		evaluation: WarehouseEvaluationReference{
			DAGRunID: "run-001", FeatureRunID: "feature-run-001",
		},
	}
	auditor := NewLineageAuditor(catalog, &snapshotIndexStub{found: true})

	result, err := auditor.AuditRun(context.Background(), "run-001")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LineageHealthy || len(result.Issues) != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestLineageAuditorChecksEveryEvaluationInTheRun(t *testing.T) {
	catalog := &auditCatalogStub{
		run: WarehouseRunLineage{DAGRunID: "run-001", Status: "succeeded"},
		evaluations: []WarehouseEvaluationReference{
			{DAGRunID: "run-001", FeatureRunID: "feature-run-001"},
			{DAGRunID: "run-001", FeatureRunID: "feature-run-002"},
		},
	}
	snapshots := &snapshotIndexStub{foundByRun: map[string]bool{
		"feature-run-001": true,
		"feature-run-002": false,
	}}
	auditor := NewLineageAuditor(catalog, snapshots)

	result, err := auditor.AuditRun(context.Background(), "run-001")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LineageBroken || result.EvaluationCount != 2 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.FeatureRunIDs) != 2 || len(snapshots.checked) != 2 {
		t.Fatalf("feature runs = %v checked = %v", result.FeatureRunIDs, snapshots.checked)
	}
	if len(result.Issues) != 1 || result.Issues[0].FeatureRunID != "feature-run-002" {
		t.Fatalf("issues = %+v", result.Issues)
	}
}

type auditCatalogStub struct {
	run         WarehouseRunLineage
	evaluation  WarehouseEvaluationReference
	evaluations []WarehouseEvaluationReference
}

func (s *auditCatalogStub) ListWarehouseEvaluations(_ context.Context, _ string) ([]WarehouseEvaluationReference, error) {
	if s.evaluations != nil {
		return s.evaluations, nil
	}
	if s.evaluation.DAGRunID == "" {
		return nil, nil
	}
	return []WarehouseEvaluationReference{s.evaluation}, nil
}

func (s *auditCatalogStub) LoadWarehouseRun(_ context.Context, _ string) (WarehouseRunLineage, bool, error) {
	return s.run, s.run.DAGRunID != "", nil
}

func (s *auditCatalogStub) LoadWarehouseEvaluation(_ context.Context, _ string) (WarehouseEvaluationReference, bool, error) {
	return s.evaluation, s.evaluation.DAGRunID != "", nil
}

type snapshotIndexStub struct {
	found      bool
	foundByRun map[string]bool
	checked    []string
}

func (s *snapshotIndexStub) HasFeatureSnapshot(_ context.Context, featureRunID string) (bool, error) {
	s.checked = append(s.checked, featureRunID)
	if s.foundByRun != nil {
		return s.foundByRun[featureRunID], nil
	}
	return s.found, nil
}
