package riskwarehouse

import (
	"math"
	"strings"
	"testing"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func TestFeatureReconcilerMatchesEquivalentStreamAndBatchSnapshots(t *testing.T) {
	stream := reconciliationFixture("flink-shadow-v1")
	batch := reconciliationFixture("airflow-run-001")

	result, err := NewFeatureReconciler(ReconciliationThresholds{
		AmountMinor: 0,
		Ratio:       0.000001,
	}).Compare(stream, batch)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ReconciliationMatched || result.AlertRequired {
		t.Fatalf("expected matched result without alert, got %+v", result)
	}
	if result.InflowDeltaMinor != 0 || result.OutflowDeltaMinor != 0 || result.RatioDelta != 0 {
		t.Fatalf("expected zero deltas, got %+v", result)
	}
}

func TestFeatureReconcilerRejectsNonFiniteRatioThresholds(t *testing.T) {
	for _, threshold := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := NewFeatureReconciler(ReconciliationThresholds{Ratio: threshold}).Compare(
			reconciliationFixture("flink-shadow-v1"),
			reconciliationFixture("airflow-run-001"),
		)
		if err == nil {
			t.Fatalf("expected non-finite ratio threshold %v to be rejected", threshold)
		}
	}
}

func TestFeatureReconcilerRejectsDifferentPointInTimeAsNotComparable(t *testing.T) {
	stream := reconciliationFixture("flink-shadow-v1")
	batch := reconciliationFixture("airflow-run-001")
	batch.AsOfMillis -= 60_000
	batch.WindowEndMillis -= 60_000
	batch.WindowStartMillis -= 60_000

	result, err := NewFeatureReconciler(ReconciliationThresholds{}).Compare(stream, batch)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ReconciliationNotComparable || result.Reason != "point_in_time_mismatch" {
		t.Fatalf("expected point-in-time mismatch, got %+v", result)
	}
	if result.AlertRequired {
		t.Fatalf("non-comparable snapshots must not be reported as value drift: %+v", result)
	}
}

func TestFeatureReconcilerRaisesDriftWhenThresholdIsExceeded(t *testing.T) {
	stream := reconciliationFixture("flink-shadow-v1")
	batch := reconciliationFixture("airflow-run-001")
	stream.OutflowMinor = 96_000
	stream.OutflowRatio = 0.96

	result, err := NewFeatureReconciler(ReconciliationThresholds{
		AmountMinor: 100,
		Ratio:       0.001,
	}).Compare(stream, batch)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ReconciliationDrift || !result.AlertRequired {
		t.Fatalf("expected drift alert, got %+v", result)
	}
	if result.OutflowDeltaMinor != 1_000 || result.RatioDelta < 0.0099 {
		t.Fatalf("unexpected deltas: %+v", result)
	}
}

func TestMissingBatchSnapshotRaisesDataQualityAlert(t *testing.T) {
	result := MissingBatchReconciliation()
	if result.Status != ReconciliationBatchMissing || !result.AlertRequired || result.Reason != "batch_snapshot_missing" {
		t.Fatalf("expected missing batch alert, got %+v", result)
	}
}

func TestReconciliationRecordNormalizesClickHouseFixedStringPadding(t *testing.T) {
	stream := reconciliationFixture("flink-shadow-v1")
	batch := reconciliationFixture("airflow-run-001")
	batch.ContentFingerprint = strings.Repeat("\x00", 64)
	result, err := NewFeatureReconciler(ReconciliationThresholds{}).Compare(stream, batch)
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewFeatureReconciliationRecord(stream, batch, ReconciliationThresholds{}, result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(record.BatchSnapshotJSON, `\u0000`) {
		t.Fatalf("PostgreSQL JSONB cannot store NUL escapes: %s", record.BatchSnapshotJSON)
	}
}

func reconciliationFixture(sourceRunID string) warehouse.SubjectFundActivitySnapshot {
	return warehouse.SubjectFundActivitySnapshot{
		FeatureSetKey:       "subject_fund_activity_24h",
		FeatureSetVersion:   1,
		SubjectType:         "organization",
		SubjectID:           "org_coach",
		Currency:            "USD",
		WindowStartMillis:   1_793_448_000_000,
		WindowEndMillis:     1_793_534_400_000,
		AsOfMillis:          1_793_534_400_000,
		InflowMinor:         100_000,
		OutflowMinor:        95_000,
		OutflowRatio:        0.95,
		OutflowRatioDefined: true,
		SourceEventCount:    2,
		SourceRunID:         sourceRunID,
		ComputedAtMillis:    1_793_534_460_000,
	}
}
