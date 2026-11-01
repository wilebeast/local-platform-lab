package riskwarehouse

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestPlanBackfillUsesInclusiveStableHourlyPoints(t *testing.T) {
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	got, err := PlanBackfill(start, start.Add(2*time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Time{start, start.Add(time.Hour), start.Add(2 * time.Hour)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("points = %v, want %v", got, want)
	}
}

func TestBackfillCoordinatorStopsAtFailureAndResumesFromThatPoint(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	store := newMemoryBackfillStore()
	runner := &scriptedTimepointRunner{failures: map[time.Time]error{
		start.Add(time.Hour): errors.New("quality gate failed"),
	}}
	coordinator := NewBackfillCoordinator(store, runner)
	command := BackfillCommand{
		BackfillID: "backfill-coach-001", Start: start, End: start.Add(2 * time.Hour), Step: time.Hour,
	}

	first, err := coordinator.Run(ctx, command)
	if err == nil {
		t.Fatal("first run should stop at the failed timepoint")
	}
	if first.Status != BackfillFailed || first.SucceededCount != 1 || first.FailedCount != 1 || first.PendingCount != 1 {
		t.Fatalf("first summary = %+v", first)
	}
	if want := []time.Time{start, start.Add(time.Hour)}; !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("first calls = %v, want %v", runner.calls, want)
	}

	delete(runner.failures, start.Add(time.Hour))
	second, err := coordinator.Run(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != BackfillSucceeded || second.SucceededCount != 3 || second.FailedCount != 0 || second.PendingCount != 0 {
		t.Fatalf("recovery summary = %+v", second)
	}
	wantCalls := []time.Time{start, start.Add(time.Hour), start.Add(time.Hour), start.Add(2 * time.Hour)}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("all calls = %v, want %v", runner.calls, wantCalls)
	}
}

type memoryBackfillStore struct {
	points map[string]map[time.Time]BackfillPointStatus
}

func newMemoryBackfillStore() *memoryBackfillStore {
	return &memoryBackfillStore{points: make(map[string]map[time.Time]BackfillPointStatus)}
}

func (s *memoryBackfillStore) Start(_ context.Context, command BackfillCommand, points []time.Time) error {
	if _, exists := s.points[command.BackfillID]; !exists {
		s.points[command.BackfillID] = make(map[time.Time]BackfillPointStatus, len(points))
		for _, point := range points {
			s.points[command.BackfillID][point] = BackfillPending
		}
	}
	return nil
}

func (s *memoryBackfillStore) PointStatuses(_ context.Context, backfillID string) (map[time.Time]BackfillPointStatus, error) {
	result := make(map[time.Time]BackfillPointStatus, len(s.points[backfillID]))
	for point, status := range s.points[backfillID] {
		result[point] = status
	}
	return result, nil
}

func (s *memoryBackfillStore) SetPointStatus(_ context.Context, backfillID string, point time.Time, status BackfillPointStatus, _ string) error {
	s.points[backfillID][point] = status
	return nil
}

func (s *memoryBackfillStore) Finalize(_ context.Context, _ string, _ BackfillSummary) error {
	return nil
}

type scriptedTimepointRunner struct {
	failures map[time.Time]error
	calls    []time.Time
}

func (r *scriptedTimepointRunner) Run(_ context.Context, _ string, point time.Time) error {
	r.calls = append(r.calls, point)
	return r.failures[point]
}
