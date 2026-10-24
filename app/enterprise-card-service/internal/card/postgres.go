package card

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

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

func (s *PostgresStore) Fund(ctx context.Context, p FundParams) (AccountBalance, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AccountBalance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO card_lab_accounts(card_id,currency) VALUES($1,$2) ON CONFLICT(card_id) DO NOTHING`, p.CardID, p.Currency); err != nil {
		return AccountBalance{}, err
	}
	command, err := tx.Exec(ctx, `INSERT INTO card_lab_ledger_entries(entry_id,card_id,entry_type,currency,amount_minor) VALUES($1,$2,'funding',$3,$4) ON CONFLICT(entry_id) DO NOTHING`, p.EntryID, p.CardID, p.Currency, p.AmountMinor)
	if err != nil {
		return AccountBalance{}, err
	}
	if command.RowsAffected() == 0 {
		var cardID, currency string
		var amount int64
		if err := tx.QueryRow(ctx, `SELECT card_id,currency,amount_minor FROM card_lab_ledger_entries WHERE entry_id=$1`, p.EntryID).Scan(&cardID, &currency, &amount); err != nil {
			return AccountBalance{}, err
		}
		if cardID != p.CardID || currency != p.Currency || amount != p.AmountMinor {
			return AccountBalance{}, ErrIdempotencyConflict
		}
	} else if _, err := tx.Exec(ctx, `UPDATE card_lab_accounts SET posted_balance_minor=posted_balance_minor+$2,updated_at=NOW() WHERE card_id=$1`, p.CardID, p.AmountMinor); err != nil {
		return AccountBalance{}, err
	}
	var result AccountBalance
	err = tx.QueryRow(ctx, `SELECT card_id,currency,posted_balance_minor,held_amount_minor,posted_balance_minor-held_amount_minor FROM card_lab_accounts WHERE card_id=$1`, p.CardID).Scan(&result.CardID, &result.Currency, &result.PostedBalanceMinor, &result.HeldAmountMinor, &result.AvailableBalanceMinor)
	if err != nil {
		return AccountBalance{}, err
	}
	return result, tx.Commit(ctx)
}

func (s *PostgresStore) Authorize(ctx context.Context, p AuthorizationParams) (AuthorizationResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AuthorizationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, p.AuthorizationID); err != nil {
		return AuthorizationResult{}, err
	}
	var existing AuthorizationResult
	var existingProvider, existingCurrency string
	var existingAmount int64
	err = tx.QueryRow(ctx, `SELECT provider_card_id,currency,amount_minor,decision,reason,posted_balance_after,held_amount_after,available_balance_after FROM card_lab_authorizations WHERE authorization_id=$1`, p.AuthorizationID).Scan(&existingProvider, &existingCurrency, &existingAmount, &existing.Decision, &existing.Reason, &existing.PostedBalanceMinor, &existing.HeldAmountMinor, &existing.AvailableBalanceMinor)
	if err == nil {
		if existingProvider != p.ProviderCardID || existingCurrency != p.Currency || existingAmount != p.AmountMinor {
			return AuthorizationResult{}, ErrIdempotencyConflict
		}
		existing.AuthorizationID = p.AuthorizationID
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationResult{}, err
	}
	var cardID, status string
	if err := tx.QueryRow(ctx, `SELECT id,status FROM card_lab_cards WHERE provider_card_id=$1`, p.ProviderCardID).Scan(&cardID, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			cardID = ""
			status = "missing"
		} else {
			return AuthorizationResult{}, err
		}
	}
	var account AccountBalance
	if cardID != "" {
		err = tx.QueryRow(ctx, `SELECT card_id,currency,posted_balance_minor,held_amount_minor,posted_balance_minor-held_amount_minor FROM card_lab_accounts WHERE card_id=$1 FOR UPDATE`, cardID).Scan(&account.CardID, &account.Currency, &account.PostedBalanceMinor, &account.HeldAmountMinor, &account.AvailableBalanceMinor)
		if errors.Is(err, pgx.ErrNoRows) {
			account = AccountBalance{CardID: cardID, Currency: p.Currency}
		} else if err != nil {
			return AuthorizationResult{}, err
		}
	}
	result := AuthorizationResult{AuthorizationID: p.AuthorizationID, Decision: "declined", PostedBalanceMinor: account.PostedBalanceMinor, HeldAmountMinor: account.HeldAmountMinor, AvailableBalanceMinor: account.AvailableBalanceMinor}
	switch {
	case status != "active":
		result.Reason = "card_not_active"
	case account.Currency != p.Currency:
		result.Reason = "currency_mismatch"
	case account.AvailableBalanceMinor < p.AmountMinor:
		result.Reason = "insufficient_funds"
	default:
		if _, err := tx.Exec(ctx, `UPDATE card_lab_accounts SET held_amount_minor=held_amount_minor+$2,updated_at=NOW() WHERE card_id=$1`, cardID, p.AmountMinor); err != nil {
			return AuthorizationResult{}, err
		}
		result.Decision = "approved"
		result.HeldAmountMinor += p.AmountMinor
		result.AvailableBalanceMinor -= p.AmountMinor
	}
	authorizationStatus := "authorized"
	if result.Decision != "approved" {
		authorizationStatus = "declined"
	}
	_, err = tx.Exec(ctx, `INSERT INTO card_lab_authorizations(authorization_id,provider_card_id,card_id,currency,amount_minor,decision,reason,posted_balance_after,held_amount_after,available_balance_after,authorization_status) VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,$11)`, p.AuthorizationID, p.ProviderCardID, cardID, p.Currency, p.AmountMinor, result.Decision, result.Reason, result.PostedBalanceMinor, result.HeldAmountMinor, result.AvailableBalanceMinor, authorizationStatus)
	if err != nil {
		return AuthorizationResult{}, err
	}
	return result, tx.Commit(ctx)
}

func (s *PostgresStore) ReverseAuthorization(ctx context.Context, p AuthorizationReversalParams) (AuthorizationReversalResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AuthorizationReversalResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, p.AuthorizationID); err != nil {
		return AuthorizationReversalResult{}, err
	}
	var existing AuthorizationReversalResult
	var existingAuthorizationID string
	err = tx.QueryRow(ctx, `SELECT authorization_id,released_amount_minor,posted_balance_after,held_amount_after,available_balance_after FROM card_lab_authorization_reversals WHERE reversal_id=$1`, p.ReversalID).Scan(&existingAuthorizationID, &existing.ReleasedAmountMinor, &existing.PostedBalanceMinor, &existing.HeldAmountMinor, &existing.AvailableBalanceMinor)
	if err == nil {
		if existingAuthorizationID != p.AuthorizationID {
			return AuthorizationReversalResult{}, ErrIdempotencyConflict
		}
		existing.ReversalID, existing.AuthorizationID, existing.Status = p.ReversalID, p.AuthorizationID, "reversed"
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationReversalResult{}, err
	}
	var cardID, decision, authorizationStatus string
	var amountMinor int64
	err = tx.QueryRow(ctx, `SELECT card_id,amount_minor,decision,authorization_status FROM card_lab_authorizations WHERE authorization_id=$1 FOR UPDATE`, p.AuthorizationID).Scan(&cardID, &amountMinor, &decision, &authorizationStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationReversalResult{}, ErrAuthorizationNotReversible
	}
	if err != nil {
		return AuthorizationReversalResult{}, err
	}
	if decision != "approved" || authorizationStatus != "authorized" {
		return AuthorizationReversalResult{}, ErrAuthorizationNotReversible
	}
	var result AuthorizationReversalResult
	result.ReversalID, result.AuthorizationID, result.Status, result.ReleasedAmountMinor = p.ReversalID, p.AuthorizationID, "reversed", amountMinor
	err = tx.QueryRow(ctx, `UPDATE card_lab_accounts SET held_amount_minor=held_amount_minor-$2,updated_at=NOW() WHERE card_id=$1 AND held_amount_minor >= $2 RETURNING posted_balance_minor,held_amount_minor,posted_balance_minor-held_amount_minor`, cardID, amountMinor).Scan(&result.PostedBalanceMinor, &result.HeldAmountMinor, &result.AvailableBalanceMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationReversalResult{}, ErrAuthorizationNotReversible
	}
	if err != nil {
		return AuthorizationReversalResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO card_lab_authorization_reversals(reversal_id,authorization_id,released_amount_minor,posted_balance_after,held_amount_after,available_balance_after) VALUES($1,$2,$3,$4,$5,$6)`, p.ReversalID, p.AuthorizationID, result.ReleasedAmountMinor, result.PostedBalanceMinor, result.HeldAmountMinor, result.AvailableBalanceMinor); err != nil {
		return AuthorizationReversalResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE card_lab_authorizations SET authorization_status='reversed' WHERE authorization_id=$1`, p.AuthorizationID); err != nil {
		return AuthorizationReversalResult{}, err
	}
	return result, tx.Commit(ctx)
}

func (s *PostgresStore) SettleAuthorization(ctx context.Context, p AuthorizationSettlementParams) (AuthorizationSettlementResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AuthorizationSettlementResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, p.AuthorizationID); err != nil {
		return AuthorizationSettlementResult{}, err
	}
	var existing AuthorizationSettlementResult
	var existingAuthorizationID string
	err = tx.QueryRow(ctx, `SELECT authorization_id,settled_amount_minor,posted_balance_after,held_amount_after,available_balance_after FROM card_lab_authorization_settlements WHERE settlement_id=$1`, p.SettlementID).Scan(&existingAuthorizationID, &existing.SettledAmountMinor, &existing.PostedBalanceMinor, &existing.HeldAmountMinor, &existing.AvailableBalanceMinor)
	if err == nil {
		if existingAuthorizationID != p.AuthorizationID {
			return AuthorizationSettlementResult{}, ErrIdempotencyConflict
		}
		existing.SettlementID, existing.AuthorizationID, existing.Status = p.SettlementID, p.AuthorizationID, "settled"
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationSettlementResult{}, err
	}
	var cardID, currency, decision, authorizationStatus string
	var amountMinor int64
	err = tx.QueryRow(ctx, `SELECT card_id,currency,amount_minor,decision,authorization_status FROM card_lab_authorizations WHERE authorization_id=$1 FOR UPDATE`, p.AuthorizationID).Scan(&cardID, &currency, &amountMinor, &decision, &authorizationStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationSettlementResult{}, ErrAuthorizationNotSettleable
	}
	if err != nil {
		return AuthorizationSettlementResult{}, err
	}
	if decision != "approved" || authorizationStatus != "authorized" {
		return AuthorizationSettlementResult{}, ErrAuthorizationNotSettleable
	}
	var result AuthorizationSettlementResult
	result.SettlementID, result.AuthorizationID, result.Status, result.SettledAmountMinor = p.SettlementID, p.AuthorizationID, "settled", amountMinor
	err = tx.QueryRow(ctx, `UPDATE card_lab_accounts SET posted_balance_minor=posted_balance_minor-$2,held_amount_minor=held_amount_minor-$2,updated_at=NOW() WHERE card_id=$1 AND posted_balance_minor >= $2 AND held_amount_minor >= $2 RETURNING posted_balance_minor,held_amount_minor,posted_balance_minor-held_amount_minor`, cardID, amountMinor).Scan(&result.PostedBalanceMinor, &result.HeldAmountMinor, &result.AvailableBalanceMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationSettlementResult{}, ErrAuthorizationNotSettleable
	}
	if err != nil {
		return AuthorizationSettlementResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO card_lab_ledger_entries(entry_id,card_id,entry_type,currency,amount_minor) VALUES($1,$2,'settlement',$3,$4)`, p.SettlementID, cardID, currency, amountMinor); err != nil {
		return AuthorizationSettlementResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO card_lab_authorization_settlements(settlement_id,authorization_id,settled_amount_minor,posted_balance_after,held_amount_after,available_balance_after) VALUES($1,$2,$3,$4,$5,$6)`, p.SettlementID, p.AuthorizationID, amountMinor, result.PostedBalanceMinor, result.HeldAmountMinor, result.AvailableBalanceMinor); err != nil {
		return AuthorizationSettlementResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE card_lab_authorizations SET authorization_status='settled' WHERE authorization_id=$1`, p.AuthorizationID); err != nil {
		return AuthorizationSettlementResult{}, err
	}
	return result, tx.Commit(ctx)
}

func (s *PostgresStore) RefundSettlement(ctx context.Context, p SettlementRefundParams) (SettlementRefundResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SettlementRefundResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, p.SettlementID); err != nil {
		return SettlementRefundResult{}, err
	}
	var existing SettlementRefundResult
	var existingSettlementID string
	err = tx.QueryRow(ctx, `SELECT settlement_id,refunded_amount_minor,posted_balance_after,held_amount_after,available_balance_after FROM card_lab_settlement_refunds WHERE refund_id=$1`, p.RefundID).Scan(&existingSettlementID, &existing.RefundedAmountMinor, &existing.PostedBalanceMinor, &existing.HeldAmountMinor, &existing.AvailableBalanceMinor)
	if err == nil {
		if existingSettlementID != p.SettlementID {
			return SettlementRefundResult{}, ErrIdempotencyConflict
		}
		existing.RefundID, existing.SettlementID, existing.Status = p.RefundID, p.SettlementID, "refunded"
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SettlementRefundResult{}, err
	}
	var authorizationID string
	var amountMinor int64
	err = tx.QueryRow(ctx, `SELECT authorization_id,settled_amount_minor FROM card_lab_authorization_settlements WHERE settlement_id=$1`, p.SettlementID).Scan(&authorizationID, &amountMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return SettlementRefundResult{}, ErrSettlementNotRefundable
	}
	if err != nil {
		return SettlementRefundResult{}, err
	}
	var cardID, currency, authorizationStatus string
	err = tx.QueryRow(ctx, `SELECT card_id,currency,authorization_status FROM card_lab_authorizations WHERE authorization_id=$1 FOR UPDATE`, authorizationID).Scan(&cardID, &currency, &authorizationStatus)
	if err != nil {
		return SettlementRefundResult{}, err
	}
	if authorizationStatus != "settled" {
		return SettlementRefundResult{}, ErrSettlementNotRefundable
	}
	var result SettlementRefundResult
	result.RefundID, result.SettlementID, result.Status, result.RefundedAmountMinor = p.RefundID, p.SettlementID, "refunded", amountMinor
	err = tx.QueryRow(ctx, `UPDATE card_lab_accounts SET posted_balance_minor=posted_balance_minor+$2,updated_at=NOW() WHERE card_id=$1 RETURNING posted_balance_minor,held_amount_minor,posted_balance_minor-held_amount_minor`, cardID, amountMinor).Scan(&result.PostedBalanceMinor, &result.HeldAmountMinor, &result.AvailableBalanceMinor)
	if err != nil {
		return SettlementRefundResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO card_lab_ledger_entries(entry_id,card_id,entry_type,currency,amount_minor) VALUES($1,$2,'refund',$3,$4)`, p.RefundID, cardID, currency, amountMinor); err != nil {
		return SettlementRefundResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO card_lab_settlement_refunds(refund_id,settlement_id,refunded_amount_minor,posted_balance_after,held_amount_after,available_balance_after) VALUES($1,$2,$3,$4,$5,$6)`, p.RefundID, p.SettlementID, amountMinor, result.PostedBalanceMinor, result.HeldAmountMinor, result.AvailableBalanceMinor); err != nil {
		return SettlementRefundResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE card_lab_authorizations SET authorization_status='refunded' WHERE authorization_id=$1`, authorizationID); err != nil {
		return SettlementRefundResult{}, err
	}
	return result, tx.Commit(ctx)
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
