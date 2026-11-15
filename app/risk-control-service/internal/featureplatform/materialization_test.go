package featureplatform

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCurrentMaterializerPublishesQualityPassedSnapshotForPublishedFeatureSet(t *testing.T) {
	store := NewInMemoryCurrentFeatureStore()
	materializer := NewCurrentMaterializer(
		&materializationCatalogStub{featureSet: FeatureSetVersion{
			FeatureSetDraft: FeatureSetDraft{Key: "subject_fund_activity", Version: 1},
			Status:          "published",
		}},
		store,
	)
	snapshot := onlineStoreTestSnapshot(
		time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC),
		int64(100_000),
		"materialization-fingerprint",
	)

	result, err := materializer.MaterializeCurrent(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if result != CurrentWriteApplied {
		t.Fatalf("materialization result = %q, want %q", result, CurrentWriteApplied)
	}
	rows, err := store.ReadLatestBefore(context.Background(), SnapshotReadRequest{
		FeatureSetKey: snapshot.FeatureSetKey, FeatureSetVersion: snapshot.FeatureSetVersion,
		FeatureKeys: []string{"inflow_minor"}, Subjects: []Subject{snapshot.Subject}, Cutoff: snapshot.AsOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ContentFingerprint != snapshot.ContentFingerprint {
		t.Fatalf("materialized rows = %+v", rows)
	}
}

func TestCurrentMaterializerRejectsUnpublishedOrFailedQualitySnapshot(t *testing.T) {
	ctx := context.Background()
	snapshot := onlineStoreTestSnapshot(
		time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC), int64(100_000), "rejected",
	)
	store := NewInMemoryCurrentFeatureStore()

	for _, test := range []struct {
		name    string
		status  string
		quality string
	}{
		{name: "unpublished feature set", status: "validated", quality: "passed"},
		{name: "failed quality", status: "published", quality: "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := snapshot
			candidate.QualityStatus = test.quality
			materializer := NewCurrentMaterializer(
				&materializationCatalogStub{featureSet: FeatureSetVersion{
					FeatureSetDraft: FeatureSetDraft{Key: candidate.FeatureSetKey, Version: candidate.FeatureSetVersion},
					Status:          test.status,
				}},
				store,
			)
			if _, err := materializer.MaterializeCurrent(ctx, candidate); err == nil {
				t.Fatal("expected materialization rejection")
			} else {
				var invalidState InvalidStateError
				if !errors.As(err, &invalidState) {
					t.Fatalf("error = %T %v, want InvalidStateError", err, err)
				}
			}
		})
	}
}

type materializationCatalogStub struct {
	featureSet FeatureSetVersion
	err        error
}

func (s *materializationCatalogStub) GetFeatureSet(context.Context, string, int) (FeatureSetVersion, error) {
	return s.featureSet, s.err
}
