package card

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	ledgerpkg "github.com/wilebeast/local-platform-lab/app/enterprise-card-service/internal/ledger"
)

const localLedgerBookID = "book-local"

type fundingAccounts struct {
	BindingID          string
	AvailableID        string
	HeldID             string
	PlatformCashID     string
	ProviderPayableID  string
	ProviderReceivable string
	Currency           string
	AvailableStatus    string
	SpendingLimitMinor int64
}

type OperationalLedgerReconciliation struct {
	Healthy                     bool  `json:"healthy"`
	UnbalancedJournals          int64 `json:"unbalanced_journals"`
	BalanceProjectionMismatches int64 `json:"balance_projection_mismatches"`
	AuthorizationsMissingHold   int64 `json:"authorizations_missing_hold_journal"`
	ReversalsMissingJournal     int64 `json:"reversals_missing_journal"`
	SettlementsMissingJournal   int64 `json:"settlements_missing_journal"`
	RefundsMissingJournal       int64 `json:"refunds_missing_journal"`
	FailedMigrations            int64 `json:"failed_migrations"`
}

func (s *PostgresStore) ReconcileOperationalLedger(ctx context.Context) (OperationalLedgerReconciliation, error) {
	var report OperationalLedgerReconciliation
	queries := []struct {
		destination *int64
		query       string
	}{
		{&report.UnbalancedJournals, `SELECT COUNT(*) FROM (
			SELECT j.id
			FROM ledger_journals j
			LEFT JOIN ledger_postings p ON p.journal_id=j.id
			WHERE j.status='posted'
			GROUP BY j.id
			HAVING COUNT(p.id) < 2 OR
			       COALESCE(SUM(p.amount_minor) FILTER (WHERE p.side='debit'),0) <>
			       COALESCE(SUM(p.amount_minor) FILTER (WHERE p.side='credit'),0)
		) invalid`},
		{&report.BalanceProjectionMismatches, `WITH posting_totals AS (
			SELECT p.account_id,
			       COALESCE(SUM(p.amount_minor) FILTER (WHERE p.side='debit'),0) AS debits,
			       COALESCE(SUM(p.amount_minor) FILTER (WHERE p.side='credit'),0) AS credits
			FROM ledger_postings p
			JOIN ledger_journals j ON j.id=p.journal_id AND j.status='posted'
			GROUP BY p.account_id
		)
		SELECT COUNT(*)
		FROM ledger_accounts a
		LEFT JOIN ledger_balances b ON b.account_id=a.id
		LEFT JOIN posting_totals p ON p.account_id=a.id
		WHERE COALESCE(b.posted_debits_minor,0) <> COALESCE(p.debits,0)
		   OR COALESCE(b.posted_credits_minor,0) <> COALESCE(p.credits,0)`},
		{&report.AuthorizationsMissingHold, `SELECT COUNT(*)
			FROM card_lab_authorizations a
			LEFT JOIN ledger_journals j ON j.id=a.hold_journal_id AND j.status='posted'
			WHERE a.ledger_version=1 AND a.decision='approved' AND j.id IS NULL`},
		{&report.ReversalsMissingJournal, `SELECT COUNT(*)
			FROM card_lab_authorization_reversals r
			LEFT JOIN ledger_journals j ON j.id=r.release_journal_id AND j.status='posted'
			WHERE r.ledger_version=1 AND j.id IS NULL`},
		{&report.SettlementsMissingJournal, `SELECT COUNT(*)
			FROM card_lab_authorization_settlements s
			LEFT JOIN ledger_journals j ON j.id=s.settlement_journal_id AND j.status='posted'
			WHERE s.ledger_version=1 AND j.id IS NULL`},
		{&report.RefundsMissingJournal, `SELECT COUNT(*)
			FROM card_lab_settlement_refunds r
			LEFT JOIN ledger_journals j ON j.id=r.refund_journal_id AND j.status='posted'
			WHERE r.ledger_version=1 AND j.id IS NULL`},
		{&report.FailedMigrations, `SELECT COUNT(*) FROM ledger_migrations WHERE status='failed'`},
	}
	for _, check := range queries {
		if err := s.pool.QueryRow(ctx, check.query).Scan(check.destination); err != nil {
			return OperationalLedgerReconciliation{}, err
		}
	}
	report.Healthy = report.UnbalancedJournals == 0 && report.BalanceProjectionMismatches == 0 &&
		report.AuthorizationsMissingHold == 0 && report.ReversalsMissingJournal == 0 &&
		report.SettlementsMissingJournal == 0 &&
		report.RefundsMissingJournal == 0 && report.FailedMigrations == 0
	return report, nil
}

func (s *PostgresStore) Fund(ctx context.Context, params FundParams) (AccountBalance, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AccountBalance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	accounts, err := s.ensureCardFundingAccounts(ctx, tx, params.CardID)
	if err != nil {
		return AccountBalance{}, err
	}
	if accounts.Currency != params.Currency {
		return AccountBalance{}, ErrIdempotencyConflict
	}
	_, err = s.ledger.PostJournalInTx(ctx, tx, ledgerpkg.PostJournalCommand{
		JournalID:    stableLedgerID("journal-funding", params.EntryID),
		BookID:       localLedgerBookID,
		BusinessType: "card_funding",
		BusinessID:   params.EntryID,
		EventType:    "funding",
		Currency:     params.Currency,
		EffectiveAt:  s.now().Unix(),
		Postings: []ledgerpkg.Posting{
			{AccountID: accounts.PlatformCashID, Side: ledgerpkg.Debit, AmountMinor: params.AmountMinor},
			{AccountID: accounts.AvailableID, Side: ledgerpkg.Credit, AmountMinor: params.AmountMinor},
		},
	})
	if errors.Is(err, ledgerpkg.ErrIdempotencyConflict) {
		return AccountBalance{}, ErrIdempotencyConflict
	}
	if err != nil {
		return AccountBalance{}, err
	}
	result, err := loadCardLedgerBalance(ctx, tx, params.CardID, accounts)
	if err != nil {
		return AccountBalance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AccountBalance{}, err
	}
	return result, nil
}

func (s *PostgresStore) MigrateCardLedger(ctx context.Context, cardID string) (AccountBalance, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AccountBalance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	accounts, err := s.ensureCardFundingAccounts(ctx, tx, cardID)
	if err != nil {
		return AccountBalance{}, err
	}
	result, err := loadCardLedgerBalance(ctx, tx, cardID, accounts)
	if err != nil {
		return AccountBalance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AccountBalance{}, err
	}
	return result, nil
}

func (s *PostgresStore) Authorize(ctx context.Context, params AuthorizationParams) (AuthorizationResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AuthorizationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, params.AuthorizationID); err != nil {
		return AuthorizationResult{}, err
	}

	if existing, found, err := loadExistingAuthorization(ctx, tx, params); err != nil {
		return AuthorizationResult{}, err
	} else if found {
		if err := tx.Commit(ctx); err != nil {
			return AuthorizationResult{}, err
		}
		return existing, nil
	}

	var cardID, cardStatus, cardCurrency string
	err = tx.QueryRow(ctx, `SELECT id,status,currency FROM card_lab_cards WHERE provider_card_id=$1 FOR UPDATE`, params.ProviderCardID).
		Scan(&cardID, &cardStatus, &cardCurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.recordDeclinedAuthorization(ctx, tx, params, "", "card_not_active", fundingAccounts{Currency: params.Currency})
	}
	if err != nil {
		return AuthorizationResult{}, err
	}
	accounts, err := s.ensureCardFundingAccounts(ctx, tx, cardID)
	if err != nil {
		return AuthorizationResult{}, err
	}
	if cardStatus != "active" {
		return s.recordDeclinedAuthorization(ctx, tx, params, cardID, "card_not_active", accounts)
	}
	if cardCurrency != params.Currency || accounts.Currency != params.Currency {
		return s.recordDeclinedAuthorization(ctx, tx, params, cardID, "currency_mismatch", accounts)
	}
	if accounts.AvailableStatus != "active" {
		return s.recordDeclinedAuthorization(ctx, tx, params, cardID, "funding_source_not_active", accounts)
	}
	if accounts.SpendingLimitMinor > 0 && params.AmountMinor > accounts.SpendingLimitMinor {
		return s.recordDeclinedAuthorization(ctx, tx, params, cardID, "spending_limit_exceeded", accounts)
	}

	holdJournalID := stableLedgerID("journal-hold", params.AuthorizationID)
	holdTx, err := tx.Begin(ctx)
	if err != nil {
		return AuthorizationResult{}, err
	}
	_, err = s.ledger.PostJournalInTx(ctx, holdTx, ledgerpkg.PostJournalCommand{
		JournalID:    holdJournalID,
		BookID:       localLedgerBookID,
		BusinessType: "card_authorization",
		BusinessID:   params.AuthorizationID,
		EventType:    "hold",
		Currency:     params.Currency,
		EffectiveAt:  s.now().Unix(),
		Postings: []ledgerpkg.Posting{
			{AccountID: accounts.AvailableID, Side: ledgerpkg.Debit, AmountMinor: params.AmountMinor},
			{AccountID: accounts.HeldID, Side: ledgerpkg.Credit, AmountMinor: params.AmountMinor},
		},
	})
	if errors.Is(err, ledgerpkg.ErrInsufficientFunds) {
		_ = holdTx.Rollback(ctx)
		return s.recordDeclinedAuthorization(ctx, tx, params, cardID, "insufficient_funds", accounts)
	}
	if err != nil {
		_ = holdTx.Rollback(ctx)
		return AuthorizationResult{}, err
	}
	if err := holdTx.Commit(ctx); err != nil {
		return AuthorizationResult{}, err
	}
	balance, err := loadCardLedgerBalance(ctx, tx, cardID, accounts)
	if err != nil {
		return AuthorizationResult{}, err
	}
	result := AuthorizationResult{
		AuthorizationID:       params.AuthorizationID,
		Decision:              "approved",
		PostedBalanceMinor:    balance.PostedBalanceMinor,
		HeldAmountMinor:       balance.HeldAmountMinor,
		AvailableBalanceMinor: balance.AvailableBalanceMinor,
	}
	_, err = tx.Exec(ctx, `INSERT INTO card_lab_authorizations(
		authorization_id,provider_card_id,card_id,currency,amount_minor,decision,reason,
		posted_balance_after,held_amount_after,available_balance_after,authorization_status,
		funding_source_id,available_account_id,held_account_id,hold_journal_id,ledger_version
	) VALUES($1,$2,$3,$4,$5,'approved','',$6,$7,$8,'authorized',$9,$10,$11,$12,1)`,
		params.AuthorizationID, params.ProviderCardID, cardID, params.Currency, params.AmountMinor,
		result.PostedBalanceMinor, result.HeldAmountMinor, result.AvailableBalanceMinor,
		accounts.BindingID, accounts.AvailableID, accounts.HeldID, holdJournalID)
	if err != nil {
		return AuthorizationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AuthorizationResult{}, err
	}
	return result, nil
}

func (s *PostgresStore) ReverseAuthorization(ctx context.Context, params AuthorizationReversalParams) (AuthorizationReversalResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AuthorizationReversalResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, params.AuthorizationID); err != nil {
		return AuthorizationReversalResult{}, err
	}
	var existingAuthorizationID string
	var result AuthorizationReversalResult
	err = tx.QueryRow(ctx, `SELECT authorization_id,released_amount_minor,posted_balance_after,held_amount_after,available_balance_after
		FROM card_lab_authorization_reversals WHERE reversal_id=$1`, params.ReversalID).
		Scan(&existingAuthorizationID, &result.ReleasedAmountMinor, &result.PostedBalanceMinor, &result.HeldAmountMinor, &result.AvailableBalanceMinor)
	if err == nil {
		if existingAuthorizationID != params.AuthorizationID {
			return AuthorizationReversalResult{}, ErrIdempotencyConflict
		}
		result.ReversalID, result.AuthorizationID, result.Status = params.ReversalID, params.AuthorizationID, "reversed"
		if err := tx.Commit(ctx); err != nil {
			return AuthorizationReversalResult{}, err
		}
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationReversalResult{}, err
	}

	var cardID, currency, decision, status, availableID, heldID string
	var amount int64
	err = tx.QueryRow(ctx, `SELECT card_id,currency,amount_minor,decision,authorization_status,available_account_id,held_account_id
		FROM card_lab_authorizations WHERE authorization_id=$1 FOR UPDATE`, params.AuthorizationID).
		Scan(&cardID, &currency, &amount, &decision, &status, &availableID, &heldID)
	if errors.Is(err, pgx.ErrNoRows) || decision != "approved" || status != "authorized" {
		return AuthorizationReversalResult{}, ErrAuthorizationNotReversible
	}
	if err != nil {
		return AuthorizationReversalResult{}, err
	}
	releaseJournalID := stableLedgerID("journal-hold-release", params.ReversalID)
	_, err = s.ledger.PostJournalInTx(ctx, tx, ledgerpkg.PostJournalCommand{
		JournalID: releaseJournalID, BookID: localLedgerBookID, BusinessType: "card_authorization_reversal",
		BusinessID: params.ReversalID, EventType: "hold_release", Currency: currency, EffectiveAt: s.now().Unix(),
		Postings: []ledgerpkg.Posting{
			{AccountID: heldID, Side: ledgerpkg.Debit, AmountMinor: amount},
			{AccountID: availableID, Side: ledgerpkg.Credit, AmountMinor: amount},
		},
	})
	if err != nil {
		return AuthorizationReversalResult{}, err
	}
	balance, err := loadCardLedgerBalance(ctx, tx, cardID, fundingAccounts{AvailableID: availableID, HeldID: heldID, Currency: currency})
	if err != nil {
		return AuthorizationReversalResult{}, err
	}
	result = AuthorizationReversalResult{params.ReversalID, params.AuthorizationID, "reversed", amount, balance.PostedBalanceMinor, balance.HeldAmountMinor, balance.AvailableBalanceMinor}
	if _, err := tx.Exec(ctx, `INSERT INTO card_lab_authorization_reversals(
		reversal_id,authorization_id,released_amount_minor,posted_balance_after,held_amount_after,available_balance_after,release_journal_id,ledger_version
	) VALUES($1,$2,$3,$4,$5,$6,$7,1)`, params.ReversalID, params.AuthorizationID, amount,
		result.PostedBalanceMinor, result.HeldAmountMinor, result.AvailableBalanceMinor, releaseJournalID); err != nil {
		return AuthorizationReversalResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE card_lab_authorizations SET authorization_status='reversed' WHERE authorization_id=$1`, params.AuthorizationID); err != nil {
		return AuthorizationReversalResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AuthorizationReversalResult{}, err
	}
	return result, nil
}

func (s *PostgresStore) SettleAuthorization(ctx context.Context, params AuthorizationSettlementParams) (AuthorizationSettlementResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AuthorizationSettlementResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, params.AuthorizationID); err != nil {
		return AuthorizationSettlementResult{}, err
	}
	var existingAuthorizationID string
	var result AuthorizationSettlementResult
	err = tx.QueryRow(ctx, `SELECT authorization_id,settled_amount_minor,posted_balance_after,held_amount_after,available_balance_after
		FROM card_lab_authorization_settlements WHERE settlement_id=$1`, params.SettlementID).
		Scan(&existingAuthorizationID, &result.SettledAmountMinor, &result.PostedBalanceMinor, &result.HeldAmountMinor, &result.AvailableBalanceMinor)
	if err == nil {
		if existingAuthorizationID != params.AuthorizationID {
			return AuthorizationSettlementResult{}, ErrIdempotencyConflict
		}
		result.SettlementID, result.AuthorizationID, result.Status = params.SettlementID, params.AuthorizationID, "settled"
		if err := tx.Commit(ctx); err != nil {
			return AuthorizationSettlementResult{}, err
		}
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationSettlementResult{}, err
	}

	var cardID, currency, decision, status, availableID, heldID string
	var amount int64
	err = tx.QueryRow(ctx, `SELECT card_id,currency,amount_minor,decision,authorization_status,available_account_id,held_account_id
		FROM card_lab_authorizations WHERE authorization_id=$1 FOR UPDATE`, params.AuthorizationID).
		Scan(&cardID, &currency, &amount, &decision, &status, &availableID, &heldID)
	if errors.Is(err, pgx.ErrNoRows) || decision != "approved" || status != "authorized" {
		return AuthorizationSettlementResult{}, ErrAuthorizationNotSettleable
	}
	if err != nil {
		return AuthorizationSettlementResult{}, err
	}
	providerPayableID := systemAccountID("provider-payable", currency)
	settlementJournalID := stableLedgerID("journal-settlement", params.SettlementID)
	_, err = s.ledger.PostJournalInTx(ctx, tx, ledgerpkg.PostJournalCommand{
		JournalID: settlementJournalID, BookID: localLedgerBookID, BusinessType: "card_settlement",
		BusinessID: params.SettlementID, EventType: "settlement", Currency: currency, EffectiveAt: s.now().Unix(),
		Postings: []ledgerpkg.Posting{
			{AccountID: heldID, Side: ledgerpkg.Debit, AmountMinor: amount},
			{AccountID: providerPayableID, Side: ledgerpkg.Credit, AmountMinor: amount},
		},
	})
	if err != nil {
		return AuthorizationSettlementResult{}, err
	}
	balance, err := loadCardLedgerBalance(ctx, tx, cardID, fundingAccounts{AvailableID: availableID, HeldID: heldID, Currency: currency})
	if err != nil {
		return AuthorizationSettlementResult{}, err
	}
	result = AuthorizationSettlementResult{params.SettlementID, params.AuthorizationID, "settled", amount, balance.PostedBalanceMinor, balance.HeldAmountMinor, balance.AvailableBalanceMinor}
	if _, err := tx.Exec(ctx, `INSERT INTO card_lab_authorization_settlements(
		settlement_id,authorization_id,settled_amount_minor,posted_balance_after,held_amount_after,available_balance_after,settlement_journal_id,ledger_version
	) VALUES($1,$2,$3,$4,$5,$6,$7,1)`, params.SettlementID, params.AuthorizationID, amount,
		result.PostedBalanceMinor, result.HeldAmountMinor, result.AvailableBalanceMinor, settlementJournalID); err != nil {
		return AuthorizationSettlementResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE card_lab_authorizations SET authorization_status='settled' WHERE authorization_id=$1`, params.AuthorizationID); err != nil {
		return AuthorizationSettlementResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AuthorizationSettlementResult{}, err
	}
	return result, nil
}

func (s *PostgresStore) RefundSettlement(ctx context.Context, params SettlementRefundParams) (SettlementRefundResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SettlementRefundResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, params.SettlementID); err != nil {
		return SettlementRefundResult{}, err
	}
	var existingSettlementID string
	var result SettlementRefundResult
	err = tx.QueryRow(ctx, `SELECT settlement_id,refunded_amount_minor,posted_balance_after,held_amount_after,available_balance_after
		FROM card_lab_settlement_refunds WHERE refund_id=$1`, params.RefundID).
		Scan(&existingSettlementID, &result.RefundedAmountMinor, &result.PostedBalanceMinor, &result.HeldAmountMinor, &result.AvailableBalanceMinor)
	if err == nil {
		if existingSettlementID != params.SettlementID {
			return SettlementRefundResult{}, ErrIdempotencyConflict
		}
		result.RefundID, result.SettlementID, result.Status = params.RefundID, params.SettlementID, "refunded"
		if err := tx.Commit(ctx); err != nil {
			return SettlementRefundResult{}, err
		}
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return SettlementRefundResult{}, err
	}

	var authorizationID string
	var amount int64
	err = tx.QueryRow(ctx, `SELECT authorization_id,settled_amount_minor FROM card_lab_authorization_settlements WHERE settlement_id=$1`, params.SettlementID).
		Scan(&authorizationID, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return SettlementRefundResult{}, ErrSettlementNotRefundable
	}
	if err != nil {
		return SettlementRefundResult{}, err
	}
	var cardID, currency, status, availableID, heldID string
	err = tx.QueryRow(ctx, `SELECT card_id,currency,authorization_status,available_account_id,held_account_id
		FROM card_lab_authorizations WHERE authorization_id=$1 FOR UPDATE`, authorizationID).
		Scan(&cardID, &currency, &status, &availableID, &heldID)
	if err != nil {
		return SettlementRefundResult{}, err
	}
	if status != "settled" {
		return SettlementRefundResult{}, ErrSettlementNotRefundable
	}
	providerReceivableID := systemAccountID("provider-receivable", currency)
	refundJournalID := stableLedgerID("journal-refund", params.RefundID)
	_, err = s.ledger.PostJournalInTx(ctx, tx, ledgerpkg.PostJournalCommand{
		JournalID: refundJournalID, BookID: localLedgerBookID, BusinessType: "card_refund",
		BusinessID: params.RefundID, EventType: "refund", Currency: currency, EffectiveAt: s.now().Unix(),
		Postings: []ledgerpkg.Posting{
			{AccountID: providerReceivableID, Side: ledgerpkg.Debit, AmountMinor: amount},
			{AccountID: availableID, Side: ledgerpkg.Credit, AmountMinor: amount},
		},
	})
	if err != nil {
		return SettlementRefundResult{}, err
	}
	balance, err := loadCardLedgerBalance(ctx, tx, cardID, fundingAccounts{AvailableID: availableID, HeldID: heldID, Currency: currency})
	if err != nil {
		return SettlementRefundResult{}, err
	}
	result = SettlementRefundResult{params.RefundID, params.SettlementID, "refunded", amount, balance.PostedBalanceMinor, balance.HeldAmountMinor, balance.AvailableBalanceMinor}
	if _, err := tx.Exec(ctx, `INSERT INTO card_lab_settlement_refunds(
		refund_id,settlement_id,refunded_amount_minor,posted_balance_after,held_amount_after,available_balance_after,refund_journal_id,ledger_version
	) VALUES($1,$2,$3,$4,$5,$6,$7,1)`, params.RefundID, params.SettlementID, amount,
		result.PostedBalanceMinor, result.HeldAmountMinor, result.AvailableBalanceMinor, refundJournalID); err != nil {
		return SettlementRefundResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE card_lab_authorizations SET authorization_status='refunded' WHERE authorization_id=$1`, authorizationID); err != nil {
		return SettlementRefundResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SettlementRefundResult{}, err
	}
	return result, nil
}

func (s *PostgresStore) recordDeclinedAuthorization(ctx context.Context, tx pgx.Tx, params AuthorizationParams, cardID, reason string, accounts fundingAccounts) (AuthorizationResult, error) {
	balance := AccountBalance{CardID: cardID, Currency: accounts.Currency}
	var err error
	if cardID != "" && accounts.AvailableID != "" {
		balance, err = loadCardLedgerBalance(ctx, tx, cardID, accounts)
		if err != nil {
			return AuthorizationResult{}, err
		}
	}
	result := AuthorizationResult{AuthorizationID: params.AuthorizationID, Decision: "declined", Reason: reason,
		PostedBalanceMinor: balance.PostedBalanceMinor, HeldAmountMinor: balance.HeldAmountMinor, AvailableBalanceMinor: balance.AvailableBalanceMinor}
	_, err = tx.Exec(ctx, `INSERT INTO card_lab_authorizations(
		authorization_id,provider_card_id,card_id,currency,amount_minor,decision,reason,
		posted_balance_after,held_amount_after,available_balance_after,authorization_status,
		funding_source_id,available_account_id,held_account_id,ledger_version
	) VALUES($1,$2,NULLIF($3,''),$4,$5,'declined',$6,$7,$8,$9,'declined',$10,$11,$12,1)`,
		params.AuthorizationID, params.ProviderCardID, cardID, params.Currency, params.AmountMinor, reason,
		result.PostedBalanceMinor, result.HeldAmountMinor, result.AvailableBalanceMinor,
		accounts.BindingID, accounts.AvailableID, accounts.HeldID)
	if err != nil {
		return AuthorizationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AuthorizationResult{}, err
	}
	return result, nil
}

func loadExistingAuthorization(ctx context.Context, tx pgx.Tx, params AuthorizationParams) (AuthorizationResult, bool, error) {
	var result AuthorizationResult
	var providerCardID, currency string
	var amount int64
	err := tx.QueryRow(ctx, `SELECT provider_card_id,currency,amount_minor,decision,reason,
		posted_balance_after,held_amount_after,available_balance_after
		FROM card_lab_authorizations WHERE authorization_id=$1`, params.AuthorizationID).
		Scan(&providerCardID, &currency, &amount, &result.Decision, &result.Reason,
			&result.PostedBalanceMinor, &result.HeldAmountMinor, &result.AvailableBalanceMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthorizationResult{}, false, nil
	}
	if err != nil {
		return AuthorizationResult{}, false, err
	}
	if providerCardID != params.ProviderCardID || currency != params.Currency || amount != params.AmountMinor {
		return AuthorizationResult{}, false, ErrIdempotencyConflict
	}
	result.AuthorizationID = params.AuthorizationID
	return result, true, nil
}

func (s *PostgresStore) ensureCardFundingAccounts(ctx context.Context, tx pgx.Tx, cardID string) (fundingAccounts, error) {
	var organizationID, currency string
	if err := tx.QueryRow(ctx, `SELECT organization_id,currency FROM card_lab_cards WHERE id=$1 FOR UPDATE`, cardID).
		Scan(&organizationID, &currency); err != nil {
		return fundingAccounts{}, err
	}
	currency = strings.ToUpper(currency)
	if _, err := tx.Exec(ctx, `INSERT INTO ledger_books(id,legal_entity_id,name,base_currency,status)
		VALUES($1,'local-legal-entity','Local Operational Ledger',$2,'active') ON CONFLICT(id) DO NOTHING`, localLedgerBookID, currency); err != nil {
		return fundingAccounts{}, err
	}
	platformCashID := systemAccountID("platform-cash", currency)
	providerPayableID := systemAccountID("provider-payable", currency)
	providerReceivableID := systemAccountID("provider-receivable", currency)
	for _, account := range []struct {
		id, key, group, accountType, normalSide, ownerType, ownerID, purpose string
	}{
		{platformCashID, "platform.cash." + strings.ToLower(currency), "platform-" + strings.ToLower(currency), "asset", "debit", "platform", "platform-local", "cash"},
		{providerPayableID, "provider.payable." + strings.ToLower(currency), "provider-" + strings.ToLower(currency), "liability", "credit", "provider", "provider-local", "payable"},
		{providerReceivableID, "provider.receivable." + strings.ToLower(currency), "provider-" + strings.ToLower(currency), "asset", "debit", "provider", "provider-local", "receivable"},
	} {
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_accounts(
			id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'active') ON CONFLICT(book_id,account_key) DO NOTHING`,
			account.id, localLedgerBookID, account.key, account.group, account.accountType, account.normalSide,
			account.ownerType, account.ownerID, currency, account.purpose); err != nil {
			return fundingAccounts{}, err
		}
	}
	groupID := stableLedgerID("funding-pool", organizationID+":"+currency)
	availableID := stableLedgerID("account-available", organizationID+":"+currency)
	heldID := stableLedgerID("account-held", organizationID+":"+currency)
	for _, account := range []struct{ id, key, purpose string }{
		{availableID, fmt.Sprintf("organization.%s.%s.available", organizationID, strings.ToLower(currency)), "available"},
		{heldID, fmt.Sprintf("organization.%s.%s.held", organizationID, strings.ToLower(currency)), "held"},
	} {
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_accounts(
			id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status
		) VALUES($1,$2,$3,$4,'liability','credit','organization',$5,$6,$7,'active') ON CONFLICT(book_id,account_key) DO NOTHING`,
			account.id, localLedgerBookID, account.key, groupID, organizationID, currency, account.purpose); err != nil {
			return fundingAccounts{}, err
		}
	}
	bindingID := stableLedgerID("funding-source", cardID)
	if _, err := tx.Exec(ctx, `INSERT INTO card_funding_sources(
		id,card_id,account_id,priority,spending_limit_minor,status,effective_from,effective_to
	) VALUES($1,$2,$3,0,0,'active',0,0)
	ON CONFLICT(card_id) WHERE status='active' DO NOTHING`, bindingID, cardID, availableID); err != nil {
		return fundingAccounts{}, err
	}
	var result fundingAccounts
	result.PlatformCashID, result.ProviderPayableID, result.ProviderReceivable = platformCashID, providerPayableID, providerReceivableID
	result.Currency = currency
	var accountGroupID string
	err := tx.QueryRow(ctx, `SELECT fs.id,fs.account_id,fs.spending_limit_minor,a.account_group_id,a.status
		FROM card_funding_sources fs
		JOIN ledger_accounts a ON a.id=fs.account_id
		WHERE fs.card_id=$1 AND fs.status='active' AND fs.effective_from <= $2
		  AND (fs.effective_to=0 OR fs.effective_to > $2)
		FOR UPDATE OF fs,a`, cardID, s.now().Unix()).
		Scan(&result.BindingID, &result.AvailableID, &result.SpendingLimitMinor, &accountGroupID, &result.AvailableStatus)
	if err != nil {
		return fundingAccounts{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM ledger_accounts
		WHERE book_id=$1 AND account_group_id=$2 AND purpose='held' AND currency=$3`, localLedgerBookID, accountGroupID, currency).
		Scan(&result.HeldID); err != nil {
		return fundingAccounts{}, err
	}
	if err := s.migrateLegacyCardBalance(ctx, tx, cardID, result); err != nil {
		return fundingAccounts{}, err
	}
	return result, nil
}

func (s *PostgresStore) migrateLegacyCardBalance(ctx context.Context, tx pgx.Tx, cardID string, accounts fundingAccounts) error {
	var posted, held int64
	var legacyCurrency string
	err := tx.QueryRow(ctx, `SELECT currency,posted_balance_minor,held_amount_minor
		FROM card_lab_accounts WHERE card_id=$1 FOR UPDATE`, cardID).Scan(&legacyCurrency, &posted, &held)
	if errors.Is(err, pgx.ErrNoRows) {
		legacyCurrency, posted, held = accounts.Currency, 0, 0
	} else if err != nil {
		return err
	}
	if legacyCurrency != accounts.Currency || posted < 0 || held < 0 || held > posted {
		return ErrIdempotencyConflict
	}
	fingerprintInput := fmt.Sprintf("%s\n%s\n%d\n%d", cardID, legacyCurrency, posted, held)
	fingerprintSum := sha256.Sum256([]byte(fingerprintInput))
	fingerprint := hex.EncodeToString(fingerprintSum[:])
	var existingFingerprint, status string
	err = tx.QueryRow(ctx, `SELECT source_fingerprint,status FROM ledger_migrations
		WHERE source_type='legacy_card_account' AND source_id=$1 FOR UPDATE`, cardID).
		Scan(&existingFingerprint, &status)
	if err == nil {
		if existingFingerprint != fingerprint {
			return ErrIdempotencyConflict
		}
		if status == "completed" {
			return nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	} else {
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_migrations(
			id,source_type,source_id,source_fingerprint,status,attempt_count
		) VALUES($1,'legacy_card_account',$2,$3,'pending',1)`, stableLedgerID("migration", cardID), cardID, fingerprint); err != nil {
			return err
		}
	}

	openingJournalID := ""
	if posted > 0 {
		openingJournalID = stableLedgerID("journal-opening", cardID)
		postings := []ledgerpkg.Posting{{AccountID: accounts.PlatformCashID, Side: ledgerpkg.Debit, AmountMinor: posted}}
		if available := posted - held; available > 0 {
			postings = append(postings, ledgerpkg.Posting{AccountID: accounts.AvailableID, Side: ledgerpkg.Credit, AmountMinor: available})
		}
		if held > 0 {
			postings = append(postings, ledgerpkg.Posting{AccountID: accounts.HeldID, Side: ledgerpkg.Credit, AmountMinor: held})
		}
		if _, err := s.ledger.PostJournalInTx(ctx, tx, ledgerpkg.PostJournalCommand{
			JournalID: openingJournalID, BookID: localLedgerBookID, BusinessType: "legacy_card_account",
			BusinessID: cardID, EventType: "opening_balance", Currency: accounts.Currency, EffectiveAt: s.now().Unix(),
			Postings: postings,
		}); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE ledger_migrations SET status='completed',opening_journal_id=$2,
		completed_at=NOW(),last_error=NULL WHERE source_type='legacy_card_account' AND source_id=$1`, cardID, openingJournalID)
	return err
}

func loadCardLedgerBalance(ctx context.Context, tx pgx.Tx, cardID string, accounts fundingAccounts) (AccountBalance, error) {
	result := AccountBalance{CardID: cardID, Currency: accounts.Currency}
	for accountID, destination := range map[string]*int64{
		accounts.AvailableID: &result.AvailableBalanceMinor,
		accounts.HeldID:      &result.HeldAmountMinor,
	} {
		if accountID == "" {
			continue
		}
		var debits, credits int64
		err := tx.QueryRow(ctx, `SELECT posted_debits_minor,posted_credits_minor FROM ledger_balances WHERE account_id=$1`, accountID).
			Scan(&debits, &credits)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return AccountBalance{}, err
		}
		*destination = credits - debits
	}
	result.PostedBalanceMinor = result.AvailableBalanceMinor + result.HeldAmountMinor
	return result, nil
}

func systemAccountID(prefix, currency string) string {
	return prefix + "-" + strings.ToLower(currency)
}

func stableLedgerID(prefix, value string) string {
	sum := sha256.Sum256([]byte(value))
	return prefix + "-" + hex.EncodeToString(sum[:12])
}
