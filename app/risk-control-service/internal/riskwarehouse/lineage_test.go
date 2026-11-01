package riskwarehouse_test

import (
	"context"
	"testing"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/risk"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/riskwarehouse"
	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func TestRecordWarehouseEvaluationPreservesRunFeaturePolicyAndDecisionLineage(t *testing.T) {
	asOf := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	recorder := &lineageRecorderStub{}
	command := riskwarehouse.WarehouseEvaluationLineage{
		DAGRunID: "backfill_2026_10_31", TriggerType: "backfill", RunReason: "late bank file",
		SubjectType: "organization", SubjectID: "org_warehouse_coach", Currency: "USD",
		AsOf: asOf,
		FeatureSnapshot: warehouse.SubjectFundActivitySnapshot{
			FeatureSetKey: "subject_fund_activity_24h", FeatureSetVersion: 1,
			SourceRunID:       "backfill_2026_10_31:subject_fund_activity_24h:v1",
			WindowStartMillis: asOf.Add(-24 * time.Hour).UnixMilli(), WindowEndMillis: asOf.UnixMilli(),
			AsOfMillis: asOf.UnixMilli(),
		},
		Evaluation: risk.PolicyEvaluation{
			PolicyKey: "warehouse_rapid_outflow", PolicyVersion: 1, Decision: "block",
			ReasonCodes: []string{"rapid_outflow"}, MatchedStrategyKey: "block_rapid_outflow",
			MatchedRuleKey: "rapid_outflow",
		},
	}

	replay, err := riskwarehouse.RecordWarehouseEvaluation(context.Background(), recorder, command)
	if err != nil {
		t.Fatal(err)
	}
	if replay || recorder.calls != 1 {
		t.Fatalf("replay=%t calls=%d", replay, recorder.calls)
	}
	if recorder.got.DAGRunID != command.DAGRunID ||
		recorder.got.FeatureSnapshot.SourceRunID != command.FeatureSnapshot.SourceRunID ||
		recorder.got.Evaluation.PolicyVersion != 1 || recorder.got.Evaluation.Decision != "block" {
		t.Fatalf("lineage was not preserved: %+v", recorder.got)
	}
}

type lineageRecorderStub struct {
	calls int
	got   riskwarehouse.WarehouseEvaluationLineage
}

func (s *lineageRecorderStub) SaveWarehouseEvaluation(
	_ context.Context,
	lineage riskwarehouse.WarehouseEvaluationLineage,
) (bool, error) {
	s.calls++
	s.got = lineage
	return false, nil
}
