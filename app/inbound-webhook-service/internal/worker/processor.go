package worker

import (
	"context"
	"time"
)

type Event struct {
	ID              int64
	IntegrationCode string
	Domain          string
	ExternalEventID string
	EventType       string
	EventVersion    int
	OccurredAt      int64
	RawBody         []byte
	AttemptCount    int
}

type Inbox interface {
	Claim(ctx context.Context, now time.Time) (*Event, error)
	MarkProcessed(ctx context.Context, id int64, processedAt time.Time) error
	MarkRetry(ctx context.Context, id int64, nextAttemptAt time.Time, reason string) error
	MarkDeadLetter(ctx context.Context, id int64, reason string) error
}

type Projector interface {
	Apply(ctx context.Context, event Event) error
}

type Processor struct {
	inbox       Inbox
	projector   Projector
	now         func() time.Time
	maxAttempts int
}

func NewProcessor(inbox Inbox, projector Projector, now func() time.Time, maxAttempts int) *Processor {
	return &Processor{inbox: inbox, projector: projector, now: now, maxAttempts: maxAttempts}
}

func (p *Processor) ProcessNext(ctx context.Context) (bool, error) {
	now := p.now()
	event, err := p.inbox.Claim(ctx, now)
	if err != nil || event == nil {
		return false, err
	}
	if err := p.projector.Apply(ctx, *event); err != nil {
		if event.AttemptCount >= p.maxAttempts {
			return true, p.inbox.MarkDeadLetter(ctx, event.ID, err.Error())
		}
		return true, p.inbox.MarkRetry(ctx, event.ID, now.Add(retryDelay(event.AttemptCount)), err.Error())
	}
	return true, p.inbox.MarkProcessed(ctx, event.ID, now)
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return time.Duration(1<<attempt) * time.Second
}
