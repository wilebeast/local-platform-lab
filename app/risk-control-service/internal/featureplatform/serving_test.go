package featureplatform

import (
	"context"
	"testing"
	"time"
)

func TestServingBatchGetLatestBeforeReadsOneSnapshotPerFeatureSet(t *testing.T) {
	cutoff := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	reader := &recordingSnapshotReader{
		snapshots: []FeatureSnapshot{
			{
				FeatureSetKey:     "subject_fund_activity",
				FeatureSetVersion: 1,
				Subject: Subject{
					Type: "organization",
					Keys: map[string]string{"subject_id": "org-001", "currency": "USD"},
				},
				AsOf:               cutoff.Add(-2 * time.Minute),
				ComputedAt:         cutoff.Add(-90 * time.Second),
				SourceRunID:        "run-001",
				ContentFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				QualityStatus:      "passed",
				Values: map[string]any{
					"inflow_minor":  int64(100_000),
					"outflow_minor": int64(0),
				},
				Undefined: map[string]bool{"outflow_ratio": true},
			},
		},
	}
	catalog := staticFeatureCatalog{definitions: []ServingFeatureDefinition{
		{Ref: FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "inflow_minor"}, ValueType: "int", MaxAge: 10 * time.Minute},
		{Ref: FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "outflow_minor"}, ValueType: "int", MaxAge: 10 * time.Minute},
		{Ref: FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "outflow_ratio"}, ValueType: "double", MaxAge: 10 * time.Minute},
	}}
	authorizer := &recordingFeatureAuthorizer{}
	service := NewServingService(catalog, reader, authorizer)
	request := BatchGetLatestBeforeRequest{
		Principal: "risk-control-service",
		RequestID: "feature-request-001",
		Subjects: []Subject{
			{Type: "organization", Keys: map[string]string{"subject_id": "org-001", "currency": "USD"}},
		},
		Features: []FeatureRef{
			{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "inflow_minor"},
			{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "outflow_minor"},
			{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "outflow_ratio"},
		},
		Cutoff: cutoff,
		MaxAge: 5 * time.Minute,
	}
	response, err := service.BatchGetLatestBefore(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if reader.latestCalls != 1 {
		t.Fatalf("snapshot reads=%d, want one grouped read", reader.latestCalls)
	}
	if authorizer.calls != 1 || authorizer.lastPrincipal != request.Principal {
		t.Fatalf("unexpected authorization calls: %+v", authorizer)
	}
	if len(response.Values) != 3 {
		t.Fatalf("values=%d, want 3", len(response.Values))
	}
	if response.Values[0].Status != ValueStatusSuccess || response.Values[0].Value != int64(100_000) {
		t.Fatalf("unexpected inflow value: %+v", response.Values[0])
	}
	if response.Values[2].Status != ValueStatusUndefined || response.Values[2].Value != nil {
		t.Fatalf("unexpected ratio value: %+v", response.Values[2])
	}
	if !response.Values[0].SnapshotAsOf.Equal(cutoff.Add(-2*time.Minute)) || response.Values[0].SourceRunID != "run-001" {
		t.Fatalf("missing snapshot evidence: %+v", response.Values[0])
	}
}

func TestServingBatchGetAtRequiresExactHistoricalSnapshot(t *testing.T) {
	asOf := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	reader := &recordingSnapshotReader{
		atSnapshots: []FeatureSnapshot{
			{
				FeatureSetKey:     "subject_fund_activity",
				FeatureSetVersion: 1,
				Subject: Subject{Type: "organization", Keys: map[string]string{
					"subject_id": "org-001",
					"currency":   "USD",
				}},
				AsOf:               asOf,
				ComputedAt:         asOf.Add(time.Minute),
				SourceRunID:        "batch-run-014",
				ContentFingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				QualityStatus:      "passed",
				Values:             map[string]any{"inflow_minor": int64(80_000)},
			},
		},
	}
	ref := FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "inflow_minor"}
	service := NewServingService(
		staticFeatureCatalog{definitions: []ServingFeatureDefinition{{Ref: ref, ValueType: "int", MaxAge: 24 * time.Hour}}},
		reader,
		&recordingFeatureAuthorizer{},
	)
	response, err := service.BatchGetAt(context.Background(), BatchGetAtRequest{
		Principal: "offline-risk-worker",
		RequestID: "historical-request-001",
		Subjects: []Subject{{Type: "organization", Keys: map[string]string{
			"subject_id": "org-001",
			"currency":   "USD",
		}}},
		Features: []FeatureRef{ref},
		AsOf:     asOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reader.atCalls != 1 || reader.latestCalls != 0 {
		t.Fatalf("at_calls=%d latest_calls=%d", reader.atCalls, reader.latestCalls)
	}
	if len(response.Values) != 1 || response.Values[0].Status != ValueStatusSuccess {
		t.Fatalf("unexpected historical response: %+v", response)
	}
	if !response.Values[0].SnapshotAsOf.Equal(asOf) {
		t.Fatalf("snapshot_as_of=%s, want %s", response.Values[0].SnapshotAsOf, asOf)
	}
}

func TestServingPreservesStaleMissingAndQualityFailureSemantics(t *testing.T) {
	cutoff := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	ref := FeatureRef{FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1, FeatureKey: "inflow_minor"}
	reader := &recordingSnapshotReader{snapshots: []FeatureSnapshot{
		{
			FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1,
			Subject: Subject{Type: "organization", Keys: map[string]string{"subject_id": "org-stale", "currency": "USD"}},
			AsOf:    cutoff.Add(-11 * time.Minute), QualityStatus: "passed", Values: map[string]any{"inflow_minor": int64(10)},
		},
		{
			FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1,
			Subject: Subject{Type: "organization", Keys: map[string]string{"subject_id": "org-quality", "currency": "USD"}},
			AsOf:    cutoff.Add(-time.Minute), QualityStatus: "failed", Values: map[string]any{"inflow_minor": int64(20)},
		},
		{
			FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1,
			Subject: Subject{Type: "organization", Keys: map[string]string{"subject_id": "org-type", "currency": "USD"}},
			AsOf:    cutoff.Add(-time.Minute), QualityStatus: "passed", Values: map[string]any{"inflow_minor": "20"},
		},
	}}
	service := NewServingService(
		staticFeatureCatalog{definitions: []ServingFeatureDefinition{{Ref: ref, ValueType: "int", MaxAge: 10 * time.Minute}}},
		reader,
		&recordingFeatureAuthorizer{},
	)
	response, err := service.BatchGetLatestBefore(context.Background(), BatchGetLatestBeforeRequest{
		Principal: "risk-control-service",
		RequestID: "status-request-001",
		Subjects: []Subject{
			{Type: "organization", Keys: map[string]string{"subject_id": "org-stale", "currency": "USD"}},
			{Type: "organization", Keys: map[string]string{"subject_id": "org-missing", "currency": "USD"}},
			{Type: "organization", Keys: map[string]string{"subject_id": "org-quality", "currency": "USD"}},
			{Type: "organization", Keys: map[string]string{"subject_id": "org-type", "currency": "USD"}},
		},
		Features: []FeatureRef{ref},
		Cutoff:   cutoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Values) != 4 {
		t.Fatalf("values=%d, want 4", len(response.Values))
	}
	if response.Values[0].Status != ValueStatusStale || response.Values[0].ErrorCode != "max_age_exceeded" {
		t.Fatalf("unexpected stale result: %+v", response.Values[0])
	}
	if response.Values[1].Status != ValueStatusMissing || response.Values[1].Value != nil {
		t.Fatalf("unexpected missing result: %+v", response.Values[1])
	}
	if response.Values[2].Status != ValueStatusError || response.Values[2].ErrorCode != "quality_not_passed" {
		t.Fatalf("unexpected quality result: %+v", response.Values[2])
	}
	if response.Values[3].Status != ValueStatusError || response.Values[3].ErrorCode != "value_type_mismatch" {
		t.Fatalf("unexpected type mismatch result: %+v", response.Values[3])
	}
}

type staticFeatureCatalog struct {
	definitions []ServingFeatureDefinition
}

func (c staticFeatureCatalog) ResolveFeatures(context.Context, []FeatureRef) ([]ServingFeatureDefinition, error) {
	return c.definitions, nil
}

type recordingSnapshotReader struct {
	snapshots   []FeatureSnapshot
	atSnapshots []FeatureSnapshot
	latestCalls int
	atCalls     int
}

func (r *recordingSnapshotReader) ReadLatestBefore(context.Context, SnapshotReadRequest) ([]FeatureSnapshot, error) {
	r.latestCalls++
	return r.snapshots, nil
}

func (r *recordingSnapshotReader) ReadAt(context.Context, SnapshotReadRequest) ([]FeatureSnapshot, error) {
	r.atCalls++
	return r.atSnapshots, nil
}

type recordingFeatureAuthorizer struct {
	calls         int
	lastPrincipal string
}

func (a *recordingFeatureAuthorizer) Authorize(_ context.Context, principal string, _ []ServingFeatureDefinition) error {
	a.calls++
	a.lastPrincipal = principal
	return nil
}
