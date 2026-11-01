package warehouse_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wilebeast/local-platform-lab/app/risk-control-service/internal/warehouse"
)

func TestFundActivitySnapshotPublisherStopsBeforeSinkOnRegistryConflict(t *testing.T) {
	registry := &featureRunRegistryStub{err: warehouse.FeatureRunFingerprintConflictError{
		SourceRunID: "run-001",
	}}
	sink := &snapshotSinkStub{}
	publisher := warehouse.NewFundActivitySnapshotPublisher(registry, sink)

	err := publisher.Publish(context.Background(), []warehouse.SubjectFundActivitySnapshot{{
		FeatureSetKey: "subject_fund_activity_24h", FeatureSetVersion: 1,
		SubjectType: "organization", SubjectID: "org-001", Currency: "USD",
		AsOfMillis: 1793534400000, InflowMinor: 100000,
		SourceRunID: "run-001", ComputedAtMillis: 1793534460000,
	}})
	var conflict warehouse.FeatureRunFingerprintConflictError
	if !errors.As(err, &conflict) || conflict.SourceRunID != "run-001" {
		t.Fatalf("error = %v", err)
	}
	if sink.calls != 0 {
		t.Fatalf("sink calls = %d, want 0", sink.calls)
	}
	if registry.sourceRunID != "run-001" || registry.fingerprint == "" {
		t.Fatalf("registry request = %q %q", registry.sourceRunID, registry.fingerprint)
	}
}

type featureRunRegistryStub struct {
	sourceRunID string
	fingerprint string
	err         error
}

func (s *featureRunRegistryStub) Register(_ context.Context, sourceRunID, fingerprint string) error {
	s.sourceRunID = sourceRunID
	s.fingerprint = fingerprint
	return s.err
}

type snapshotSinkStub struct{ calls int }

func (s *snapshotSinkStub) SaveFundActivitySnapshots(_ context.Context, _ []warehouse.SubjectFundActivitySnapshot) error {
	s.calls++
	return nil
}
