package card

import (
	"context"
	"time"
)

type ProviderApplyRequest struct {
	CardID string `json:"card_id"`
}

type ProviderApplyResult struct {
	ProviderCardID string `json:"provider_card_id"`
}

type Provider interface {
	ApplyCard(context.Context, ProviderApplyRequest) (ProviderApplyResult, error)
}

type Processor struct {
	store       Store
	provider    Provider
	now         func() time.Time
	maxAttempts int
}

func NewProcessor(store Store, provider Provider, now func() time.Time, maxAttempts int) *Processor {
	return &Processor{store: store, provider: provider, now: now, maxAttempts: maxAttempts}
}

func (p *Processor) ProcessNext(ctx context.Context) (bool, error) {
	now := p.now()
	operation, err := p.store.ClaimOperation(ctx, now)
	if err != nil || operation == nil {
		return false, err
	}
	result, err := p.provider.ApplyCard(ctx, ProviderApplyRequest{CardID: operation.CardID})
	if err != nil {
		dead := operation.AttemptCount >= p.maxAttempts
		return true, p.store.RetryOperation(ctx, operation.ID, err.Error(), now.Add(retryDelay(operation.AttemptCount)), dead)
	}
	return true, p.store.CompleteOperation(ctx, operation.ID, operation.CardID, result.ProviderCardID, now)
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
