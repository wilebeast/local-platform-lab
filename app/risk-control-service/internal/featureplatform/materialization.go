package featureplatform

import (
	"context"
	"fmt"
)

type FeatureSetPublicationReader interface {
	GetFeatureSet(context.Context, string, int) (FeatureSetVersion, error)
}

// CurrentMaterializer is the controlled write seam between governed Feature
// Snapshots and the rebuildable Online Current Store.
type CurrentMaterializer struct {
	catalog FeatureSetPublicationReader
	store   CurrentFeatureStore
}

func NewCurrentMaterializer(
	catalog FeatureSetPublicationReader,
	store CurrentFeatureStore,
) *CurrentMaterializer {
	return &CurrentMaterializer{catalog: catalog, store: store}
}

func (m *CurrentMaterializer) MaterializeCurrent(
	ctx context.Context,
	snapshot FeatureSnapshot,
) (CurrentWriteResult, error) {
	if m == nil || m.catalog == nil || m.store == nil {
		return "", fmt.Errorf("feature catalog and current store are required")
	}
	featureSet, err := m.catalog.GetFeatureSet(ctx, snapshot.FeatureSetKey, snapshot.FeatureSetVersion)
	if err != nil {
		return "", fmt.Errorf("load feature set publication: %w", err)
	}
	if featureSet.Status != "published" {
		return "", InvalidStateError{Message: "only a published feature set can update the online current store"}
	}
	if snapshot.QualityStatus != "passed" {
		return "", InvalidStateError{Message: "only a quality-passed snapshot can update the online current store"}
	}
	return m.store.PutCurrent(ctx, snapshot)
}
