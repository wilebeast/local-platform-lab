package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memoryInbox struct {
	event        *Event
	processed    bool
	retryAt      time.Time
	deadLettered bool
	lastError    string
}

func (s *memoryInbox) Claim(context.Context, time.Time) (*Event, error) {
	if s.event == nil {
		return nil, nil
	}
	event := *s.event
	s.event = nil
	return &event, nil
}

func (s *memoryInbox) MarkProcessed(_ context.Context, _ int64, _ time.Time) error {
	s.processed = true
	return nil
}

func (s *memoryInbox) MarkRetry(_ context.Context, _ int64, next time.Time, reason string) error {
	s.retryAt = next
	s.lastError = reason
	return nil
}

func (s *memoryInbox) MarkDeadLetter(_ context.Context, _ int64, reason string) error {
	s.deadLettered = true
	s.lastError = reason
	return nil
}

type recordingProjector struct {
	events []Event
	err    error
}

func (p *recordingProjector) Apply(_ context.Context, event Event) error {
	p.events = append(p.events, event)
	return p.err
}

func TestProcessorProjectsEventAndMarksItProcessed(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	store := &memoryInbox{event: &Event{ID: 7, Domain: "card", AttemptCount: 1}}
	projector := &recordingProjector{}
	processor := NewProcessor(store, projector, func() time.Time { return now }, 3)

	processed, err := processor.ProcessNext(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessNext() processed=%t err=%v", processed, err)
	}
	if !store.processed || len(projector.events) != 1 {
		t.Fatalf("processed=%t projected=%d", store.processed, len(projector.events))
	}
}

func TestProcessorRetriesThenDeadLettersProjectionFailure(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	projector := &recordingProjector{err: errors.New("unsupported event version")}

	retryStore := &memoryInbox{event: &Event{ID: 8, Domain: "verification", AttemptCount: 1}}
	processed, err := NewProcessor(retryStore, projector, func() time.Time { return now }, 3).ProcessNext(context.Background())
	if err != nil || !processed || retryStore.retryAt.IsZero() || retryStore.deadLettered {
		t.Fatalf("retry result processed=%t err=%v retry=%v dead=%t", processed, err, retryStore.retryAt, retryStore.deadLettered)
	}

	deadStore := &memoryInbox{event: &Event{ID: 9, Domain: "verification", AttemptCount: 3}}
	processed, err = NewProcessor(deadStore, projector, func() time.Time { return now }, 3).ProcessNext(context.Background())
	if err != nil || !processed || !deadStore.deadLettered {
		t.Fatalf("dead-letter result processed=%t err=%v dead=%t", processed, err, deadStore.deadLettered)
	}
}
