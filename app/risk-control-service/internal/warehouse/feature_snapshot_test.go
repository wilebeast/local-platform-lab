package warehouse_test

import (
	"context"
	"testing"
	"time"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func TestFundActivityAggregatorBuildsPointInTime24HourSnapshot(t *testing.T) {
	asOf := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	facts := []warehouse.FundMovementFact{
		{EventID: "evt_in", SubjectType: "organization", SubjectID: "org_001", Currency: "USD", Direction: "inflow", AmountMinor: 100000, OccurredAtMillis: asOf.Add(-2 * time.Hour).UnixMilli()},
		{EventID: "evt_out", SubjectType: "organization", SubjectID: "org_001", Currency: "USD", Direction: "outflow", AmountMinor: 95000, OccurredAtMillis: asOf.Add(-time.Hour).UnixMilli()},
		{EventID: "evt_future", SubjectType: "organization", SubjectID: "org_001", Currency: "USD", Direction: "outflow", AmountMinor: 5000, OccurredAtMillis: asOf.Add(time.Minute).UnixMilli()},
	}

	snapshots, err := warehouse.NewFundActivityAggregator(24*time.Hour).Aggregate(
		context.Background(), asOf, facts,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(snapshots))
	}

	got := snapshots[0]
	if got.SubjectType != "organization" || got.SubjectID != "org_001" || got.Currency != "USD" {
		t.Fatalf("unexpected snapshot identity: %+v", got)
	}
	if got.InflowMinor != 100000 || got.OutflowMinor != 95000 || got.OutflowRatio != 0.95 || !got.OutflowRatioDefined {
		t.Fatalf("unexpected 24h values: %+v", got)
	}
	if got.WindowStartMillis != asOf.Add(-24*time.Hour).UnixMilli() || got.WindowEndMillis != asOf.UnixMilli() || got.AsOfMillis != asOf.UnixMilli() {
		t.Fatalf("unexpected point-in-time metadata: %+v", got)
	}
	if got.FeatureSetKey != "subject_fund_activity_24h" || got.FeatureSetVersion != 1 || got.SourceEventCount != 2 {
		t.Fatalf("unexpected feature metadata: %+v", got)
	}
}

func TestFundActivityAggregatorMarksOutflowRatioUndefinedWithoutInflow(t *testing.T) {
	asOf := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	facts := []warehouse.FundMovementFact{
		{EventID: "evt_out", SubjectType: "organization", SubjectID: "org_002", Currency: "USD", Direction: "outflow", AmountMinor: 5000, OccurredAtMillis: asOf.Add(-time.Hour).UnixMilli()},
	}
	snapshots, err := warehouse.NewFundActivityAggregator(24*time.Hour).Aggregate(context.Background(), asOf, facts)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].OutflowRatioDefined {
		t.Fatalf("outflow-only snapshot must not publish a defined ratio: %+v", snapshots)
	}
}
