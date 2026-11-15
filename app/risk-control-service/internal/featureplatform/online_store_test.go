package featureplatform

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCurrentFeatureStoreKeepsNewestSnapshotAndRejectsConflictingReplay(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryCurrentFeatureStore()
	base := onlineStoreTestSnapshot(time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC), int64(100_000), "fingerprint-v1")

	result, err := store.PutCurrent(ctx, base)
	if err != nil || result != CurrentWriteApplied {
		t.Fatalf("initial write result=%q err=%v", result, err)
	}
	result, err = store.PutCurrent(ctx, onlineStoreTestSnapshot(base.AsOf.Add(-time.Minute), int64(80_000), "older"))
	if err != nil || result != CurrentWriteIgnoredOlder {
		t.Fatalf("older write result=%q err=%v", result, err)
	}
	result, err = store.PutCurrent(ctx, base)
	if err != nil || result != CurrentWriteReplay {
		t.Fatalf("replay result=%q err=%v", result, err)
	}
	conflicting := onlineStoreTestSnapshot(base.AsOf, int64(110_000), "different")
	if _, err := store.PutCurrent(ctx, conflicting); !errors.Is(err, ErrCurrentSnapshotConflict) {
		t.Fatalf("conflicting replay error=%v, want ErrCurrentSnapshotConflict", err)
	}

	rows, err := store.ReadLatestBefore(ctx, SnapshotReadRequest{
		RequestID: "current-read-001", FeatureSetKey: base.FeatureSetKey,
		FeatureSetVersion: base.FeatureSetVersion, FeatureKeys: []string{"inflow_minor"},
		Subjects: []Subject{base.Subject}, Cutoff: base.AsOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Values["inflow_minor"] != int64(100_000) || rows[0].ContentFingerprint != "fingerprint-v1" {
		t.Fatalf("unexpected current snapshot: %+v", rows)
	}
}

func TestShadowFeatureSnapshotReaderRecordsMismatchButReturnsReference(t *testing.T) {
	ctx := context.Background()
	cutoff := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	candidateStore := NewInMemoryCurrentFeatureStore()
	if _, err := candidateStore.PutCurrent(ctx, onlineStoreTestSnapshot(cutoff, int64(90_000), "candidate")); err != nil {
		t.Fatal(err)
	}
	referenceSnapshot := onlineStoreTestSnapshot(cutoff, int64(100_000), "reference")
	reference := &staticFeatureSnapshotReader{latest: []FeatureSnapshot{referenceSnapshot}}
	recorder := &recordingShadowComparisonRecorder{}
	reader := NewShadowFeatureSnapshotReader(candidateStore, reference, recorder)

	rows, err := reader.ReadLatestBefore(ctx, SnapshotReadRequest{
		RequestID: "shadow-read-001", FeatureSetKey: referenceSnapshot.FeatureSetKey,
		FeatureSetVersion: referenceSnapshot.FeatureSetVersion, FeatureKeys: []string{"inflow_minor"},
		Subjects: []Subject{referenceSnapshot.Subject}, Cutoff: cutoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Values["inflow_minor"] != int64(100_000) {
		t.Fatalf("shadow read must return reference data: %+v", rows)
	}
	if len(recorder.comparisons) != 1 || recorder.comparisons[0].Status != ShadowComparisonMismatch {
		t.Fatalf("unexpected comparisons: %+v", recorder.comparisons)
	}
	if len(recorder.comparisons[0].Differences) == 0 {
		t.Fatal("mismatch must explain at least one difference")
	}
	if !recorder.comparisons[0].Cutoff.Equal(cutoff) {
		t.Fatalf("comparison cutoff = %s, want %s", recorder.comparisons[0].Cutoff, cutoff)
	}
	if recorder.comparisons[0].CandidateReadDuration < 0 || recorder.comparisons[0].ReferenceReadDuration < 0 {
		t.Fatalf("read durations must be non-negative: %+v", recorder.comparisons[0])
	}
}

func TestOnlineMonitoredReaderRecordsCandidateFailureBeforeFailingRequest(t *testing.T) {
	ctx := context.Background()
	cutoff := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	snapshot := onlineStoreTestSnapshot(cutoff, int64(100_000), "reference")
	recorder := &recordingShadowComparisonRecorder{}
	reader := NewOnlineMonitoredFeatureSnapshotReader(
		&failingCurrentFeatureStore{err: errors.New("redis unavailable")},
		&staticFeatureSnapshotReader{latest: []FeatureSnapshot{snapshot}}, recorder,
	)
	_, err := reader.ReadLatestBefore(ctx, SnapshotReadRequest{
		RequestID: "online-candidate-failure", FeatureSetKey: snapshot.FeatureSetKey,
		FeatureSetVersion: snapshot.FeatureSetVersion, FeatureKeys: []string{"inflow_minor"},
		Subjects: []Subject{snapshot.Subject}, Cutoff: cutoff,
	})
	if err == nil {
		t.Fatal("expected candidate failure to fail online request")
	}
	if len(recorder.comparisons) != 1 || recorder.comparisons[0].Status != ShadowComparisonCandidateError {
		t.Fatalf("candidate failure evidence = %+v", recorder.comparisons)
	}
}

func onlineStoreTestSnapshot(asOf time.Time, inflow int64, fingerprint string) FeatureSnapshot {
	return FeatureSnapshot{
		FeatureSetKey: "subject_fund_activity", FeatureSetVersion: 1,
		Subject: Subject{Type: "organization", Keys: map[string]string{"subject_id": "org-001", "currency": "USD"}},
		AsOf:    asOf, ComputedAt: asOf.Add(time.Second), SourceRunID: "run-001",
		ContentFingerprint: fingerprint, QualityStatus: "passed",
		Values: map[string]any{"inflow_minor": inflow}, Undefined: map[string]bool{},
	}
}

type staticFeatureSnapshotReader struct {
	latest []FeatureSnapshot
	at     []FeatureSnapshot
}

func (r *staticFeatureSnapshotReader) ReadLatestBefore(context.Context, SnapshotReadRequest) ([]FeatureSnapshot, error) {
	return append([]FeatureSnapshot(nil), r.latest...), nil
}

func (r *staticFeatureSnapshotReader) ReadAt(context.Context, SnapshotReadRequest) ([]FeatureSnapshot, error) {
	return append([]FeatureSnapshot(nil), r.at...), nil
}

type recordingShadowComparisonRecorder struct{ comparisons []FeatureShadowComparison }

func (r *recordingShadowComparisonRecorder) Record(_ context.Context, comparisons []FeatureShadowComparison) error {
	r.comparisons = append(r.comparisons, comparisons...)
	return nil
}

type failingCurrentFeatureStore struct{ err error }

func (s *failingCurrentFeatureStore) PutCurrent(context.Context, FeatureSnapshot) (CurrentWriteResult, error) {
	return "", s.err
}

func (s *failingCurrentFeatureStore) ReadLatestBefore(context.Context, SnapshotReadRequest) ([]FeatureSnapshot, error) {
	return nil, s.err
}
