package warehouse

import (
	"context"
	"fmt"
)

type FeatureRunFingerprintConflictError struct {
	SourceRunID string
}

func (e FeatureRunFingerprintConflictError) Error() string {
	return fmt.Sprintf("feature snapshot source_run_id conflict: %s", e.SourceRunID)
}

type FeatureRunRegistry interface {
	Register(context.Context, string, string) error
}

type FundActivitySnapshotSink interface {
	SaveFundActivitySnapshots(context.Context, []SubjectFundActivitySnapshot) error
}

type FundActivitySnapshotPublisher struct {
	registry FeatureRunRegistry
	sink     FundActivitySnapshotSink
}

func NewFundActivitySnapshotPublisher(
	registry FeatureRunRegistry,
	sink FundActivitySnapshotSink,
) *FundActivitySnapshotPublisher {
	return &FundActivitySnapshotPublisher{registry: registry, sink: sink}
}

func (p *FundActivitySnapshotPublisher) Publish(
	ctx context.Context,
	snapshots []SubjectFundActivitySnapshot,
) error {
	if p == nil || p.registry == nil || p.sink == nil {
		return fmt.Errorf("feature run registry and snapshot sink are required")
	}
	fingerprint, sourceRunID, err := FundActivitySnapshotContentFingerprint(snapshots)
	if err != nil {
		return err
	}
	if err := p.registry.Register(ctx, sourceRunID, fingerprint); err != nil {
		return err
	}
	return p.sink.SaveFundActivitySnapshots(ctx, snapshots)
}

func FundActivitySnapshotContentFingerprint(
	snapshots []SubjectFundActivitySnapshot,
) (fingerprint string, sourceRunID string, err error) {
	if len(snapshots) == 0 {
		return "", "", fmt.Errorf("feature snapshots are required")
	}
	_, fingerprint, sourceRunID, err = prepareFundActivitySnapshots(snapshots)
	return fingerprint, sourceRunID, err
}
