package riskwarehouse

import (
	"testing"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func TestValidateFundActivitySnapshotAcceptsExactAsOf(t *testing.T) {
	asOf := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	snapshot := warehouse.SubjectFundActivitySnapshot{
		FeatureSetKey: "subject_fund_activity_24h", FeatureSetVersion: 1,
		SubjectType: "organization", SubjectID: "org_warehouse_coach", Currency: "USD",
		WindowStartMillis: asOf.Add(-24 * time.Hour).UnixMilli(),
		WindowEndMillis:   asOf.UnixMilli(), AsOfMillis: asOf.UnixMilli(),
		InflowMinor: 100000, OutflowMinor: 95000, OutflowRatio: 0.95,
		OutflowRatioDefined: true, SourceEventCount: 2,
	}

	if err := ValidateFundActivitySnapshot(snapshot, asOf); err != nil {
		t.Fatalf("expected valid snapshot, got %v", err)
	}
}

func TestValidateFundActivitySnapshotRejectsDifferentAsOf(t *testing.T) {
	asOf := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	snapshot := warehouse.SubjectFundActivitySnapshot{
		FeatureSetKey: "subject_fund_activity_24h", FeatureSetVersion: 1,
		SubjectType: "organization", SubjectID: "org_warehouse_coach", Currency: "USD",
		WindowStartMillis: asOf.Add(-25 * time.Hour).UnixMilli(),
		WindowEndMillis:   asOf.Add(-time.Hour).UnixMilli(),
		AsOfMillis:        asOf.Add(-time.Hour).UnixMilli(),
	}

	err := ValidateFundActivitySnapshot(snapshot, asOf)
	if err == nil || err.Error() != "feature_as_of_mismatch" {
		t.Fatalf("expected feature_as_of_mismatch, got %v", err)
	}
}

func TestValidateFundActivitySnapshotRejectsInvalidWindow(t *testing.T) {
	asOf := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	snapshot := validFundActivitySnapshot(asOf)
	snapshot.WindowStartMillis = asOf.Add(-23 * time.Hour).UnixMilli()

	err := ValidateFundActivitySnapshot(snapshot, asOf)
	if err == nil || err.Error() != "feature_window_invalid" {
		t.Fatalf("expected feature_window_invalid, got %v", err)
	}
}

func TestValidateFundActivitySnapshotRejectsInvalidAmountsAndRatio(t *testing.T) {
	asOf := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*warehouse.SubjectFundActivitySnapshot)
		code   string
	}{
		{"negative inflow", func(s *warehouse.SubjectFundActivitySnapshot) { s.InflowMinor = -1 }, "feature_amount_negative"},
		{"ratio mismatch", func(s *warehouse.SubjectFundActivitySnapshot) { s.OutflowRatio = 0.80 }, "feature_ratio_mismatch"},
		{"undefined ratio with inflow", func(s *warehouse.SubjectFundActivitySnapshot) { s.OutflowRatioDefined = false }, "feature_ratio_definition_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := validFundActivitySnapshot(asOf)
			test.mutate(&snapshot)
			err := ValidateFundActivitySnapshot(snapshot, asOf)
			if err == nil || err.Error() != test.code {
				t.Fatalf("expected %s, got %v", test.code, err)
			}
		})
	}
}

func validFundActivitySnapshot(asOf time.Time) warehouse.SubjectFundActivitySnapshot {
	return warehouse.SubjectFundActivitySnapshot{
		FeatureSetKey: "subject_fund_activity_24h", FeatureSetVersion: 1,
		SubjectType: "organization", SubjectID: "org_warehouse_coach", Currency: "USD",
		WindowStartMillis: asOf.Add(-24 * time.Hour).UnixMilli(),
		WindowEndMillis:   asOf.UnixMilli(), AsOfMillis: asOf.UnixMilli(),
		InflowMinor: 100000, OutflowMinor: 95000, OutflowRatio: 0.95,
		OutflowRatioDefined: true, SourceEventCount: 2,
	}
}
