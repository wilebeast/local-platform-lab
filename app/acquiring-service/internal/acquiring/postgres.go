package acquiring

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var acquiringSchema string

type PostgresPaymentStore struct{ pool *pgxpool.Pool }

func NewPostgresPaymentStore(pool *pgxpool.Pool) *PostgresPaymentStore {
	return &PostgresPaymentStore{pool: pool}
}

func (s *PostgresPaymentStore) ProcessNextProviderEvent(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var inboxID int64
	var provider, eventID, eventType, providerRequestID string
	var occurredAt int64
	err = tx.QueryRow(ctx, `SELECT inbox_id,provider,event_id,event_type,provider_request_id,occurred_at
		FROM acquiring_provider_inbox WHERE processing_status='pending'
		ORDER BY inbox_id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&inboxID, &provider, &eventID, &eventType, &providerRequestID, &occurredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE acquiring_provider_inbox SET processing_status='processing',attempt_count=attempt_count+1 WHERE inbox_id=$1`, inboxID); err != nil {
		return false, err
	}
	var operationID, attemptID, paymentID, merchantID, currency, operationStatus string
	var amount, fee int64
	err = tx.QueryRow(ctx, `SELECT c.operation_id,c.attempt_id,a.payment_id,o.merchant_id,o.currency,o.amount_minor,o.fee_minor,c.status
		FROM acquiring_channel_operations c
		JOIN acquiring_payment_attempts a ON a.attempt_id=c.attempt_id
		JOIN acquiring_payment_orders o ON o.payment_id=a.payment_id
		WHERE c.provider=$1 AND c.provider_request_id=$2 FOR UPDATE OF c,a,o`, provider, providerRequestID).
		Scan(&operationID, &attemptID, &paymentID, &merchantID, &currency, &amount, &fee, &operationStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		_, updateErr := tx.Exec(ctx, `UPDATE acquiring_provider_inbox SET processing_status='dead_letter',last_error='channel operation not found',processed_at=NOW() WHERE inbox_id=$1`, inboxID)
		if updateErr != nil {
			return false, updateErr
		}
		return true, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	if eventType != "payment.succeeded" {
		if _, err = tx.Exec(ctx, `UPDATE acquiring_payment_attempts SET status='failed',updated_at=NOW() WHERE attempt_id=$1`, attemptID); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE acquiring_channel_operations SET status='failed',last_error='provider reported payment failure',updated_at=NOW() WHERE operation_id=$1`, operationID); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE acquiring_provider_inbox SET processing_status='processed',processed_at=NOW() WHERE inbox_id=$1`, inboxID); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	if operationStatus == "succeeded" {
		_, err = tx.Exec(ctx, `UPDATE acquiring_provider_inbox SET processing_status='processed',processed_at=NOW() WHERE inbox_id=$1`, inboxID)
		if err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	journal, err := CaptureJournal(CaptureCommand{PaymentID: paymentID, Currency: currency, AmountMinor: amount, FeeMinor: fee})
	if err != nil {
		return false, err
	}
	accountIDs, err := ensureAcquiringAccounts(ctx, tx, provider, merchantID, currency)
	if err != nil {
		return false, err
	}
	journalID := stablePaymentID("journal-capture", paymentID)
	fingerprint := paymentFingerprint(CreatePaymentCommand{MerchantID: merchantID, MerchantOrder: paymentID, Currency: currency, AmountMinor: amount, PaymentMethod: provider})
	if err := postAcquiringJournal(ctx, tx, journalID, journal, fingerprint, occurredAt, accountIDs); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE acquiring_payment_orders SET status='succeeded',updated_at=NOW() WHERE payment_id=$1`, paymentID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE acquiring_payment_attempts SET status='succeeded',updated_at=NOW() WHERE attempt_id=$1`, attemptID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE acquiring_channel_operations SET status='succeeded',updated_at=NOW() WHERE operation_id=$1`, operationID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE acquiring_provider_inbox SET processing_status='processed',processed_at=NOW() WHERE inbox_id=$1`, inboxID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func ensureAcquiringAccounts(ctx context.Context, tx pgx.Tx, provider, merchantID, currency string) (map[string]string, error) {
	type account struct{ logical, id, key, group, accountType, normalSide, ownerType, ownerID, purpose string }
	accounts := []account{
		{"channel_receivable", stablePaymentID("account-receivable", provider+":"+currency), "provider." + provider + "." + currency + ".receivable", "provider-" + provider, "asset", "debit", "provider", provider, "receivable"},
		{"merchant_payable", stablePaymentID("account-payable", merchantID+":"+currency), "merchant." + merchantID + "." + currency + ".payable", "merchant-" + merchantID, "liability", "credit", "organization", merchantID, "payable"},
		{"fee_revenue", stablePaymentID("account-fee", currency), "platform." + currency + ".fee-revenue", "platform-fee", "revenue", "credit", "platform", "platform-local", "fee"},
		{"platform_cash", stablePaymentID("account-cash", currency), "platform." + currency + ".cash", "platform-cash", "asset", "debit", "platform", "platform-local", "cash"},
		{"provider_refund_payable", stablePaymentID("account-refund-payable", provider+":"+currency), "provider." + provider + "." + currency + ".refund-payable", "provider-" + provider, "liability", "credit", "provider", provider, "payable"},
	}
	result := make(map[string]string, len(accounts))
	for _, a := range accounts {
		_, err := tx.Exec(ctx, `INSERT INTO ledger_accounts(id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status)
			VALUES($1,'book-acquiring-local',$2,$3,$4,$5,$6,$7,$8,$9,'active') ON CONFLICT(book_id,account_key) DO NOTHING`,
			a.id, a.key, a.group, a.accountType, a.normalSide, a.ownerType, a.ownerID, currency, a.purpose)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_balances(account_id) VALUES($1) ON CONFLICT(account_id) DO NOTHING`, a.id); err != nil {
			return nil, err
		}
		result[a.logical] = a.id
	}
	return result, nil
}

func postAcquiringJournal(ctx context.Context, tx pgx.Tx, journalID string, journal Journal, fingerprint string, effectiveAt int64, accountIDs map[string]string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_journals(id,book_id,business_type,business_id,event_type,currency,status,command_fingerprint,effective_at)
		VALUES($1,'book-acquiring-local',$2,$3,$4,$5,'draft',$6,$7)`, journalID, journal.BusinessType, journal.BusinessID, journal.EventType, journal.Currency, fingerprint, effectiveAt); err != nil {
		return err
	}
	accountOrder := make([]string, 0, len(journal.Postings))
	for _, posting := range journal.Postings {
		accountOrder = append(accountOrder, accountIDs[posting.Account])
	}
	sort.Strings(accountOrder)
	for _, accountID := range accountOrder {
		if _, err := tx.Exec(ctx, `SELECT id FROM ledger_accounts WHERE id=$1 FOR UPDATE`, accountID); err != nil {
			return err
		}
	}
	for index, posting := range journal.Postings {
		accountID := accountIDs[posting.Account]
		if accountID == "" {
			return fmt.Errorf("ledger account mapping missing for %s", posting.Account)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_postings(journal_id,sequence_no,account_id,side,amount_minor,currency) VALUES($1,$2,$3,$4,$5,$6)`,
			journalID, index+1, accountID, posting.Side, posting.Amount, journal.Currency); err != nil {
			return err
		}
		column := "posted_debits_minor"
		if posting.Side == Credit {
			column = "posted_credits_minor"
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE ledger_balances SET %s=%s+$1,version=version+1,updated_at=NOW() WHERE account_id=$2`, column, column), posting.Amount, accountID); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, `UPDATE ledger_journals SET status='posted',posted_at=$1 WHERE id=$2`, time.Now().Unix(), journalID)
	return err
}

func EnsureAcquiringSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, acquiringSchema)
	return err
}

func (s *PostgresPaymentStore) CreatePayment(ctx context.Context, command CreatePaymentCommand) (CreatePaymentResult, error) {
	if err := validateCreatePayment(command); err != nil {
		return CreatePaymentResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreatePaymentResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	scope := command.MerchantID + ":" + command.IdempotencyKey
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, scope); err != nil {
		return CreatePaymentResult{}, err
	}
	fingerprint := paymentFingerprint(command)
	existing, storedFingerprint, err := loadPaymentByIdempotency(ctx, tx, command.MerchantID, command.IdempotencyKey)
	if err == nil {
		if storedFingerprint != fingerprint {
			return CreatePaymentResult{}, ErrPaymentIdempotencyConflict
		}
		existing.Replayed = true
		if err := tx.Commit(ctx); err != nil {
			return CreatePaymentResult{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CreatePaymentResult{}, err
	}

	paymentID := stablePaymentID("pay", scope)
	attemptID := stablePaymentID("attempt", paymentID+":"+command.PaymentMethod)
	operationID := stablePaymentID("operation", attemptID)
	provider := routeProvider(command.PaymentMethod)
	providerRequestID := stablePaymentID("provider-request", operationID)
	_, err = tx.Exec(ctx, `INSERT INTO acquiring_payment_orders(
		payment_id,merchant_id,merchant_order_id,idempotency_key,command_fingerprint,
		currency,amount_minor,fee_minor,merchant_net_minor,status
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'created')`,
		paymentID, command.MerchantID, command.MerchantOrder, command.IdempotencyKey, fingerprint,
		command.Currency, command.AmountMinor, fixedFeeMinor, command.AmountMinor-fixedFeeMinor)
	if isUniqueViolation(err) {
		return CreatePaymentResult{}, ErrPaymentIdempotencyConflict
	}
	if err != nil {
		return CreatePaymentResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO acquiring_payment_attempts(
		attempt_id,payment_id,payment_method,provider,status
	) VALUES($1,$2,$3,$4,'created')`, attemptID, paymentID, command.PaymentMethod, provider); err != nil {
		return CreatePaymentResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO acquiring_channel_operations(
		operation_id,attempt_id,provider,provider_request_id,status
	) VALUES($1,$2,$3,$4,'pending')`, operationID, attemptID, provider, providerRequestID); err != nil {
		return CreatePaymentResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreatePaymentResult{}, err
	}
	return CreatePaymentResult{
		Order: PaymentOrder{ID: paymentID, MerchantID: command.MerchantID, MerchantOrderID: command.MerchantOrder,
			Currency: command.Currency, AmountMinor: command.AmountMinor, FeeMinor: fixedFeeMinor,
			MerchantNetMinor: command.AmountMinor - fixedFeeMinor, Status: OrderCreated},
		Attempt:     PaymentAttempt{ID: attemptID, PaymentID: paymentID, PaymentMethod: command.PaymentMethod, Provider: provider, Status: AttemptCreated},
		Operation:   ChannelOperation{ID: operationID, AttemptID: attemptID, Provider: provider, ProviderReqID: providerRequestID, Status: OperationPending},
		CheckoutURL: "/checkout/" + paymentID,
	}, nil
}

func (s *PostgresPaymentStore) Receive(ctx context.Context, event ProviderEvent) (bool, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO acquiring_provider_inbox(
		provider,event_id,event_type,provider_request_id,occurred_at,raw_body
	) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(provider,event_id) DO NOTHING`,
		event.Provider, event.ID, event.Type, event.ProviderRequestID, event.OccurredAt, event.RawBody)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 0, nil
}

func loadPaymentByIdempotency(ctx context.Context, tx pgx.Tx, merchantID, idempotencyKey string) (CreatePaymentResult, string, error) {
	var result CreatePaymentResult
	var fingerprint string
	err := tx.QueryRow(ctx, `SELECT
		o.payment_id,o.merchant_id,o.merchant_order_id,o.currency,o.amount_minor,o.fee_minor,o.merchant_net_minor,o.status,o.command_fingerprint,
		a.attempt_id,a.payment_method,a.provider,a.status,
		c.operation_id,c.provider_request_id,c.status
	FROM acquiring_payment_orders o
	JOIN acquiring_payment_attempts a ON a.payment_id=o.payment_id
	JOIN acquiring_channel_operations c ON c.attempt_id=a.attempt_id
	WHERE o.merchant_id=$1 AND o.idempotency_key=$2`, merchantID, idempotencyKey).Scan(
		&result.Order.ID, &result.Order.MerchantID, &result.Order.MerchantOrderID, &result.Order.Currency,
		&result.Order.AmountMinor, &result.Order.FeeMinor, &result.Order.MerchantNetMinor, &result.Order.Status, &fingerprint,
		&result.Attempt.ID, &result.Attempt.PaymentMethod, &result.Attempt.Provider, &result.Attempt.Status,
		&result.Operation.ID, &result.Operation.ProviderReqID, &result.Operation.Status,
	)
	if err != nil {
		return CreatePaymentResult{}, "", err
	}
	result.Attempt.PaymentID = result.Order.ID
	result.Operation.AttemptID = result.Attempt.ID
	result.Operation.Provider = result.Attempt.Provider
	result.CheckoutURL = "/checkout/" + result.Order.ID
	return result, fingerprint, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
