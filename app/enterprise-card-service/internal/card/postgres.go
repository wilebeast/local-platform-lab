package card

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/enterprise-card-service/internal/ledger"
)

type PostgresStore struct {
	pool   *pgxpool.Pool
	ledger *ledger.PostgresStore
	now    func() time.Time
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, ledger: ledger.NewPostgresStore(pool), now: time.Now}
}

func (s *PostgresStore) Apply(ctx context.Context, params ApplyParams) (Card, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Card{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		INSERT INTO card_lab_applications (idempotency_key, organization_id, holder_id, alias, card_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		params.IdempotencyKey, params.Card.OrganizationID, params.Card.HolderID, params.Card.Alias, params.Card.ID)
	if err != nil {
		return Card{}, err
	}
	if command.RowsAffected() == 0 {
		var existing memoryApplication
		err = tx.QueryRow(ctx, `SELECT organization_id, holder_id, alias, card_id
			FROM card_lab_applications WHERE idempotency_key = $1 FOR UPDATE`, params.IdempotencyKey).
			Scan(&existing.organizationID, &existing.holderID, &existing.alias, &existing.cardID)
		if err != nil {
			return Card{}, err
		}
		if existing.organizationID != params.Card.OrganizationID || existing.holderID != params.Card.HolderID || existing.alias != params.Card.Alias {
			return Card{}, ErrIdempotencyConflict
		}
		card, err := getCard(ctx, tx, existing.cardID)
		if err != nil {
			return Card{}, err
		}
		return card, tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `INSERT INTO card_lab_cards
		(id, organization_id, holder_id, alias, status, currency, balance_minor, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		params.Card.ID, params.Card.OrganizationID, params.Card.HolderID, params.Card.Alias,
		params.Card.Status, params.Card.Currency, params.Card.BalanceMinor, params.Card.CreatedAt)
	if err != nil {
		return Card{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO card_lab_channel_operations
		(operation_id, card_id, operation_type, status, idempotency_key, created_at)
		VALUES ($1, $2, 'apply_card', 'pending', $3, $4)`,
		"operation-"+params.Card.ID, params.Card.ID, params.IdempotencyKey, params.Card.CreatedAt)
	if err != nil {
		return Card{}, err
	}
	return params.Card, tx.Commit(ctx)
}

func (s *PostgresStore) ListByOrganization(ctx context.Context, organizationID string) ([]Card, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, organization_id, holder_id, alias, status, currency, balance_minor, provider_card_id, created_at
		FROM card_lab_cards WHERE organization_id = $1 ORDER BY created_at, id`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cards := make([]Card, 0)
	for rows.Next() {
		var card Card
		if err := rows.Scan(&card.ID, &card.OrganizationID, &card.HolderID, &card.Alias, &card.Status, &card.Currency, &card.BalanceMinor, &card.ProviderCardID, &card.CreatedAt); err != nil {
			return nil, err
		}
		cards = append(cards, card)
	}
	return cards, rows.Err()
}

func (s *PostgresStore) ClaimOperation(ctx context.Context, now time.Time) (*ChannelOperation, error) {
	row := s.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT operation_id FROM card_lab_channel_operations
		WHERE (status IN ('pending', 'retry') AND next_attempt_at <= $1)
		   OR (status = 'processing' AND updated_at < $1 - INTERVAL '1 minute')
		ORDER BY created_at, operation_id
		FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE card_lab_channel_operations operation
	SET status = 'processing', attempt_count = attempt_count + 1, updated_at = $1
	FROM candidate WHERE operation.operation_id = candidate.operation_id
	RETURNING operation.operation_id, operation.card_id, operation.operation_type, operation.status, operation.attempt_count`, now)
	var operation ChannelOperation
	if err := row.Scan(&operation.ID, &operation.CardID, &operation.Type, &operation.Status, &operation.AttemptCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &operation, nil
}

func (s *PostgresStore) CompleteOperation(ctx context.Context, operationID, cardID, providerCardID string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE card_lab_cards SET status='active', provider_card_id=$2, updated_at=$3 WHERE id=$1`, cardID, providerCardID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE card_lab_channel_operations SET status='completed', last_error=NULL, updated_at=$2 WHERE operation_id=$1`, operationID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) RetryOperation(ctx context.Context, operationID, reason string, next time.Time, dead bool) error {
	status := "retry"
	if dead {
		status = "dead_letter"
	}
	_, err := s.pool.Exec(ctx, `UPDATE card_lab_channel_operations
		SET status=$2, next_attempt_at=$3, last_error=$4, updated_at=NOW() WHERE operation_id=$1`, operationID, status, next, reason)
	return err
}

func (s *PostgresStore) RecordProviderEvent(ctx context.Context, event ProviderEvent) (bool, error) {
	command, err := s.pool.Exec(ctx, `INSERT INTO card_lab_provider_events
		(event_id, event_type, event_version, provider_card_id, target_status, raw_body, processing_status, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,'pending',$7) ON CONFLICT (event_id) DO NOTHING`,
		event.ID, event.Type, event.Version, event.Data.ProviderCardID, event.Data.Status, event.RawBody, event.OccurredAt)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 0, nil
}

func (s *PostgresStore) ClaimProviderEvent(ctx context.Context, now time.Time) (*ProviderEvent, error) {
	row := s.pool.QueryRow(ctx, `WITH candidate AS (
		SELECT event_id FROM card_lab_provider_events WHERE processing_status='pending'
		ORDER BY received_at, event_id FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE card_lab_provider_events event
	SET processing_status='processing', attempt_count=attempt_count+1
	FROM candidate WHERE event.event_id=candidate.event_id
	RETURNING event.event_id,event.event_type,event.event_version,event.provider_card_id,event.target_status,event.occurred_at,event.processing_status,event.attempt_count`)
	var event ProviderEvent
	if err := row.Scan(&event.ID, &event.Type, &event.Version, &event.Data.ProviderCardID, &event.Data.Status, &event.OccurredAt, &event.ProcessingStatus, &event.AttemptCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &event, nil
}

func (s *PostgresStore) CompleteProviderEvent(ctx context.Context, event ProviderEvent, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE card_lab_cards
		SET status=$2, provider_status_occurred_at=$4, provider_status_event_id=$5, updated_at=$3
		WHERE provider_card_id=$1 AND $4 > provider_status_occurred_at`,
		event.Data.ProviderCardID, event.Data.Status, now, event.OccurredAt, event.ID)
	if err != nil {
		return err
	}
	status, result, lastError := "processed", "applied", any(nil)
	if command.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM card_lab_cards WHERE provider_card_id=$1)`, event.Data.ProviderCardID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			result = "ignored_stale"
		} else {
			status, result, lastError = "dead_letter", "failed", "provider card not found"
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE card_lab_provider_events SET processing_status=$2,processing_result=$3,processed_at=$4,last_error=$5 WHERE event_id=$1`, event.ID, status, result, now, lastError); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getCard(ctx context.Context, query rowQuerier, cardID string) (Card, error) {
	var card Card
	err := query.QueryRow(ctx, `SELECT id, organization_id, holder_id, alias, status, currency, balance_minor, provider_card_id, created_at
		FROM card_lab_cards WHERE id = $1`, cardID).
		Scan(&card.ID, &card.OrganizationID, &card.HolderID, &card.Alias, &card.Status, &card.Currency, &card.BalanceMinor, &card.ProviderCardID, &card.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Card{}, errors.New("card application exists without a card")
	}
	return card, err
}
