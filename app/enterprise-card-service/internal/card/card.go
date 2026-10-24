package card

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrIdempotencyConflict = errors.New("idempotency key belongs to another request")
var ErrAuthorizationNotReversible = errors.New("authorization cannot be reversed")
var ErrAuthorizationNotSettleable = errors.New("authorization cannot be settled")
var ErrSettlementNotRefundable = errors.New("settlement cannot be refunded")

type Card struct {
	ID                       string `json:"id"`
	OrganizationID           string `json:"organization_id"`
	HolderID                 string `json:"holder_id"`
	Alias                    string `json:"alias"`
	Status                   string `json:"status"`
	Currency                 string `json:"currency"`
	BalanceMinor             int64  `json:"balance_minor"`
	ProviderCardID           string `json:"provider_card_id,omitempty"`
	CreatedAt                int64  `json:"created_at"`
	providerStatusOccurredAt int64
}

type ChannelOperation struct {
	ID           string
	CardID       string
	Type         string
	Status       string
	AttemptCount int
}

type ApplyParams struct {
	IdempotencyKey string
	Card           Card
}

type Store interface {
	Apply(context.Context, ApplyParams) (Card, error)
	ListByOrganization(context.Context, string) ([]Card, error)
	ClaimOperation(context.Context, time.Time) (*ChannelOperation, error)
	CompleteOperation(context.Context, string, string, string, time.Time) error
	RetryOperation(context.Context, string, string, time.Time, bool) error
}

type memoryApplication struct {
	organizationID string
	holderID       string
	alias          string
	cardID         string
}

type MemoryStore struct {
	mu             sync.Mutex
	cards          map[string]Card
	applications   map[string]memoryApplication
	operations     map[string]ChannelOperation
	providerEvents map[string]ProviderEvent
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		cards:          make(map[string]Card),
		applications:   make(map[string]memoryApplication),
		operations:     make(map[string]ChannelOperation),
		providerEvents: make(map[string]ProviderEvent),
	}
}

func (s *MemoryStore) RecordProviderEvent(_ context.Context, event ProviderEvent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.providerEvents[event.ID]; exists {
		return true, nil
	}
	event.ProcessingStatus = "pending"
	s.providerEvents[event.ID] = event
	return false, nil
}

func (s *MemoryStore) ClaimProviderEvent(_ context.Context, _ time.Time) (*ProviderEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, event := range s.providerEvents {
		if event.ProcessingStatus != "pending" {
			continue
		}
		event.ProcessingStatus = "processing"
		event.AttemptCount++
		s.providerEvents[id] = event
		claimed := event
		return &claimed, nil
	}
	return nil, nil
}

func (s *MemoryStore) CompleteProviderEvent(_ context.Context, event ProviderEvent, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, item := range s.cards {
		if item.ProviderCardID == event.Data.ProviderCardID {
			if event.OccurredAt > item.providerStatusOccurredAt {
				item.Status = event.Data.Status
				item.providerStatusOccurredAt = event.OccurredAt
				s.cards[id] = item
				event.ProcessingResult = "applied"
			} else {
				event.ProcessingResult = "ignored_stale"
			}
			event.ProcessingStatus = "processed"
			s.providerEvents[event.ID] = event
			return nil
		}
	}
	event.ProcessingStatus = "dead_letter"
	event.ProcessingResult = "failed"
	s.providerEvents[event.ID] = event
	return nil
}

func (s *MemoryStore) Apply(_ context.Context, params ApplyParams) (Card, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.applications[params.IdempotencyKey]; ok {
		if existing.organizationID != params.Card.OrganizationID || existing.holderID != params.Card.HolderID || existing.alias != params.Card.Alias {
			return Card{}, ErrIdempotencyConflict
		}
		return s.cards[existing.cardID], nil
	}
	s.cards[params.Card.ID] = params.Card
	s.applications[params.IdempotencyKey] = memoryApplication{
		organizationID: params.Card.OrganizationID,
		holderID:       params.Card.HolderID,
		alias:          params.Card.Alias,
		cardID:         params.Card.ID,
	}
	operationID := "operation-" + params.Card.ID
	s.operations[operationID] = ChannelOperation{ID: operationID, CardID: params.Card.ID, Type: "apply_card", Status: "pending"}
	return params.Card, nil
}

func (s *MemoryStore) ListByOrganization(_ context.Context, organizationID string) ([]Card, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cards := make([]Card, 0)
	for _, item := range s.cards {
		if item.OrganizationID == organizationID {
			cards = append(cards, item)
		}
	}
	return cards, nil
}

func (s *MemoryStore) ClaimOperation(_ context.Context, _ time.Time) (*ChannelOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, operation := range s.operations {
		if operation.Status != "pending" && operation.Status != "retry" {
			continue
		}
		operation.Status = "processing"
		operation.AttemptCount++
		s.operations[id] = operation
		claimed := operation
		return &claimed, nil
	}
	return nil, nil
}

func (s *MemoryStore) CompleteOperation(_ context.Context, operationID, cardID, providerCardID string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	card := s.cards[cardID]
	card.Status = "active"
	card.ProviderCardID = providerCardID
	s.cards[cardID] = card
	operation := s.operations[operationID]
	operation.Status = "completed"
	s.operations[operationID] = operation
	return nil
}

func (s *MemoryStore) RetryOperation(_ context.Context, operationID, _ string, _ time.Time, dead bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation := s.operations[operationID]
	if dead {
		operation.Status = "dead_letter"
	} else {
		operation.Status = "retry"
	}
	s.operations[operationID] = operation
	return nil
}
