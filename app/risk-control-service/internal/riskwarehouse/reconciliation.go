package riskwarehouse

import (
	"fmt"
	"math"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

type ReconciliationStatus string

const (
	ReconciliationMatched       ReconciliationStatus = "matched"
	ReconciliationDrift         ReconciliationStatus = "drift"
	ReconciliationNotComparable ReconciliationStatus = "not_comparable"
	ReconciliationBatchMissing  ReconciliationStatus = "batch_missing"
)

type ReconciliationThresholds struct {
	AmountMinor int64
	Ratio       float64
}

func MissingBatchReconciliation() FeatureReconciliationResult {
	return FeatureReconciliationResult{
		Status:        ReconciliationBatchMissing,
		Reason:        "batch_snapshot_missing",
		AlertRequired: true,
	}
}

type FeatureReconciliationResult struct {
	Status                ReconciliationStatus
	Reason                string
	AlertRequired         bool
	InflowDeltaMinor      int64
	OutflowDeltaMinor     int64
	RatioDelta            float64
	SourceEventCountDelta int64
}

type FeatureReconciler struct {
	thresholds ReconciliationThresholds
}

func NewFeatureReconciler(thresholds ReconciliationThresholds) *FeatureReconciler {
	return &FeatureReconciler{thresholds: thresholds}
}

func (r *FeatureReconciler) Compare(
	stream warehouse.SubjectFundActivitySnapshot,
	batch warehouse.SubjectFundActivitySnapshot,
) (FeatureReconciliationResult, error) {
	if r.thresholds.AmountMinor < 0 || r.thresholds.Ratio < 0 ||
		math.IsNaN(r.thresholds.Ratio) || math.IsInf(r.thresholds.Ratio, 0) {
		return FeatureReconciliationResult{}, fmt.Errorf("reconciliation thresholds must be finite and non-negative")
	}
	if reason := incompatibleReason(stream, batch); reason != "" {
		return FeatureReconciliationResult{
			Status: ReconciliationNotComparable,
			Reason: reason,
		}, nil
	}

	result := FeatureReconciliationResult{
		InflowDeltaMinor:      stream.InflowMinor - batch.InflowMinor,
		OutflowDeltaMinor:     stream.OutflowMinor - batch.OutflowMinor,
		RatioDelta:            math.Abs(stream.OutflowRatio - batch.OutflowRatio),
		SourceEventCountDelta: stream.SourceEventCount - batch.SourceEventCount,
	}
	valueDrift := absInt64(result.InflowDeltaMinor) > r.thresholds.AmountMinor ||
		absInt64(result.OutflowDeltaMinor) > r.thresholds.AmountMinor ||
		result.RatioDelta > r.thresholds.Ratio ||
		result.SourceEventCountDelta != 0 ||
		stream.OutflowRatioDefined != batch.OutflowRatioDefined
	if valueDrift {
		result.Status = ReconciliationDrift
		result.Reason = "value_drift"
		result.AlertRequired = true
		return result, nil
	}
	result.Status = ReconciliationMatched
	result.Reason = "within_threshold"
	return result, nil
}

func incompatibleReason(
	stream warehouse.SubjectFundActivitySnapshot,
	batch warehouse.SubjectFundActivitySnapshot,
) string {
	if stream.FeatureSetKey != batch.FeatureSetKey ||
		stream.FeatureSetVersion != batch.FeatureSetVersion {
		return "feature_definition_mismatch"
	}
	if stream.SubjectType != batch.SubjectType ||
		stream.SubjectID != batch.SubjectID ||
		stream.Currency != batch.Currency {
		return "subject_mismatch"
	}
	if stream.AsOfMillis != batch.AsOfMillis ||
		stream.WindowStartMillis != batch.WindowStartMillis ||
		stream.WindowEndMillis != batch.WindowEndMillis {
		return "point_in_time_mismatch"
	}
	return ""
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
