package featureplatform

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"
)

type CurrentWriteResult string

const (
	CurrentWriteApplied      CurrentWriteResult = "applied"
	CurrentWriteReplay       CurrentWriteResult = "replay"
	CurrentWriteIgnoredOlder CurrentWriteResult = "ignored_older"
)

var ErrCurrentSnapshotConflict = errors.New("current snapshot identity has conflicting content")

// CurrentFeatureStore is the Online Store seam. It only serves the latest
// eligible snapshot; exact historical reads remain the History Store's job.
type CurrentFeatureStore interface {
	PutCurrent(context.Context, FeatureSnapshot) (CurrentWriteResult, error)
	ReadLatestBefore(context.Context, SnapshotReadRequest) ([]FeatureSnapshot, error)
}

type InMemoryCurrentFeatureStore struct {
	mu        sync.RWMutex
	snapshots map[string]FeatureSnapshot
}

func NewInMemoryCurrentFeatureStore() *InMemoryCurrentFeatureStore {
	return &InMemoryCurrentFeatureStore{snapshots: make(map[string]FeatureSnapshot)}
}

func (s *InMemoryCurrentFeatureStore) PutCurrent(_ context.Context, snapshot FeatureSnapshot) (CurrentWriteResult, error) {
	if err := validateCurrentSnapshot(snapshot); err != nil {
		return "", err
	}
	key := currentSnapshotIdentity(snapshot.FeatureSetKey, snapshot.FeatureSetVersion, snapshot.Subject)
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, found := s.snapshots[key]
	if found {
		switch {
		case snapshot.AsOf.Before(existing.AsOf):
			return CurrentWriteIgnoredOlder, nil
		case snapshot.AsOf.Equal(existing.AsOf) && snapshot.ContentFingerprint == existing.ContentFingerprint:
			return CurrentWriteReplay, nil
		case snapshot.AsOf.Equal(existing.AsOf):
			return "", ErrCurrentSnapshotConflict
		}
	}
	s.snapshots[key] = cloneFeatureSnapshot(snapshot)
	return CurrentWriteApplied, nil
}

func (s *InMemoryCurrentFeatureStore) ReadLatestBefore(_ context.Context, request SnapshotReadRequest) ([]FeatureSnapshot, error) {
	if err := validateCurrentReadRequest(request); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := make([]FeatureSnapshot, 0, len(request.Subjects))
	for _, subject := range request.Subjects {
		stored, found := s.snapshots[currentSnapshotIdentity(request.FeatureSetKey, request.FeatureSetVersion, subject)]
		if !found || stored.AsOf.After(request.Cutoff) {
			continue
		}
		rows = append(rows, projectFeatureSnapshot(stored, request.FeatureKeys))
	}
	return rows, nil
}

type ShadowComparisonStatus string

const (
	ShadowComparisonMatched          ShadowComparisonStatus = "matched"
	ShadowComparisonMismatch         ShadowComparisonStatus = "mismatch"
	ShadowComparisonMissingCandidate ShadowComparisonStatus = "missing_candidate"
	ShadowComparisonMissingReference ShadowComparisonStatus = "missing_reference"
	ShadowComparisonCandidateError   ShadowComparisonStatus = "candidate_error"
	ShadowComparisonReferenceError   ShadowComparisonStatus = "reference_error"
)

type FeatureShadowComparison struct {
	RequestID             string
	FeatureSetKey         string
	FeatureSetVersion     int
	Subject               Subject
	Cutoff                time.Time
	CandidateAsOf         time.Time
	ReferenceAsOf         time.Time
	CandidateReadDuration time.Duration
	ReferenceReadDuration time.Duration
	Status                ShadowComparisonStatus
	Differences           []string
	ComparedAt            time.Time
}

type ShadowComparisonRecorder interface {
	Record(context.Context, []FeatureShadowComparison) error
}

// ShadowFeatureSnapshotReader always returns the reference result. Candidate
// failures and mismatches are evidence for a future cutover decision, not a
// reason to change current business behaviour.
type ShadowFeatureSnapshotReader struct {
	candidate CurrentFeatureStore
	reference FeatureSnapshotReader
	recorder  ShadowComparisonRecorder
	now       func() time.Time
}

func NewShadowFeatureSnapshotReader(
	candidate CurrentFeatureStore,
	reference FeatureSnapshotReader,
	recorder ShadowComparisonRecorder,
) *ShadowFeatureSnapshotReader {
	return &ShadowFeatureSnapshotReader{
		candidate: candidate, reference: reference, recorder: recorder,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (r *ShadowFeatureSnapshotReader) ReadLatestBefore(
	ctx context.Context,
	request SnapshotReadRequest,
) ([]FeatureSnapshot, error) {
	referenceStartedAt := r.now()
	reference, err := r.reference.ReadLatestBefore(ctx, request)
	referenceReadDuration := r.now().Sub(referenceStartedAt)
	if err != nil {
		return nil, err
	}
	candidateStartedAt := r.now()
	candidate, candidateErr := r.candidate.ReadLatestBefore(ctx, request)
	candidateReadDuration := r.now().Sub(candidateStartedAt)
	comparisons := compareFeatureSnapshots(
		request, candidate, reference, candidateErr, nil, r.now(),
		candidateReadDuration, referenceReadDuration,
	)
	if r.recorder != nil {
		_ = r.recorder.Record(ctx, comparisons)
	}
	return reference, nil
}

func (r *ShadowFeatureSnapshotReader) ReadAt(ctx context.Context, request SnapshotReadRequest) ([]FeatureSnapshot, error) {
	return r.reference.ReadAt(ctx, request)
}

func compareFeatureSnapshots(
	request SnapshotReadRequest,
	candidate []FeatureSnapshot,
	reference []FeatureSnapshot,
	candidateErr error,
	referenceErr error,
	comparedAt time.Time,
	candidateReadDuration time.Duration,
	referenceReadDuration time.Duration,
) []FeatureShadowComparison {
	candidateBySubject := indexSnapshotsBySubject(candidate)
	referenceBySubject := indexSnapshotsBySubject(reference)
	comparisons := make([]FeatureShadowComparison, 0, len(request.Subjects))
	for _, subject := range request.Subjects {
		comparison := FeatureShadowComparison{
			RequestID: request.RequestID, FeatureSetKey: request.FeatureSetKey,
			FeatureSetVersion: request.FeatureSetVersion, Subject: cloneSubject(subject),
			Cutoff: request.Cutoff, CandidateReadDuration: candidateReadDuration,
			ReferenceReadDuration: referenceReadDuration, ComparedAt: comparedAt,
		}
		if candidateErr != nil {
			comparison.Status = ShadowComparisonCandidateError
			comparison.Differences = []string{"candidate_error"}
			comparisons = append(comparisons, comparison)
			continue
		}
		if referenceErr != nil {
			comparison.Status = ShadowComparisonReferenceError
			comparison.Differences = []string{"reference_error"}
			comparisons = append(comparisons, comparison)
			continue
		}
		candidateSnapshot, candidateFound := candidateBySubject[subjectIdentity(subject)]
		referenceSnapshot, referenceFound := referenceBySubject[subjectIdentity(subject)]
		if candidateFound {
			comparison.CandidateAsOf = candidateSnapshot.AsOf
		}
		if referenceFound {
			comparison.ReferenceAsOf = referenceSnapshot.AsOf
		}
		switch {
		case !candidateFound && referenceFound:
			comparison.Status = ShadowComparisonMissingCandidate
			comparison.Differences = []string{"candidate_missing"}
		case candidateFound && !referenceFound:
			comparison.Status = ShadowComparisonMissingReference
			comparison.Differences = []string{"reference_missing"}
		case !candidateFound && !referenceFound:
			comparison.Status = ShadowComparisonMatched
		default:
			comparison.Differences = snapshotDifferences(candidateSnapshot, referenceSnapshot, request.FeatureKeys)
			if len(comparison.Differences) == 0 {
				comparison.Status = ShadowComparisonMatched
			} else {
				comparison.Status = ShadowComparisonMismatch
			}
		}
		comparisons = append(comparisons, comparison)
	}
	return comparisons
}

// OnlineMonitoredFeatureSnapshotReader serves the candidate while continuing
// to compare it with the reference. Candidate failures remain fail-closed for
// the request; reference failures only reduce observability and are recorded
// for the health monitor instead of breaking online serving.
type OnlineMonitoredFeatureSnapshotReader struct {
	candidate CurrentFeatureStore
	reference FeatureSnapshotReader
	recorder  ShadowComparisonRecorder
	now       func() time.Time
}

func NewOnlineMonitoredFeatureSnapshotReader(
	candidate CurrentFeatureStore,
	reference FeatureSnapshotReader,
	recorder ShadowComparisonRecorder,
) *OnlineMonitoredFeatureSnapshotReader {
	return &OnlineMonitoredFeatureSnapshotReader{
		candidate: candidate, reference: reference, recorder: recorder,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (r *OnlineMonitoredFeatureSnapshotReader) ReadLatestBefore(
	ctx context.Context,
	request SnapshotReadRequest,
) ([]FeatureSnapshot, error) {
	candidateStartedAt := r.now()
	candidate, candidateErr := r.candidate.ReadLatestBefore(ctx, request)
	candidateReadDuration := r.now().Sub(candidateStartedAt)
	if candidateErr != nil {
		comparisons := compareFeatureSnapshots(
			request, nil, nil, candidateErr, nil, r.now(), candidateReadDuration, 0,
		)
		if r.recorder != nil {
			_ = r.recorder.Record(ctx, comparisons)
		}
		return nil, candidateErr
	}
	referenceStartedAt := r.now()
	reference, referenceErr := r.reference.ReadLatestBefore(ctx, request)
	referenceReadDuration := r.now().Sub(referenceStartedAt)
	comparisons := compareFeatureSnapshots(
		request, candidate, reference, nil, referenceErr, r.now(),
		candidateReadDuration, referenceReadDuration,
	)
	if r.recorder != nil {
		_ = r.recorder.Record(ctx, comparisons)
	}
	return candidate, nil
}

func (r *OnlineMonitoredFeatureSnapshotReader) ReadAt(
	ctx context.Context,
	request SnapshotReadRequest,
) ([]FeatureSnapshot, error) {
	return r.reference.ReadAt(ctx, request)
}

func snapshotDifferences(candidate, reference FeatureSnapshot, featureKeys []string) []string {
	var differences []string
	if !candidate.AsOf.Equal(reference.AsOf) {
		differences = append(differences, "as_of")
	}
	if candidate.ContentFingerprint != reference.ContentFingerprint {
		differences = append(differences, "content_fingerprint")
	}
	if candidate.QualityStatus != reference.QualityStatus {
		differences = append(differences, "quality_status")
	}
	for _, key := range featureKeys {
		if !reflect.DeepEqual(candidate.Values[key], reference.Values[key]) {
			differences = append(differences, "value:"+key)
		}
		if candidate.Undefined[key] != reference.Undefined[key] {
			differences = append(differences, "undefined:"+key)
		}
	}
	sort.Strings(differences)
	return differences
}

func indexSnapshotsBySubject(snapshots []FeatureSnapshot) map[string]FeatureSnapshot {
	indexed := make(map[string]FeatureSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		indexed[subjectIdentity(snapshot.Subject)] = snapshot
	}
	return indexed
}

func validateCurrentSnapshot(snapshot FeatureSnapshot) error {
	if snapshot.FeatureSetKey == "" || snapshot.FeatureSetVersion <= 0 || snapshot.Subject.Type == "" ||
		len(snapshot.Subject.Keys) == 0 || snapshot.AsOf.IsZero() || snapshot.ContentFingerprint == "" {
		return fmt.Errorf("complete feature set, subject, as_of and fingerprint are required")
	}
	return nil
}

func validateCurrentReadRequest(request SnapshotReadRequest) error {
	if request.FeatureSetKey == "" || request.FeatureSetVersion <= 0 || len(request.Subjects) == 0 || request.Cutoff.IsZero() {
		return fmt.Errorf("feature set, subjects and cutoff are required")
	}
	return nil
}

func currentSnapshotIdentity(featureSetKey string, version int, subject Subject) string {
	return fmt.Sprintf("%s\x00%d\x00%s", featureSetKey, version, subjectIdentity(subject))
}

func projectFeatureSnapshot(snapshot FeatureSnapshot, featureKeys []string) FeatureSnapshot {
	projected := cloneFeatureSnapshot(snapshot)
	projected.Values = make(map[string]any, len(featureKeys))
	projected.Undefined = make(map[string]bool, len(featureKeys))
	for _, key := range featureKeys {
		if value, found := snapshot.Values[key]; found {
			projected.Values[key] = value
		}
		if snapshot.Undefined[key] {
			projected.Undefined[key] = true
		}
	}
	return projected
}

func cloneFeatureSnapshot(snapshot FeatureSnapshot) FeatureSnapshot {
	cloned := snapshot
	cloned.Subject = cloneSubject(snapshot.Subject)
	cloned.Values = make(map[string]any, len(snapshot.Values))
	for key, value := range snapshot.Values {
		cloned.Values[key] = value
	}
	cloned.Undefined = make(map[string]bool, len(snapshot.Undefined))
	for key, value := range snapshot.Undefined {
		cloned.Undefined[key] = value
	}
	return cloned
}

func cloneSubject(subject Subject) Subject {
	cloned := Subject{Type: subject.Type, Keys: make(map[string]string, len(subject.Keys))}
	for key, value := range subject.Keys {
		cloned.Keys[key] = value
	}
	return cloned
}
