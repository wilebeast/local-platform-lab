package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Side string

const (
	Debit  Side = "debit"
	Credit Side = "credit"
)

var (
	ErrUnbalancedJournal                  = errors.New("ledger journal is not balanced")
	ErrIdempotencyConflict                = errors.New("ledger business key belongs to another journal command")
	ErrAccountBookMismatch                = errors.New("ledger account belongs to another book")
	ErrAccountCurrencyMismatch            = errors.New("ledger account uses another currency")
	ErrAccountClosed                      = errors.New("ledger account is closed")
	ErrAccountHasBalance                  = errors.New("ledger account has a non-zero balance")
	ErrAccountHasActiveFundingSource      = errors.New("ledger account has an active card funding source")
	ErrAccountHasOutstandingAuthorization = errors.New("ledger account has an outstanding authorization")
	ErrFundingSourceAccountPurpose        = errors.New("card funding source account must have available purpose")
	ErrFundingSourceCurrencyMismatch      = errors.New("card and funding source account use different currencies")
	ErrFundingSourceOwnerMismatch         = errors.New("card and funding source account belong to different organizations")
	ErrFundingSourceHeldAccountMissing    = errors.New("card funding source account has no matching held account")
	ErrCardHasActiveFundingSource         = errors.New("card already has an active funding source")
	ErrInsufficientFunds                  = errors.New("ledger account has insufficient funds")
	ErrJournalNotPosted                   = errors.New("ledger journal is not posted")
	ErrJournalAlreadyReversed             = errors.New("ledger journal is already reversed")
)

type Posting struct {
	AccountID   string
	Side        Side
	AmountMinor int64
}

type PostJournalCommand struct {
	JournalID, BookID, BusinessType, BusinessID, EventType, Currency string
	EffectiveAt                                                      int64
	ReversalOf                                                       string
	Postings                                                         []Posting
}

type ReverseJournalCommand struct {
	JournalID         string
	OriginalJournalID string
	BusinessType      string
	BusinessID        string
	EffectiveAt       int64
}

type BindFundingSourceCommand struct {
	BindingID          string
	CardID             string
	AccountID          string
	SpendingLimitMinor int64
	EffectiveAt        int64
	EffectiveTo        int64
}

type ProvisionFundingPoolCommand struct {
	PoolID             string
	BookID             string
	OwnerID            string
	Currency           string
	AccountKeyPrefix   string
	AvailableAccountID string
	HeldAccountID      string
}

type FundingPool struct {
	PoolID             string
	AvailableAccountID string
	HeldAccountID      string
	OwnerID            string
	Currency           string
}

type AccountBalance struct {
	AccountID          string
	PostedDebitsMinor  int64
	PostedCreditsMinor int64
	BalanceMinor       int64
	Version            int64
}

type PostedJournal struct {
	JournalID    string
	Status       string
	PostingCount int
	Balances     []AccountBalance
}

type PostgresStore struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool, now: time.Now}
}

func (s *PostgresStore) ProvisionFundingPool(ctx context.Context, command ProvisionFundingPoolCommand) (FundingPool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FundingPool{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockKey, err := json.Marshal([]string{command.BookID, command.AccountKeyPrefix})
	if err != nil {
		return FundingPool{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, string(lockKey)); err != nil {
		return FundingPool{}, err
	}

	accounts := []struct {
		id, key, purpose string
	}{
		{command.AvailableAccountID, command.AccountKeyPrefix + ".available", "available"},
		{command.HeldAccountID, command.AccountKeyPrefix + ".held", "held"},
	}
	for _, account := range accounts {
		result, err := tx.Exec(ctx, `INSERT INTO ledger_accounts(
			id,book_id,account_key,account_group_id,account_type,normal_side,
			owner_type,owner_id,currency,purpose,status
		) VALUES($1,$2,$3,$4,'liability','credit','organization',$5,$6,$7,'active')
		ON CONFLICT(book_id,account_key) DO NOTHING`, account.id, command.BookID, account.key,
			command.PoolID, command.OwnerID, command.Currency, account.purpose)
		if err != nil {
			return FundingPool{}, err
		}
		if result.RowsAffected() == 0 {
			var id, poolID, ownerID, currency, purpose string
			if err := tx.QueryRow(ctx, `SELECT id,account_group_id,owner_id,currency,purpose
				FROM ledger_accounts WHERE book_id=$1 AND account_key=$2`, command.BookID, account.key).
				Scan(&id, &poolID, &ownerID, &currency, &purpose); err != nil {
				return FundingPool{}, err
			}
			if id != account.id || poolID != command.PoolID || ownerID != command.OwnerID ||
				currency != command.Currency || purpose != account.purpose {
				return FundingPool{}, ErrIdempotencyConflict
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ledger_balances(account_id) VALUES($1)
			ON CONFLICT(account_id) DO NOTHING`, account.id); err != nil {
			return FundingPool{}, err
		}
	}
	result := FundingPool{command.PoolID, command.AvailableAccountID, command.HeldAccountID, command.OwnerID, command.Currency}
	if err := tx.Commit(ctx); err != nil {
		return FundingPool{}, err
	}
	return result, nil
}

func (s *PostgresStore) BindFundingSource(ctx context.Context, command BindFundingSourceCommand) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var cardOrganizationID, cardCurrency string
	if err := tx.QueryRow(ctx, `SELECT organization_id,currency FROM card_lab_cards WHERE id=$1 FOR UPDATE`, command.CardID).Scan(&cardOrganizationID, &cardCurrency); err != nil {
		return err
	}
	var accountOwnerType, accountOwnerID, accountPurpose, accountCurrency, accountStatus string
	if err := tx.QueryRow(ctx, `SELECT owner_type,owner_id,purpose,currency,status FROM ledger_accounts WHERE id=$1 FOR UPDATE`, command.AccountID).Scan(&accountOwnerType, &accountOwnerID, &accountPurpose, &accountCurrency, &accountStatus); err != nil {
		return err
	}
	if accountPurpose != "available" {
		return ErrFundingSourceAccountPurpose
	}
	if accountCurrency != cardCurrency {
		return ErrFundingSourceCurrencyMismatch
	}
	if accountStatus == "closed" {
		return ErrAccountClosed
	}
	if accountOwnerType != "organization" || accountOwnerID != cardOrganizationID {
		return ErrFundingSourceOwnerMismatch
	}
	var hasMatchingHeldAccount bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1
		FROM ledger_accounts available
		JOIN ledger_accounts held
		  ON held.book_id=available.book_id
		 AND held.account_group_id=available.account_group_id
		 AND held.owner_type=available.owner_type
		 AND held.owner_id=available.owner_id
		 AND held.currency=available.currency
		 AND held.purpose='held'
		 AND held.status <> 'closed'
		WHERE available.id=$1
	)`, command.AccountID).Scan(&hasMatchingHeldAccount); err != nil {
		return err
	}
	if !hasMatchingHeldAccount {
		return ErrFundingSourceHeldAccountMissing
	}
	if _, err := tx.Exec(ctx, `INSERT INTO card_funding_sources(
		id,card_id,account_id,priority,spending_limit_minor,status,effective_from,effective_to
	) VALUES($1,$2,$3,0,$4,'active',$5,$6)`,
		command.BindingID, command.CardID, command.AccountID,
		command.SpendingLimitMinor, command.EffectiveAt, command.EffectiveTo); err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.ConstraintName == "card_funding_sources_one_active_per_card_idx" {
			return ErrCardHasActiveFundingSource
		}
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) CloseAccount(ctx context.Context, accountID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var normalSide Side
	var status string
	if err := tx.QueryRow(ctx, `SELECT normal_side,status FROM ledger_accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&normalSide, &status); err != nil {
		return err
	}
	if status == "closed" {
		return tx.Commit(ctx)
	}
	var debits, credits int64
	err = tx.QueryRow(ctx, `SELECT posted_debits_minor,posted_credits_minor FROM ledger_balances WHERE account_id=$1`, accountID).
		Scan(&debits, &credits)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	balance := debits - credits
	if normalSide == Credit {
		balance = credits - debits
	}
	if balance != 0 {
		return ErrAccountHasBalance
	}
	var hasActiveFundingSource bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM card_funding_sources
		WHERE account_id=$1
		  AND status='active'
		  AND effective_from <= $2
		  AND (effective_to=0 OR effective_to > $2)
	)`, accountID, s.now().Unix()).Scan(&hasActiveFundingSource); err != nil {
		return err
	}
	if hasActiveFundingSource {
		return ErrAccountHasActiveFundingSource
	}
	var hasOutstandingAuthorization bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1
		FROM card_lab_authorizations auth
		JOIN card_funding_sources funding_source ON funding_source.card_id=auth.card_id
		WHERE funding_source.account_id=$1
		  AND auth.decision='approved'
		  AND auth.authorization_status='authorized'
	)`, accountID).Scan(&hasOutstandingAuthorization); err != nil {
		return err
	}
	if hasOutstandingAuthorization {
		return ErrAccountHasOutstandingAuthorization
	}
	if _, err := tx.Exec(ctx, `UPDATE ledger_accounts SET status='closed',version=version+1,updated_at=NOW() WHERE id=$1`, accountID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) ReverseJournal(ctx context.Context, command ReverseJournalCommand) (PostedJournal, error) {
	var bookID, currency, status string
	if err := s.pool.QueryRow(ctx, `SELECT book_id,currency,status FROM ledger_journals WHERE id=$1`, command.OriginalJournalID).
		Scan(&bookID, &currency, &status); err != nil {
		return PostedJournal{}, err
	}
	if status != "posted" {
		return PostedJournal{}, ErrJournalNotPosted
	}

	rows, err := s.pool.Query(ctx, `SELECT account_id,side,amount_minor
		FROM ledger_postings WHERE journal_id=$1 ORDER BY sequence_no`, command.OriginalJournalID)
	if err != nil {
		return PostedJournal{}, err
	}
	postings := make([]Posting, 0)
	for rows.Next() {
		var posting Posting
		if err := rows.Scan(&posting.AccountID, &posting.Side, &posting.AmountMinor); err != nil {
			rows.Close()
			return PostedJournal{}, err
		}
		if posting.Side == Debit {
			posting.Side = Credit
		} else {
			posting.Side = Debit
		}
		postings = append(postings, posting)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PostedJournal{}, err
	}
	rows.Close()

	return s.PostJournal(ctx, PostJournalCommand{
		JournalID:    command.JournalID,
		BookID:       bookID,
		BusinessType: command.BusinessType,
		BusinessID:   command.BusinessID,
		EventType:    "reversal",
		Currency:     currency,
		EffectiveAt:  command.EffectiveAt,
		ReversalOf:   command.OriginalJournalID,
		Postings:     postings,
	})
}

func (s *PostgresStore) PostJournal(ctx context.Context, command PostJournalCommand) (PostedJournal, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PostedJournal{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := s.PostJournalInTx(ctx, tx, command)
	if err != nil {
		return PostedJournal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PostedJournal{}, err
	}
	return result, nil
}

// PostJournalInTx posts a journal inside the caller's transaction so the
// financial effect and its owning business fact can commit atomically.
func (s *PostgresStore) PostJournalInTx(ctx context.Context, tx pgx.Tx, command PostJournalCommand) (PostedJournal, error) {
	if err := validateJournalBalance(command.Postings); err != nil {
		return PostedJournal{}, err
	}
	fingerprint, err := journalFingerprint(command)
	if err != nil {
		return PostedJournal{}, err
	}
	idempotencyLockKey, err := json.Marshal([]string{command.BookID, command.BusinessType, command.BusinessID, command.EventType})
	if err != nil {
		return PostedJournal{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, string(idempotencyLockKey)); err != nil {
		return PostedJournal{}, err
	}

	var existingJournalID, existingFingerprint string
	var existingEffectiveAt int64
	err = tx.QueryRow(ctx, `SELECT id,command_fingerprint,effective_at
		FROM ledger_journals
		WHERE book_id=$1 AND business_type=$2 AND business_id=$3 AND event_type=$4`,
		command.BookID, command.BusinessType, command.BusinessID, command.EventType).
		Scan(&existingJournalID, &existingFingerprint, &existingEffectiveAt)
	if err == nil {
		legacyFingerprint, fingerprintErr := legacyJournalFingerprint(command, existingEffectiveAt)
		if fingerprintErr != nil {
			return PostedJournal{}, fingerprintErr
		}
		if existingFingerprint == fingerprint || existingFingerprint == legacyFingerprint {
			return loadPostedJournal(ctx, tx, existingJournalID)
		}
	}
	if err == nil {
		return PostedJournal{}, ErrIdempotencyConflict
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PostedJournal{}, err
	}

	_, err = tx.Exec(ctx, `INSERT INTO ledger_journals
		(id,book_id,business_type,business_id,event_type,currency,status,reversal_of,effective_at,command_fingerprint)
		VALUES($1,$2,$3,$4,$5,$6,'draft',$7,$8,$9)`,
		command.JournalID, command.BookID, command.BusinessType, command.BusinessID,
		command.EventType, command.Currency, command.ReversalOf, command.EffectiveAt, fingerprint)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.ConstraintName == "ledger_journals_reversal_once_idx" {
			return PostedJournal{}, ErrJournalAlreadyReversed
		}
		return PostedJournal{}, err
	}

	accountIDs := make([]string, 0, len(command.Postings))
	seen := make(map[string]struct{}, len(command.Postings))
	for _, posting := range command.Postings {
		if _, exists := seen[posting.AccountID]; !exists {
			seen[posting.AccountID] = struct{}{}
			accountIDs = append(accountIDs, posting.AccountID)
		}
	}
	sort.Strings(accountIDs)
	normalSides := make(map[string]Side, len(accountIDs))
	purposes := make(map[string]string, len(accountIDs))
	type projectedBalance struct {
		debits  int64
		credits int64
	}
	projectedBalances := make(map[string]projectedBalance, len(accountIDs))
	for _, accountID := range accountIDs {
		var normalSide Side
		var accountBookID, accountCurrency, accountStatus, purpose string
		if err := tx.QueryRow(ctx, `SELECT normal_side,book_id,currency,status,purpose FROM ledger_accounts WHERE id=$1 FOR UPDATE`, accountID).
			Scan(&normalSide, &accountBookID, &accountCurrency, &accountStatus, &purpose); err != nil {
			return PostedJournal{}, err
		}
		if accountBookID != command.BookID {
			return PostedJournal{}, ErrAccountBookMismatch
		}
		if accountCurrency != command.Currency {
			return PostedJournal{}, ErrAccountCurrencyMismatch
		}
		if accountStatus == "closed" {
			return PostedJournal{}, ErrAccountClosed
		}
		normalSides[accountID] = normalSide
		purposes[accountID] = purpose
		var balance projectedBalance
		err := tx.QueryRow(ctx, `SELECT posted_debits_minor,posted_credits_minor FROM ledger_balances WHERE account_id=$1`, accountID).
			Scan(&balance.debits, &balance.credits)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return PostedJournal{}, err
		}
		projectedBalances[accountID] = balance
	}

	for _, posting := range command.Postings {
		balance := projectedBalances[posting.AccountID]
		if posting.Side == Debit {
			balance.debits += posting.AmountMinor
		} else {
			balance.credits += posting.AmountMinor
		}
		projectedBalances[posting.AccountID] = balance
		projectedAmount := balance.debits - balance.credits
		if normalSides[posting.AccountID] == Credit {
			projectedAmount = balance.credits - balance.debits
		}
		if (purposes[posting.AccountID] == "available" || purposes[posting.AccountID] == "held") && projectedAmount < 0 {
			return PostedJournal{}, ErrInsufficientFunds
		}
	}

	for index, posting := range command.Postings {
		_, err := tx.Exec(ctx, `INSERT INTO ledger_postings
			(journal_id,sequence_no,account_id,side,amount_minor,currency)
			VALUES($1,$2,$3,$4,$5,$6)`,
			command.JournalID, index+1, posting.AccountID, posting.Side, posting.AmountMinor, command.Currency)
		if err != nil {
			return PostedJournal{}, err
		}
		debit, credit := int64(0), int64(0)
		if posting.Side == Debit {
			debit = posting.AmountMinor
		} else {
			credit = posting.AmountMinor
		}
		_, err = tx.Exec(ctx, `INSERT INTO ledger_balances
			(account_id,posted_debits_minor,posted_credits_minor,version)
			VALUES($1,$2,$3,1)
			ON CONFLICT(account_id) DO UPDATE SET
			posted_debits_minor=ledger_balances.posted_debits_minor+EXCLUDED.posted_debits_minor,
			posted_credits_minor=ledger_balances.posted_credits_minor+EXCLUDED.posted_credits_minor,
			version=ledger_balances.version+1,
			updated_at=NOW()`, posting.AccountID, debit, credit)
		if err != nil {
			return PostedJournal{}, err
		}
	}

	for _, accountID := range accountIDs {
		if _, err := tx.Exec(ctx, `UPDATE ledger_accounts SET version=version+1,updated_at=NOW() WHERE id=$1`, accountID); err != nil {
			return PostedJournal{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE ledger_journals SET status='posted',posted_at=$2 WHERE id=$1`, command.JournalID, s.now().Unix()); err != nil {
		return PostedJournal{}, err
	}

	result := PostedJournal{JournalID: command.JournalID, Status: "posted", PostingCount: len(command.Postings)}
	for _, accountID := range accountIDs {
		var balance AccountBalance
		balance.AccountID = accountID
		if err := tx.QueryRow(ctx, `SELECT posted_debits_minor,posted_credits_minor,version FROM ledger_balances WHERE account_id=$1`, accountID).
			Scan(&balance.PostedDebitsMinor, &balance.PostedCreditsMinor, &balance.Version); err != nil {
			return PostedJournal{}, err
		}
		if normalSides[accountID] == Debit {
			balance.BalanceMinor = balance.PostedDebitsMinor - balance.PostedCreditsMinor
		} else {
			balance.BalanceMinor = balance.PostedCreditsMinor - balance.PostedDebitsMinor
		}
		result.Balances = append(result.Balances, balance)
	}
	return result, nil
}

func journalFingerprint(command PostJournalCommand) (string, error) {
	canonical := struct {
		BookID, BusinessType, BusinessID, EventType, Currency string
		ReversalOf                                            string
		Postings                                              []Posting
	}{
		BookID:       command.BookID,
		BusinessType: command.BusinessType,
		BusinessID:   command.BusinessID,
		EventType:    command.EventType,
		Currency:     command.Currency,
		ReversalOf:   command.ReversalOf,
		Postings:     command.Postings,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func legacyJournalFingerprint(command PostJournalCommand, effectiveAt int64) (string, error) {
	canonical := struct {
		BookID, BusinessType, BusinessID, EventType, Currency string
		EffectiveAt                                           int64
		ReversalOf                                            string
		Postings                                              []Posting
	}{
		BookID:       command.BookID,
		BusinessType: command.BusinessType,
		BusinessID:   command.BusinessID,
		EventType:    command.EventType,
		Currency:     command.Currency,
		EffectiveAt:  effectiveAt,
		ReversalOf:   command.ReversalOf,
		Postings:     command.Postings,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func loadPostedJournal(ctx context.Context, tx pgx.Tx, journalID string) (PostedJournal, error) {
	result := PostedJournal{JournalID: journalID}
	if err := tx.QueryRow(ctx, `SELECT status,
		(SELECT count(*) FROM ledger_postings WHERE journal_id=ledger_journals.id)
		FROM ledger_journals WHERE id=$1`, journalID).
		Scan(&result.Status, &result.PostingCount); err != nil {
		return PostedJournal{}, err
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT
		a.id,a.normal_side,b.posted_debits_minor,b.posted_credits_minor,b.version
		FROM ledger_postings p
		JOIN ledger_accounts a ON a.id=p.account_id
		JOIN ledger_balances b ON b.account_id=a.id
		WHERE p.journal_id=$1
		ORDER BY a.id`, journalID)
	if err != nil {
		return PostedJournal{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var balance AccountBalance
		var normalSide Side
		if err := rows.Scan(&balance.AccountID, &normalSide, &balance.PostedDebitsMinor, &balance.PostedCreditsMinor, &balance.Version); err != nil {
			return PostedJournal{}, err
		}
		if normalSide == Debit {
			balance.BalanceMinor = balance.PostedDebitsMinor - balance.PostedCreditsMinor
		} else {
			balance.BalanceMinor = balance.PostedCreditsMinor - balance.PostedDebitsMinor
		}
		result.Balances = append(result.Balances, balance)
	}
	if err := rows.Err(); err != nil {
		return PostedJournal{}, err
	}
	return result, nil
}

func validateJournalBalance(postings []Posting) error {
	if len(postings) < 2 {
		return ErrUnbalancedJournal
	}
	var debits, credits int64
	for _, posting := range postings {
		if posting.AmountMinor <= 0 {
			return ErrUnbalancedJournal
		}
		switch posting.Side {
		case Debit:
			debits += posting.AmountMinor
		case Credit:
			credits += posting.AmountMinor
		default:
			return ErrUnbalancedJournal
		}
	}
	if debits != credits {
		return ErrUnbalancedJournal
	}
	return nil
}
