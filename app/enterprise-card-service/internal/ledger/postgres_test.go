package ledger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBalancedJournalPostsAtomically(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	result, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID:    "journal-funding-001",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-001",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 100000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 100000},
		},
	})
	if err != nil {
		t.Fatalf("post balanced journal: %v", err)
	}
	if result.JournalID != "journal-funding-001" || result.Status != "posted" || result.PostingCount != 2 {
		t.Fatalf("posted journal = %#v", result)
	}

	assertBalance(t, result, "platform-cash-usd", 100000, 0, 100000, 1)
	assertBalance(t, result, "org-main-available-usd", 0, 100000, 100000, 1)
}

func TestUnbalancedJournalIsRejectedWithoutFinancialEffect(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	_, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID:    "journal-unbalanced-001",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-unbalanced-001",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 100000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 99999},
		},
	})
	if !errors.Is(err, ErrUnbalancedJournal) {
		t.Fatalf("post unbalanced journal error = %v, want %v", err, ErrUnbalancedJournal)
	}

	result, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID:    "journal-funding-after-rejection-001",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-after-rejection-001",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_001,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 100000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 100000},
		},
	})
	if err != nil {
		t.Fatalf("post balanced journal after rejection: %v", err)
	}
	assertBalance(t, result, "platform-cash-usd", 100000, 0, 100000, 1)
	assertBalance(t, result, "org-main-available-usd", 0, 100000, 100000, 1)
}

func TestExactJournalRetryReturnsOriginalResultWithoutPostingTwice(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	command := PostJournalCommand{
		JournalID:    "journal-funding-retry-001",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-retry-001",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 100000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 100000},
		},
	}

	first, err := store.PostJournal(context.Background(), command)
	if err != nil {
		t.Fatalf("first post journal: %v", err)
	}
	second, err := store.PostJournal(context.Background(), command)
	if err != nil {
		t.Fatalf("retry identical journal: %v", err)
	}

	if second.JournalID != first.JournalID || second.Status != "posted" || second.PostingCount != 2 {
		t.Fatalf("retry result = %#v, first result = %#v", second, first)
	}
	assertBalance(t, second, "platform-cash-usd", 100000, 0, 100000, 1)
	assertBalance(t, second, "org-main-available-usd", 0, 100000, 100000, 1)
}

func TestExactBusinessRetryIgnoresServerGeneratedEffectiveAt(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	original := PostJournalCommand{
		JournalID:    "journal-effective-at-retry-original",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-effective-at-retry-001",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 100000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 100000},
		},
	}
	first, err := store.PostJournal(context.Background(), original)
	if err != nil {
		t.Fatalf("post original journal: %v", err)
	}

	retry := original
	retry.JournalID = "journal-effective-at-retry-regenerated"
	retry.EffectiveAt++
	second, err := store.PostJournal(context.Background(), retry)
	if err != nil {
		t.Fatalf("retry identical business command one second later: %v", err)
	}
	if second.JournalID != first.JournalID {
		t.Fatalf("retry journal id = %q, want original %q", second.JournalID, first.JournalID)
	}
	assertBalance(t, second, "platform-cash-usd", 100000, 0, 100000, 1)
	assertBalance(t, second, "org-main-available-usd", 0, 100000, 100000, 1)
}

func TestLegacyFingerprintCompatibilityUsesPersistedEffectiveAt(t *testing.T) {
	original := PostJournalCommand{
		JournalID:    "journal-legacy-fingerprint-original",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-legacy-fingerprint-001",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 100000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 100000},
		},
	}
	storedFingerprint, err := legacyJournalFingerprint(original, original.EffectiveAt)
	if err != nil {
		t.Fatalf("legacy fingerprint: %v", err)
	}

	retry := original
	retry.JournalID = "journal-legacy-fingerprint-regenerated"
	retry.EffectiveAt++
	retryFingerprint, err := legacyJournalFingerprint(retry, original.EffectiveAt)
	if err != nil {
		t.Fatalf("legacy retry fingerprint: %v", err)
	}
	if retryFingerprint != storedFingerprint {
		t.Fatalf("legacy retry fingerprint = %q, want %q", retryFingerprint, storedFingerprint)
	}

	conflict := retry
	conflict.Postings = []Posting{
		{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 200000},
		{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 200000},
	}
	conflictFingerprint, err := legacyJournalFingerprint(conflict, original.EffectiveAt)
	if err != nil {
		t.Fatalf("legacy conflicting fingerprint: %v", err)
	}
	if conflictFingerprint == storedFingerprint {
		t.Fatal("legacy fingerprint accepted a materially different command")
	}
}

func TestReusedBusinessKeyWithDifferentJournalIsRejected(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	original := PostJournalCommand{
		JournalID:    "journal-funding-conflict-original",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-conflict-001",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 100000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 100000},
		},
	}
	if _, err := store.PostJournal(context.Background(), original); err != nil {
		t.Fatalf("post original journal: %v", err)
	}

	conflicting := original
	conflicting.JournalID = "journal-funding-conflict-changed"
	conflicting.Postings = []Posting{
		{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 200000},
		{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 200000},
	}
	_, err := store.PostJournal(context.Background(), conflicting)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("post conflicting journal error = %v, want %v", err, ErrIdempotencyConflict)
	}

	replayed, err := store.PostJournal(context.Background(), original)
	if err != nil {
		t.Fatalf("replay original journal after conflict: %v", err)
	}
	assertBalance(t, replayed, "platform-cash-usd", 100000, 0, 100000, 1)
	assertBalance(t, replayed, "org-main-available-usd", 0, 100000, 100000, 1)
}

func TestExactBusinessRetryIgnoresCallerGeneratedJournalID(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	original := PostJournalCommand{
		JournalID:    "journal-business-retry-original",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-business-retry-001",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 100000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 100000},
		},
	}
	first, err := store.PostJournal(context.Background(), original)
	if err != nil {
		t.Fatalf("post original journal: %v", err)
	}

	retry := original
	retry.JournalID = "journal-business-retry-regenerated"
	second, err := store.PostJournal(context.Background(), retry)
	if err != nil {
		t.Fatalf("retry business command with regenerated journal id: %v", err)
	}
	if second.JournalID != first.JournalID {
		t.Fatalf("retry journal id = %q, want original %q", second.JournalID, first.JournalID)
	}
	assertBalance(t, second, "platform-cash-usd", 100000, 0, 100000, 1)
	assertBalance(t, second, "org-main-available-usd", 0, 100000, 100000, 1)
}

func TestConcurrentExactBusinessRetriesPostOnce(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	base := PostJournalCommand{
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-concurrent-001",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 100000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 100000},
		},
	}

	start := make(chan struct{})
	results := make(chan PostedJournal, 2)
	errorsFound := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, journalID := range []string{"journal-concurrent-a", "journal-concurrent-b"} {
		journalID := journalID
		go func() {
			command := base
			command.JournalID = journalID
			ready.Done()
			<-start
			result, err := store.PostJournal(context.Background(), command)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- result
		}()
	}
	ready.Wait()
	close(start)

	got := make([]PostedJournal, 0, 2)
	for range 2 {
		select {
		case err := <-errorsFound:
			t.Fatalf("concurrent post journal: %v", err)
		case result := <-results:
			got = append(got, result)
		}
	}
	if got[0].JournalID != got[1].JournalID {
		t.Fatalf("concurrent journal ids = %q and %q, want the same persisted journal", got[0].JournalID, got[1].JournalID)
	}
	for _, result := range got {
		assertBalance(t, result, "platform-cash-usd", 100000, 0, 100000, 1)
		assertBalance(t, result, "org-main-available-usd", 0, 100000, 100000, 1)
	}
}

func TestJournalRejectsAccountFromAnotherBook(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO ledger_books(id,legal_entity_id,name,base_currency,status)
		VALUES('book-other','entity-other','Other Book','USD','active');
		INSERT INTO ledger_accounts
			(id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status)
		VALUES
			('other-org-available-usd','book-other','other.org.available.usd','other-org-usd','liability','credit','organization','org-other','USD','available','active');
	`); err != nil {
		t.Fatalf("seed account from another book: %v", err)
	}

	_, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID:    "journal-cross-book-001",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-cross-book-001",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 100000},
			{AccountID: "other-org-available-usd", Side: Credit, AmountMinor: 100000},
		},
	})
	if !errors.Is(err, ErrAccountBookMismatch) {
		t.Fatalf("post cross-book journal error = %v, want %v", err, ErrAccountBookMismatch)
	}

	result, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID:    "journal-after-cross-book-rejection",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-after-cross-book-rejection",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_001,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 100000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 100000},
		},
	})
	if err != nil {
		t.Fatalf("post valid journal after cross-book rejection: %v", err)
	}
	assertBalance(t, result, "platform-cash-usd", 100000, 0, 100000, 1)
	assertBalance(t, result, "org-main-available-usd", 0, 100000, 100000, 1)
}

func TestJournalRejectsAccountWithAnotherCurrency(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO ledger_accounts
			(id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status)
		VALUES
			('org-main-available-eur','book-local','org.main.available.eur','org-main-eur','liability','credit','organization','org-local','EUR','available','active');
	`); err != nil {
		t.Fatalf("seed EUR account: %v", err)
	}

	_, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID:    "journal-cross-currency-001",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-cross-currency-001",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 100000},
			{AccountID: "org-main-available-eur", Side: Credit, AmountMinor: 100000},
		},
	})
	if !errors.Is(err, ErrAccountCurrencyMismatch) {
		t.Fatalf("post cross-currency journal error = %v, want %v", err, ErrAccountCurrencyMismatch)
	}

	result, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID:    "journal-after-cross-currency-rejection",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-after-cross-currency-rejection",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_001,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 100000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 100000},
		},
	})
	if err != nil {
		t.Fatalf("post valid journal after cross-currency rejection: %v", err)
	}
	assertBalance(t, result, "platform-cash-usd", 100000, 0, 100000, 1)
	assertBalance(t, result, "org-main-available-usd", 0, 100000, 100000, 1)
}

func TestJournalRejectsClosedAccount(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		UPDATE ledger_accounts SET status='closed' WHERE id='org-main-available-usd'
	`); err != nil {
		t.Fatalf("close account: %v", err)
	}

	_, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID:    "journal-closed-account-001",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-closed-account-001",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 100000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 100000},
		},
	})
	if !errors.Is(err, ErrAccountClosed) {
		t.Fatalf("post to closed account error = %v, want %v", err, ErrAccountClosed)
	}
}

func TestZeroBalanceAccountCanBeClosed(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	if err := store.CloseAccount(context.Background(), "org-main-available-usd"); err != nil {
		t.Fatalf("close zero-balance account: %v", err)
	}

	_, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID:    "journal-after-account-close",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-after-account-close",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 10000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 10000},
		},
	})
	if !errors.Is(err, ErrAccountClosed) {
		t.Fatalf("post after account close error = %v, want %v", err, ErrAccountClosed)
	}
}

func TestAccountWithNonZeroBalanceCannotBeClosed(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID:    "journal-fund-before-close",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-before-close",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 10000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 10000},
		},
	}); err != nil {
		t.Fatalf("fund account before close attempt: %v", err)
	}

	err := store.CloseAccount(context.Background(), "org-main-available-usd")
	if !errors.Is(err, ErrAccountHasBalance) {
		t.Fatalf("close non-zero account error = %v, want %v", err, ErrAccountHasBalance)
	}

	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM ledger_accounts WHERE id='org-main-available-usd'`).Scan(&status); err != nil {
		t.Fatalf("read account status after rejected close: %v", err)
	}
	if status != "active" {
		t.Fatalf("account status after rejected close = %s, want active", status)
	}
}

func TestAccountWithActiveCardFundingSourceCannotBeClosed(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-close-guard', 'org-local', 'user-local', 'Close Guard Card', 'active', 'USD', 1800000000
		);
		INSERT INTO card_funding_sources(
			id, card_id, account_id, priority, spending_limit_minor, status,
			effective_from, effective_to
		) VALUES (
			'funding-source-close-guard', 'card-close-guard', 'org-main-available-usd',
			0, 0, 'active', 0, 0
		);
	`); err != nil {
		t.Fatalf("seed active card funding source: %v", err)
	}

	err := store.CloseAccount(context.Background(), "org-main-available-usd")
	if err == nil || err.Error() != "ledger account has an active card funding source" {
		t.Fatalf("close bound account error = %v, want active funding source rejection", err)
	}
}

func TestAccountWithOutstandingAuthorizationCannotBeClosed(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency,
			provider_card_id, created_at
		) VALUES (
			'card-outstanding-authorization', 'org-local', 'user-local',
			'Outstanding Authorization Card', 'active', 'USD',
			'provider-card-outstanding-authorization', 1800000000
		);
		INSERT INTO card_funding_sources(
			id, card_id, account_id, priority, spending_limit_minor, status,
			effective_from, effective_to
		) VALUES (
			'funding-source-outstanding-authorization', 'card-outstanding-authorization',
			'org-main-available-usd', 0, 0, 'inactive', 0, 0
		);
		INSERT INTO card_lab_authorizations(
			authorization_id, provider_card_id, card_id, currency, amount_minor,
			decision, posted_balance_after, held_amount_after,
			available_balance_after, authorization_status
		) VALUES (
			'authorization-outstanding-close-guard',
			'provider-card-outstanding-authorization',
			'card-outstanding-authorization', 'USD', 1000,
			'approved', 1000, 1000, 0, 'authorized'
		);
	`); err != nil {
		t.Fatalf("seed outstanding authorization: %v", err)
	}

	err := store.CloseAccount(context.Background(), "org-main-available-usd")
	if err == nil || err.Error() != "ledger account has an outstanding authorization" {
		t.Fatalf("close account with outstanding authorization error = %v, want outstanding authorization rejection", err)
	}
}

func TestAccountCloseAndPostingCannotBothSucceedConcurrently(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	start := make(chan struct{})
	closeResult := make(chan error, 1)
	postResult := make(chan error, 1)
	var ready sync.WaitGroup
	ready.Add(2)

	go func() {
		ready.Done()
		<-start
		closeResult <- store.CloseAccount(context.Background(), "org-main-available-usd")
	}()
	go func() {
		ready.Done()
		<-start
		_, err := store.PostJournal(context.Background(), PostJournalCommand{
			JournalID:    "journal-concurrent-account-close",
			BookID:       "book-local",
			BusinessType: "funding",
			BusinessID:   "funding-concurrent-account-close",
			EventType:    "funding",
			Currency:     "USD",
			EffectiveAt:  1_800_000_000,
			Postings: []Posting{
				{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 10000},
				{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 10000},
			},
		})
		postResult <- err
	}()

	ready.Wait()
	close(start)
	closeErr := <-closeResult
	postErr := <-postResult

	switch {
	case closeErr == nil && errors.Is(postErr, ErrAccountClosed):
	case postErr == nil && errors.Is(closeErr, ErrAccountHasBalance):
	default:
		t.Fatalf("concurrent close/post results: close=%v post=%v", closeErr, postErr)
	}
}

func TestNonZeroAccountCannotBeClosedThroughDirectSQL(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID:    "journal-fund-before-direct-close",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-before-direct-close",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 10000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 10000},
		},
	}); err != nil {
		t.Fatalf("fund account before direct close: %v", err)
	}

	_, err := pool.Exec(context.Background(), `
		UPDATE ledger_accounts
		SET status='closed'
		WHERE id='org-main-available-usd'
	`)
	if err == nil || !strings.Contains(err.Error(), "non-zero ledger account cannot be closed") {
		t.Fatalf("direct close error = %v, want non-zero balance rejection", err)
	}
}

func TestBoundAccountCannotBeClosedThroughDirectSQL(t *testing.T) {
	_, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-direct-close-guard', 'org-local', 'user-local',
			'Direct Close Guard Card', 'active', 'USD', 1800000000
		);
		INSERT INTO card_funding_sources(
			id, card_id, account_id, priority, spending_limit_minor, status,
			effective_from, effective_to
		) VALUES (
			'funding-source-direct-close-guard', 'card-direct-close-guard',
			'org-main-available-usd', 0, 0, 'active', 0, 0
		);
	`); err != nil {
		t.Fatalf("seed direct close funding source: %v", err)
	}

	_, err := pool.Exec(context.Background(), `
		UPDATE ledger_accounts
		SET status='closed'
		WHERE id='org-main-available-usd'
	`)
	if err == nil || !strings.Contains(err.Error(), "ledger account with active funding source cannot be closed") {
		t.Fatalf("direct close bound account error = %v, want active funding source rejection", err)
	}
}

func TestAccountWithOutstandingAuthorizationCannotBeClosedThroughDirectSQL(t *testing.T) {
	_, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency,
			provider_card_id, created_at
		) VALUES (
			'card-direct-auth-close-guard', 'org-local', 'user-local',
			'Direct Authorization Close Guard', 'active', 'USD',
			'provider-card-direct-auth-close-guard', 1800000000
		);
		INSERT INTO card_funding_sources(
			id, card_id, account_id, priority, spending_limit_minor, status,
			effective_from, effective_to
		) VALUES (
			'funding-source-direct-auth-close-guard', 'card-direct-auth-close-guard',
			'org-main-available-usd', 0, 0, 'inactive', 0, 0
		);
		INSERT INTO card_lab_authorizations(
			authorization_id, provider_card_id, card_id, currency, amount_minor,
			decision, posted_balance_after, held_amount_after,
			available_balance_after, authorization_status
		) VALUES (
			'authorization-direct-close-guard',
			'provider-card-direct-auth-close-guard',
			'card-direct-auth-close-guard', 'USD', 1000,
			'approved', 1000, 1000, 0, 'authorized'
		);
	`); err != nil {
		t.Fatalf("seed direct close outstanding authorization: %v", err)
	}

	_, err := pool.Exec(context.Background(), `
		UPDATE ledger_accounts
		SET status='closed'
		WHERE id='org-main-available-usd'
	`)
	if err == nil || !strings.Contains(err.Error(), "ledger account with outstanding authorization cannot be closed") {
		t.Fatalf("direct close authorized account error = %v, want outstanding authorization rejection", err)
	}
}

func TestAvailableAccountCanBeBoundAsCardFundingSource(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-bind-funding-source', 'org-local', 'user-local',
			'Bind Funding Source Card', 'active', 'USD', 1800000000
		)
	`); err != nil {
		t.Fatalf("seed card for funding source binding: %v", err)
	}

	err := store.BindFundingSource(context.Background(), BindFundingSourceCommand{
		BindingID: "funding-source-bind-success",
		CardID:    "card-bind-funding-source",
		AccountID: "org-main-available-usd",
	})
	if err != nil {
		t.Fatalf("bind available account as funding source: %v", err)
	}

	err = store.CloseAccount(context.Background(), "org-main-available-usd")
	if !errors.Is(err, ErrAccountHasActiveFundingSource) {
		t.Fatalf("close newly bound account error = %v, want %v", err, ErrAccountHasActiveFundingSource)
	}
}

func TestFundingPoolCanBeProvisionedIdempotentlyWithoutSchemaChange(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	command := ProvisionFundingPoolCommand{
		PoolID:             "org-engineering-usd",
		BookID:             "book-local",
		OwnerID:            "org-local",
		Currency:           "USD",
		AccountKeyPrefix:   "org.engineering.usd",
		AvailableAccountID: "org-engineering-available-usd",
		HeldAccountID:      "org-engineering-held-usd",
	}
	first, err := store.ProvisionFundingPool(context.Background(), command)
	if err != nil {
		t.Fatalf("provision funding pool: %v", err)
	}
	second, err := store.ProvisionFundingPool(context.Background(), command)
	if err != nil {
		t.Fatalf("repeat funding pool provisioning: %v", err)
	}
	if first != second || first.PoolID != command.PoolID || first.AvailableAccountID != command.AvailableAccountID || first.HeldAccountID != command.HeldAccountID {
		t.Fatalf("provisioned funding pools: first=%#v second=%#v", first, second)
	}

	var accountCount, balanceCount int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM ledger_accounts WHERE account_group_id='org-engineering-usd'`).Scan(&accountCount); err != nil {
		t.Fatalf("count provisioned accounts: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM ledger_balances
		WHERE account_id IN ('org-engineering-available-usd','org-engineering-held-usd')`).Scan(&balanceCount); err != nil {
		t.Fatalf("count provisioned balances: %v", err)
	}
	if accountCount != 2 || balanceCount != 2 {
		t.Fatalf("provisioned rows: accounts=%d balances=%d, want 2 and 2", accountCount, balanceCount)
	}
}

func TestFundingPoolAccountKeyConflictIsRejected(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	original := ProvisionFundingPoolCommand{
		PoolID: "org-sales-usd", BookID: "book-local", OwnerID: "org-local", Currency: "USD",
		AccountKeyPrefix: "org.sales.usd", AvailableAccountID: "org-sales-available-usd", HeldAccountID: "org-sales-held-usd",
	}
	if _, err := store.ProvisionFundingPool(context.Background(), original); err != nil {
		t.Fatalf("provision original funding pool: %v", err)
	}
	conflict := original
	conflict.OwnerID = "org-other"
	if _, err := store.ProvisionFundingPool(context.Background(), conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting funding pool error = %v, want %v", err, ErrIdempotencyConflict)
	}
}

func TestHeldAccountCannotBeBoundAsCardFundingSource(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-bind-held-account', 'org-local', 'user-local',
			'Bind Held Account Card', 'active', 'USD', 1800000000
		)
	`); err != nil {
		t.Fatalf("seed card for held account binding: %v", err)
	}

	err := store.BindFundingSource(context.Background(), BindFundingSourceCommand{
		BindingID: "funding-source-held-account",
		CardID:    "card-bind-held-account",
		AccountID: "org-main-held-usd",
	})
	if !errors.Is(err, ErrFundingSourceAccountPurpose) {
		t.Fatalf("bind held account error = %v, want %v", err, ErrFundingSourceAccountPurpose)
	}
}

func TestCardCannotBindFundingSourceWithAnotherCurrency(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO ledger_accounts
			(id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status)
		VALUES
			('org-main-available-eur','book-local','org.main.available.eur','org-main-eur','liability','credit','organization','org-local','EUR','available','active');
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-bind-currency-mismatch', 'org-local', 'user-local',
			'Currency Mismatch Card', 'active', 'USD', 1800000000
		);
	`); err != nil {
		t.Fatalf("seed currency mismatch binding: %v", err)
	}

	err := store.BindFundingSource(context.Background(), BindFundingSourceCommand{
		BindingID: "funding-source-currency-mismatch",
		CardID:    "card-bind-currency-mismatch",
		AccountID: "org-main-available-eur",
	})
	if !errors.Is(err, ErrFundingSourceCurrencyMismatch) {
		t.Fatalf("bind another currency error = %v, want %v", err, ErrFundingSourceCurrencyMismatch)
	}
}

func TestClosedAccountCannotBeBoundAsCardFundingSource(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		UPDATE ledger_accounts SET status='closed' WHERE id='org-main-available-usd';
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-bind-closed-account', 'org-local', 'user-local',
			'Closed Account Card', 'active', 'USD', 1800000000
		);
	`); err != nil {
		t.Fatalf("seed closed account binding: %v", err)
	}

	err := store.BindFundingSource(context.Background(), BindFundingSourceCommand{
		BindingID: "funding-source-closed-account",
		CardID:    "card-bind-closed-account",
		AccountID: "org-main-available-usd",
	})
	if !errors.Is(err, ErrAccountClosed) {
		t.Fatalf("bind closed account error = %v, want %v", err, ErrAccountClosed)
	}
}

func TestCardCannotBindAnotherOrganizationsFundingSource(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO ledger_accounts
			(id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status)
		VALUES
			('org-other-available-usd','book-local','org.other.available.usd','org-other-usd','liability','credit','organization','org-other','USD','available','active');
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-owner-mismatch', 'org-local', 'user-local',
			'Owner Mismatch Card', 'active', 'USD', 1800000000
		);
	`); err != nil {
		t.Fatalf("seed owner mismatch binding: %v", err)
	}

	err := store.BindFundingSource(context.Background(), BindFundingSourceCommand{
		BindingID: "funding-source-owner-mismatch",
		CardID:    "card-owner-mismatch",
		AccountID: "org-other-available-usd",
	})
	if !errors.Is(err, ErrFundingSourceOwnerMismatch) {
		t.Fatalf("bind another organization's account error = %v, want %v", err, ErrFundingSourceOwnerMismatch)
	}

	var bindingCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM card_funding_sources WHERE id='funding-source-owner-mismatch'
	`).Scan(&bindingCount); err != nil {
		t.Fatalf("count rejected owner mismatch binding: %v", err)
	}
	if bindingCount != 0 {
		t.Fatalf("rejected owner mismatch binding count = %d, want 0", bindingCount)
	}
}

func TestAvailableAccountWithoutHeldPairCannotBeBoundAsFundingSource(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO ledger_accounts
			(id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status)
		VALUES
			('org-unpaired-available-usd','book-local','org.unpaired.available.usd','org-unpaired-usd','liability','credit','organization','org-local','USD','available','active');
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-unpaired-funding-source', 'org-local', 'user-local',
			'Unpaired Funding Source Card', 'active', 'USD', 1800000000
		);
	`); err != nil {
		t.Fatalf("seed unpaired funding source: %v", err)
	}

	err := store.BindFundingSource(context.Background(), BindFundingSourceCommand{
		BindingID: "funding-source-unpaired",
		CardID:    "card-unpaired-funding-source",
		AccountID: "org-unpaired-available-usd",
	})
	if !errors.Is(err, ErrFundingSourceHeldAccountMissing) {
		t.Fatalf("bind unpaired available account error = %v, want %v", err, ErrFundingSourceHeldAccountMissing)
	}
}

func TestCardCannotHaveTwoActiveFundingSources(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO ledger_accounts
			(id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status)
		VALUES
			('org-budget-available-usd','book-local','org.budget.available.usd','org-budget-usd','liability','credit','organization','org-local','USD','available','active'),
			('org-budget-held-usd','book-local','org.budget.held.usd','org-budget-usd','liability','credit','organization','org-local','USD','held','active');
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-double-funding-source', 'org-local', 'user-local',
			'Double Funding Source Card', 'active', 'USD', 1800000000
		);
	`); err != nil {
		t.Fatalf("seed card with two candidate accounts: %v", err)
	}

	if err := store.BindFundingSource(context.Background(), BindFundingSourceCommand{
		BindingID: "funding-source-first",
		CardID:    "card-double-funding-source",
		AccountID: "org-main-available-usd",
	}); err != nil {
		t.Fatalf("bind first funding source: %v", err)
	}
	err := store.BindFundingSource(context.Background(), BindFundingSourceCommand{
		BindingID: "funding-source-second",
		CardID:    "card-double-funding-source",
		AccountID: "org-budget-available-usd",
	})
	if !errors.Is(err, ErrCardHasActiveFundingSource) {
		t.Fatalf("bind second active funding source error = %v, want %v", err, ErrCardHasActiveFundingSource)
	}
}

func TestConcurrentFundingSourceBindingsAllowOnlyOneActiveSource(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO ledger_accounts
			(id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status)
		VALUES
			('org-concurrent-available-usd','book-local','org.concurrent.available.usd','org-concurrent-usd','liability','credit','organization','org-local','USD','available','active'),
			('org-concurrent-held-usd','book-local','org.concurrent.held.usd','org-concurrent-usd','liability','credit','organization','org-local','USD','held','active');
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-concurrent-funding-source', 'org-local', 'user-local',
			'Concurrent Funding Source Card', 'active', 'USD', 1800000000
		);
	`); err != nil {
		t.Fatalf("seed concurrent funding source candidates: %v", err)
	}

	type binding struct {
		id        string
		accountID string
	}
	bindings := []binding{
		{id: "funding-source-concurrent-a", accountID: "org-main-available-usd"},
		{id: "funding-source-concurrent-b", accountID: "org-concurrent-available-usd"},
	}
	start := make(chan struct{})
	outcomes := make(chan error, len(bindings))
	var ready sync.WaitGroup
	ready.Add(len(bindings))
	for _, candidate := range bindings {
		candidate := candidate
		go func() {
			ready.Done()
			<-start
			outcomes <- store.BindFundingSource(context.Background(), BindFundingSourceCommand{
				BindingID: candidate.id,
				CardID:    "card-concurrent-funding-source",
				AccountID: candidate.accountID,
			})
		}()
	}
	ready.Wait()
	close(start)

	var succeeded, rejected int
	for range bindings {
		err := <-outcomes
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrCardHasActiveFundingSource):
			rejected++
		default:
			t.Fatalf("unexpected concurrent binding error: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent bindings: succeeded=%d rejected=%d, want 1 and 1", succeeded, rejected)
	}
}

func TestHeldAccountCannotBeBoundThroughDirectSQL(t *testing.T) {
	_, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-direct-held-binding', 'org-local', 'user-local',
			'Direct Held Binding Card', 'active', 'USD', 1800000000
		)
	`); err != nil {
		t.Fatalf("seed card for direct held binding: %v", err)
	}

	_, err := pool.Exec(context.Background(), `
		INSERT INTO card_funding_sources(
			id, card_id, account_id, priority, spending_limit_minor, status,
			effective_from, effective_to
		) VALUES (
			'funding-source-direct-held', 'card-direct-held-binding',
			'org-main-held-usd', 0, 0, 'active', 0, 0
		)
	`)
	if err == nil || !strings.Contains(err.Error(), "funding source account must have available purpose") {
		t.Fatalf("direct held binding error = %v, want available purpose rejection", err)
	}
}

func TestCurrencyMismatchFundingSourceCannotBeBoundThroughDirectSQL(t *testing.T) {
	_, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO ledger_accounts
			(id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status)
		VALUES
			('org-direct-available-eur','book-local','org.direct.available.eur','org-direct-eur','liability','credit','organization','org-local','EUR','available','active');
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-direct-currency-binding', 'org-local', 'user-local',
			'Direct Currency Binding Card', 'active', 'USD', 1800000000
		);
	`); err != nil {
		t.Fatalf("seed direct currency mismatch binding: %v", err)
	}

	_, err := pool.Exec(context.Background(), `
		INSERT INTO card_funding_sources(
			id, card_id, account_id, priority, spending_limit_minor, status,
			effective_from, effective_to
		) VALUES (
			'funding-source-direct-currency', 'card-direct-currency-binding',
			'org-direct-available-eur', 0, 0, 'active', 0, 0
		)
	`)
	if err == nil || !strings.Contains(err.Error(), "card and funding source account use different currencies") {
		t.Fatalf("direct currency mismatch binding error = %v, want currency mismatch rejection", err)
	}
}

func TestClosedFundingSourceAccountCannotBeBoundThroughDirectSQL(t *testing.T) {
	_, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		UPDATE ledger_accounts SET status='closed' WHERE id='org-main-available-usd';
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-direct-closed-binding', 'org-local', 'user-local',
			'Direct Closed Binding Card', 'active', 'USD', 1800000000
		);
	`); err != nil {
		t.Fatalf("seed direct closed account binding: %v", err)
	}

	_, err := pool.Exec(context.Background(), `
		INSERT INTO card_funding_sources(
			id, card_id, account_id, priority, spending_limit_minor, status,
			effective_from, effective_to
		) VALUES (
			'funding-source-direct-closed', 'card-direct-closed-binding',
			'org-main-available-usd', 0, 0, 'active', 0, 0
		)
	`)
	if err == nil || !strings.Contains(err.Error(), "closed ledger account cannot be a funding source") {
		t.Fatalf("direct closed account binding error = %v, want closed account rejection", err)
	}
}

func TestAnotherOrganizationsFundingSourceCannotBeBoundThroughDirectSQL(t *testing.T) {
	_, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO ledger_accounts
			(id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status)
		VALUES
			('org-direct-other-available-usd','book-local','org.direct.other.available.usd','org-direct-other-usd','liability','credit','organization','org-other','USD','available','active');
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-direct-owner-mismatch', 'org-local', 'user-local',
			'Direct Owner Mismatch Card', 'active', 'USD', 1800000000
		);
	`); err != nil {
		t.Fatalf("seed direct owner mismatch binding: %v", err)
	}

	_, err := pool.Exec(context.Background(), `
		INSERT INTO card_funding_sources(
			id, card_id, account_id, priority, spending_limit_minor, status,
			effective_from, effective_to
		) VALUES (
			'funding-source-direct-owner-mismatch', 'card-direct-owner-mismatch',
			'org-direct-other-available-usd', 0, 0, 'active', 0, 0
		)
	`)
	if err == nil || !strings.Contains(err.Error(), "card and funding source account belong to different organizations") {
		t.Fatalf("direct owner mismatch binding error = %v, want organization ownership rejection", err)
	}
}

func TestUnpairedAvailableAccountCannotBeBoundThroughDirectSQL(t *testing.T) {
	_, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO ledger_accounts
			(id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status)
		VALUES
			('org-direct-unpaired-available-usd','book-local','org.direct.unpaired.available.usd','org-direct-unpaired-usd','liability','credit','organization','org-local','USD','available','active');
		INSERT INTO card_lab_cards(
			id, organization_id, holder_id, alias, status, currency, created_at
		) VALUES (
			'card-direct-unpaired', 'org-local', 'user-local',
			'Direct Unpaired Card', 'active', 'USD', 1800000000
		);
	`); err != nil {
		t.Fatalf("seed direct unpaired funding source: %v", err)
	}

	_, err := pool.Exec(context.Background(), `INSERT INTO card_funding_sources(
		id,card_id,account_id,priority,spending_limit_minor,status,effective_from,effective_to
	) VALUES(
		'funding-source-direct-unpaired','card-direct-unpaired',
		'org-direct-unpaired-available-usd',0,0,'active',0,0
	)`)
	if err == nil || !strings.Contains(err.Error(), "funding source account must have a matching held account") {
		t.Fatalf("direct unpaired funding source error = %v, want matching held account rejection", err)
	}
}

func TestHoldCannotExceedAvailableFunds(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	_, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID:    "journal-fund-before-hold",
		BookID:       "book-local",
		BusinessType: "funding",
		BusinessID:   "funding-before-hold",
		EventType:    "funding",
		Currency:     "USD",
		EffectiveAt:  1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 10000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 10000},
		},
	})
	if err != nil {
		t.Fatalf("fund account before hold: %v", err)
	}

	_, err = store.PostJournal(context.Background(), PostJournalCommand{
		JournalID:    "journal-excessive-hold",
		BookID:       "book-local",
		BusinessType: "card_authorization",
		BusinessID:   "authorization-excessive-hold",
		EventType:    "hold",
		Currency:     "USD",
		EffectiveAt:  1_800_000_001,
		Postings: []Posting{
			{AccountID: "org-main-available-usd", Side: Debit, AmountMinor: 12000},
			{AccountID: "org-main-held-usd", Side: Credit, AmountMinor: 12000},
		},
	})
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("post excessive hold error = %v, want %v", err, ErrInsufficientFunds)
	}

	result, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID:    "journal-valid-hold-after-rejection",
		BookID:       "book-local",
		BusinessType: "card_authorization",
		BusinessID:   "authorization-valid-hold-after-rejection",
		EventType:    "hold",
		Currency:     "USD",
		EffectiveAt:  1_800_000_002,
		Postings: []Posting{
			{AccountID: "org-main-available-usd", Side: Debit, AmountMinor: 10000},
			{AccountID: "org-main-held-usd", Side: Credit, AmountMinor: 10000},
		},
	})
	if err != nil {
		t.Fatalf("post valid hold after rejection: %v", err)
	}
	assertBalance(t, result, "org-main-available-usd", 10000, 10000, 0, 2)
	assertBalance(t, result, "org-main-held-usd", 0, 10000, 10000, 1)
}

func TestHoldReleaseCannotExceedHeldFunds(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	commands := []PostJournalCommand{
		{
			JournalID: "journal-fund-before-release", BookID: "book-local",
			BusinessType: "funding", BusinessID: "funding-before-release", EventType: "funding",
			Currency: "USD", EffectiveAt: 1_800_000_000,
			Postings: []Posting{
				{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 10000},
				{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 10000},
			},
		},
		{
			JournalID: "journal-hold-before-release", BookID: "book-local",
			BusinessType: "card_authorization", BusinessID: "authorization-release-001", EventType: "hold",
			Currency: "USD", EffectiveAt: 1_800_000_001,
			Postings: []Posting{
				{AccountID: "org-main-available-usd", Side: Debit, AmountMinor: 6000},
				{AccountID: "org-main-held-usd", Side: Credit, AmountMinor: 6000},
			},
		},
	}
	for _, command := range commands {
		if _, err := store.PostJournal(context.Background(), command); err != nil {
			t.Fatalf("prepare held funds with %s: %v", command.EventType, err)
		}
	}

	_, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID: "journal-excessive-release", BookID: "book-local",
		BusinessType: "card_authorization", BusinessID: "authorization-release-001", EventType: "hold_release",
		Currency: "USD", EffectiveAt: 1_800_000_002,
		Postings: []Posting{
			{AccountID: "org-main-held-usd", Side: Debit, AmountMinor: 7000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 7000},
		},
	})
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("post excessive release error = %v, want %v", err, ErrInsufficientFunds)
	}

	result, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID: "journal-valid-release", BookID: "book-local",
		BusinessType: "card_authorization", BusinessID: "authorization-release-001", EventType: "hold_release",
		Currency: "USD", EffectiveAt: 1_800_000_003,
		Postings: []Posting{
			{AccountID: "org-main-held-usd", Side: Debit, AmountMinor: 6000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 6000},
		},
	})
	if err != nil {
		t.Fatalf("post valid release after rejection: %v", err)
	}
	assertBalance(t, result, "org-main-available-usd", 6000, 16000, 10000, 3)
	assertBalance(t, result, "org-main-held-usd", 6000, 6000, 0, 2)
}

func TestConcurrentHoldsCannotOverdrawAvailableFunds(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	if _, err := store.PostJournal(context.Background(), PostJournalCommand{
		JournalID: "journal-fund-before-concurrent-holds", BookID: "book-local",
		BusinessType: "funding", BusinessID: "funding-before-concurrent-holds", EventType: "funding",
		Currency: "USD", EffectiveAt: 1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 10000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 10000},
		},
	}); err != nil {
		t.Fatalf("fund account before concurrent holds: %v", err)
	}

	type outcome struct {
		result PostedJournal
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, suffix := range []string{"a", "b"} {
		suffix := suffix
		go func() {
			ready.Done()
			<-start
			result, err := store.PostJournal(context.Background(), PostJournalCommand{
				JournalID: "journal-concurrent-hold-" + suffix, BookID: "book-local",
				BusinessType: "card_authorization", BusinessID: "authorization-concurrent-" + suffix, EventType: "hold",
				Currency: "USD", EffectiveAt: 1_800_000_001,
				Postings: []Posting{
					{AccountID: "org-main-available-usd", Side: Debit, AmountMinor: 7000},
					{AccountID: "org-main-held-usd", Side: Credit, AmountMinor: 7000},
				},
			})
			outcomes <- outcome{result: result, err: err}
		}()
	}
	ready.Wait()
	close(start)

	var succeeded, rejected int
	var posted PostedJournal
	for range 2 {
		outcome := <-outcomes
		switch {
		case outcome.err == nil:
			succeeded++
			posted = outcome.result
		case errors.Is(outcome.err, ErrInsufficientFunds):
			rejected++
		default:
			t.Fatalf("unexpected concurrent hold error: %v", outcome.err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent holds: succeeded=%d rejected=%d, want 1 and 1", succeeded, rejected)
	}
	assertBalance(t, posted, "org-main-available-usd", 7000, 10000, 3000, 2)
	assertBalance(t, posted, "org-main-held-usd", 0, 7000, 7000, 1)
}

func TestPostedJournalAndPostingsRejectDirectMutation(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	command := PostJournalCommand{
		JournalID: "journal-immutable-001", BookID: "book-local",
		BusinessType: "funding", BusinessID: "funding-immutable-001", EventType: "funding",
		Currency: "USD", EffectiveAt: 1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 10000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 10000},
		},
	}
	if _, err := store.PostJournal(context.Background(), command); err != nil {
		t.Fatalf("post journal before mutation attempts: %v", err)
	}

	mutations := []struct {
		name string
		sql  string
	}{
		{"update journal", `UPDATE ledger_journals SET effective_at=1800000001 WHERE id='journal-immutable-001'`},
		{"delete journal", `DELETE FROM ledger_journals WHERE id='journal-immutable-001'`},
		{"update posting", `UPDATE ledger_postings SET amount_minor=9999 WHERE journal_id='journal-immutable-001' AND sequence_no=1`},
		{"delete posting", `DELETE FROM ledger_postings WHERE journal_id='journal-immutable-001' AND sequence_no=1`},
		{"append posting", `INSERT INTO ledger_postings(journal_id,sequence_no,account_id,side,amount_minor,currency) VALUES('journal-immutable-001',3,'platform-cash-usd','debit',1,'USD')`},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			tx, err := pool.Begin(context.Background())
			if err != nil {
				t.Fatalf("begin direct mutation transaction: %v", err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			_, err = tx.Exec(context.Background(), mutation.sql)
			if err == nil || !strings.Contains(err.Error(), "posted ledger") || !strings.Contains(err.Error(), "immutable") {
				t.Fatalf("direct mutation error = %v, want posted ledger immutable error", err)
			}
		})
	}

	result, err := store.PostJournal(context.Background(), command)
	if err != nil {
		t.Fatalf("replay immutable journal after mutation attempts: %v", err)
	}
	assertBalance(t, result, "platform-cash-usd", 10000, 0, 10000, 1)
	assertBalance(t, result, "org-main-available-usd", 0, 10000, 10000, 1)
}

func TestUnbalancedDraftJournalCannotBecomePostedThroughDirectSQL(t *testing.T) {
	_, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin direct posting transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	_, err = tx.Exec(context.Background(), `
		INSERT INTO ledger_journals(
			id, book_id, business_type, business_id, event_type,
			currency, status, effective_at
		) VALUES (
			'journal-direct-unbalanced', 'book-local', 'funding',
			'funding-direct-unbalanced', 'funding', 'USD', 'draft', 1800000000
		);
		INSERT INTO ledger_postings(
			journal_id, sequence_no, account_id, side, amount_minor, currency
		) VALUES (
			'journal-direct-unbalanced', 1, 'platform-cash-usd', 'debit', 10000, 'USD'
		);
	`)
	if err != nil {
		t.Fatalf("seed unbalanced draft journal: %v", err)
	}

	_, err = tx.Exec(context.Background(), `
		UPDATE ledger_journals
		SET status='posted', posted_at=1800000001
		WHERE id='journal-direct-unbalanced'
	`)
	if err == nil || !strings.Contains(err.Error(), "ledger journal is not balanced") {
		t.Fatalf("direct post error = %v, want ledger journal is not balanced", err)
	}
}

func TestDraftJournalPostingTransitionRequiresEqualDebitAndCreditTotals(t *testing.T) {
	_, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	t.Run("unequal totals are rejected", func(t *testing.T) {
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin unequal totals transaction: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()

		_, err = tx.Exec(context.Background(), `
			INSERT INTO ledger_journals(
				id, book_id, business_type, business_id, event_type,
				currency, status, effective_at
			) VALUES (
				'journal-direct-unequal', 'book-local', 'funding',
				'funding-direct-unequal', 'funding', 'USD', 'draft', 1800000000
			);
			INSERT INTO ledger_postings(
				journal_id, sequence_no, account_id, side, amount_minor, currency
			) VALUES
				('journal-direct-unequal', 1, 'platform-cash-usd', 'debit', 10000, 'USD'),
				('journal-direct-unequal', 2, 'org-main-available-usd', 'credit', 9999, 'USD');
		`)
		if err != nil {
			t.Fatalf("seed unequal draft journal: %v", err)
		}

		_, err = tx.Exec(context.Background(), `
			UPDATE ledger_journals
			SET status='posted', posted_at=1800000001
			WHERE id='journal-direct-unequal'
		`)
		if err == nil || !strings.Contains(err.Error(), "ledger journal is not balanced") {
			t.Fatalf("direct post error = %v, want ledger journal is not balanced", err)
		}
	})

	t.Run("equal totals are accepted", func(t *testing.T) {
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin equal totals transaction: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()

		_, err = tx.Exec(context.Background(), `
			INSERT INTO ledger_journals(
				id, book_id, business_type, business_id, event_type,
				currency, status, effective_at
			) VALUES (
				'journal-direct-balanced', 'book-local', 'funding',
				'funding-direct-balanced', 'funding', 'USD', 'draft', 1800000000
			);
			INSERT INTO ledger_postings(
				journal_id, sequence_no, account_id, side, amount_minor, currency
			) VALUES
				('journal-direct-balanced', 1, 'platform-cash-usd', 'debit', 10000, 'USD'),
				('journal-direct-balanced', 2, 'org-main-available-usd', 'credit', 10000, 'USD');
			UPDATE ledger_journals
			SET status='posted', posted_at=1800000001
			WHERE id='journal-direct-balanced';
		`)
		if err != nil {
			t.Fatalf("post balanced draft through direct SQL: %v", err)
		}
	})
}

func TestDraftJournalCannotPostAccountFromAnotherBookThroughDirectSQL(t *testing.T) {
	_, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO ledger_books(id,legal_entity_id,name,base_currency,status)
		VALUES('book-other','entity-other','Other Book','USD','active');
		INSERT INTO ledger_accounts
			(id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status)
		VALUES
			('other-org-available-usd','book-other','other.org.available.usd','other-org-usd','liability','credit','organization','org-other','USD','available','active');
	`); err != nil {
		t.Fatalf("seed account from another book: %v", err)
	}

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin cross-book direct posting transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	_, err = tx.Exec(context.Background(), `
		INSERT INTO ledger_journals(
			id, book_id, business_type, business_id, event_type,
			currency, status, effective_at
		) VALUES (
			'journal-direct-cross-book', 'book-local', 'funding',
			'funding-direct-cross-book', 'funding', 'USD', 'draft', 1800000000
		);
		INSERT INTO ledger_postings(
			journal_id, sequence_no, account_id, side, amount_minor, currency
		) VALUES
			('journal-direct-cross-book', 1, 'platform-cash-usd', 'debit', 10000, 'USD'),
			('journal-direct-cross-book', 2, 'other-org-available-usd', 'credit', 10000, 'USD');
	`)
	if err != nil {
		t.Fatalf("seed cross-book draft journal: %v", err)
	}

	_, err = tx.Exec(context.Background(), `
		UPDATE ledger_journals
		SET status='posted', posted_at=1800000001
		WHERE id='journal-direct-cross-book'
	`)
	if err == nil || !strings.Contains(err.Error(), "ledger posting account does not belong to journal book") {
		t.Fatalf("direct cross-book post error = %v, want account book mismatch", err)
	}
}

func TestDraftJournalCannotPostAccountWithAnotherCurrencyThroughDirectSQL(t *testing.T) {
	_, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO ledger_accounts
			(id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status)
		VALUES
			('org-main-available-eur','book-local','org.main.available.eur','org-main-eur','liability','credit','organization','org-local','EUR','available','active');
	`); err != nil {
		t.Fatalf("seed EUR account: %v", err)
	}

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin cross-currency direct posting transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	_, err = tx.Exec(context.Background(), `
		INSERT INTO ledger_journals(
			id, book_id, business_type, business_id, event_type,
			currency, status, effective_at
		) VALUES (
			'journal-direct-account-currency', 'book-local', 'funding',
			'funding-direct-account-currency', 'funding', 'USD', 'draft', 1800000000
		);
		INSERT INTO ledger_postings(
			journal_id, sequence_no, account_id, side, amount_minor, currency
		) VALUES
			('journal-direct-account-currency', 1, 'platform-cash-usd', 'debit', 10000, 'USD'),
			('journal-direct-account-currency', 2, 'org-main-available-eur', 'credit', 10000, 'EUR');
	`)
	if err != nil {
		t.Fatalf("seed cross-currency draft journal: %v", err)
	}

	_, err = tx.Exec(context.Background(), `
		UPDATE ledger_journals
		SET status='posted', posted_at=1800000001
		WHERE id='journal-direct-account-currency'
	`)
	if err == nil || !strings.Contains(err.Error(), "ledger posting account currency does not match journal currency") {
		t.Fatalf("direct cross-currency post error = %v, want account currency mismatch", err)
	}
}

func TestDraftJournalCannotPostPostingWithAnotherCurrencyThroughDirectSQL(t *testing.T) {
	_, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin posting-currency direct transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	_, err = tx.Exec(context.Background(), `
		INSERT INTO ledger_journals(
			id, book_id, business_type, business_id, event_type,
			currency, status, effective_at
		) VALUES (
			'journal-direct-posting-currency', 'book-local', 'funding',
			'funding-direct-posting-currency', 'funding', 'USD', 'draft', 1800000000
		);
		INSERT INTO ledger_postings(
			journal_id, sequence_no, account_id, side, amount_minor, currency
		) VALUES
			('journal-direct-posting-currency', 1, 'platform-cash-usd', 'debit', 10000, 'USD'),
			('journal-direct-posting-currency', 2, 'org-main-available-usd', 'credit', 10000, 'EUR');
	`)
	if err != nil {
		t.Fatalf("seed posting-currency draft journal: %v", err)
	}

	_, err = tx.Exec(context.Background(), `
		UPDATE ledger_journals
		SET status='posted', posted_at=1800000001
		WHERE id='journal-direct-posting-currency'
	`)
	if err == nil || !strings.Contains(err.Error(), "ledger posting currency does not match journal currency") {
		t.Fatalf("direct posting-currency error = %v, want posting currency mismatch", err)
	}
}

func TestDraftJournalCannotPostClosedAccountThroughDirectSQL(t *testing.T) {
	_, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	if _, err := pool.Exec(context.Background(), `
		UPDATE ledger_accounts
		SET status='closed'
		WHERE id='org-main-available-usd'
	`); err != nil {
		t.Fatalf("close account before direct posting: %v", err)
	}

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin closed-account direct transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	_, err = tx.Exec(context.Background(), `
		INSERT INTO ledger_journals(
			id, book_id, business_type, business_id, event_type,
			currency, status, effective_at
		) VALUES (
			'journal-direct-closed-account', 'book-local', 'funding',
			'funding-direct-closed-account', 'funding', 'USD', 'draft', 1800000000
		);
		INSERT INTO ledger_postings(
			journal_id, sequence_no, account_id, side, amount_minor, currency
		) VALUES
			('journal-direct-closed-account', 1, 'platform-cash-usd', 'debit', 10000, 'USD'),
			('journal-direct-closed-account', 2, 'org-main-available-usd', 'credit', 10000, 'USD');
	`)
	if err != nil {
		t.Fatalf("seed closed-account draft journal: %v", err)
	}

	_, err = tx.Exec(context.Background(), `
		UPDATE ledger_journals
		SET status='posted', posted_at=1800000001
		WHERE id='journal-direct-closed-account'
	`)
	if err == nil || !strings.Contains(err.Error(), "closed ledger account cannot receive postings") {
		t.Fatalf("direct closed-account post error = %v, want closed account rejection", err)
	}
}

func TestPostedJournalCanBeReversedWithOppositePostings(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	original := PostJournalCommand{
		JournalID: "journal-funding-to-reverse", BookID: "book-local",
		BusinessType: "funding", BusinessID: "funding-to-reverse", EventType: "funding",
		Currency: "USD", EffectiveAt: 1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 10000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 10000},
		},
	}
	if _, err := store.PostJournal(context.Background(), original); err != nil {
		t.Fatalf("post original journal: %v", err)
	}

	result, err := store.ReverseJournal(context.Background(), ReverseJournalCommand{
		JournalID:         "journal-funding-reversal",
		OriginalJournalID: original.JournalID,
		BusinessType:      "funding_correction",
		BusinessID:        "funding-correction-001",
		EffectiveAt:       1_800_000_001,
	})
	if err != nil {
		t.Fatalf("reverse posted journal: %v", err)
	}
	if result.JournalID != "journal-funding-reversal" || result.Status != "posted" || result.PostingCount != 2 {
		t.Fatalf("reversal result = %#v", result)
	}
	assertBalance(t, result, "platform-cash-usd", 10000, 10000, 0, 2)
	assertBalance(t, result, "org-main-available-usd", 10000, 10000, 0, 2)

	originalAfterReversal, err := store.PostJournal(context.Background(), original)
	if err != nil {
		t.Fatalf("reload original journal after reversal: %v", err)
	}
	if originalAfterReversal.JournalID != original.JournalID || originalAfterReversal.Status != "posted" || originalAfterReversal.PostingCount != 2 {
		t.Fatalf("original journal after reversal = %#v", originalAfterReversal)
	}
}

func TestPostedJournalCanOnlyBeReversedOnce(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	original := PostJournalCommand{
		JournalID: "journal-single-reversal-original", BookID: "book-local",
		BusinessType: "funding", BusinessID: "funding-single-reversal", EventType: "funding",
		Currency: "USD", EffectiveAt: 1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 10000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 10000},
		},
	}
	if _, err := store.PostJournal(context.Background(), original); err != nil {
		t.Fatalf("post original journal: %v", err)
	}
	firstReversal := ReverseJournalCommand{
		JournalID: "journal-single-reversal-first", OriginalJournalID: original.JournalID,
		BusinessType: "funding_correction", BusinessID: "funding-correction-first", EffectiveAt: 1_800_000_001,
	}
	if _, err := store.ReverseJournal(context.Background(), firstReversal); err != nil {
		t.Fatalf("post first reversal: %v", err)
	}

	_, err := store.ReverseJournal(context.Background(), ReverseJournalCommand{
		JournalID: "journal-single-reversal-second", OriginalJournalID: original.JournalID,
		BusinessType: "funding_correction", BusinessID: "funding-correction-second", EffectiveAt: 1_800_000_002,
	})
	if !errors.Is(err, ErrJournalAlreadyReversed) {
		t.Fatalf("post second reversal error = %v, want %v", err, ErrJournalAlreadyReversed)
	}

	replayed, err := store.ReverseJournal(context.Background(), firstReversal)
	if err != nil {
		t.Fatalf("retry first reversal: %v", err)
	}
	assertBalance(t, replayed, "platform-cash-usd", 10000, 10000, 0, 2)
	assertBalance(t, replayed, "org-main-available-usd", 10000, 10000, 0, 2)
}

func TestConcurrentReversalsPostOnce(t *testing.T) {
	store, pool := newLedgerTestStore(t)
	seedLedgerAccounts(t, pool)
	original := PostJournalCommand{
		JournalID: "journal-concurrent-reversal-original", BookID: "book-local",
		BusinessType: "funding", BusinessID: "funding-concurrent-reversal", EventType: "funding",
		Currency: "USD", EffectiveAt: 1_800_000_000,
		Postings: []Posting{
			{AccountID: "platform-cash-usd", Side: Debit, AmountMinor: 10000},
			{AccountID: "org-main-available-usd", Side: Credit, AmountMinor: 10000},
		},
	}
	if _, err := store.PostJournal(context.Background(), original); err != nil {
		t.Fatalf("post original journal: %v", err)
	}

	type outcome struct {
		result PostedJournal
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, suffix := range []string{"a", "b"} {
		suffix := suffix
		go func() {
			ready.Done()
			<-start
			result, err := store.ReverseJournal(context.Background(), ReverseJournalCommand{
				JournalID: "journal-concurrent-reversal-" + suffix, OriginalJournalID: original.JournalID,
				BusinessType: "funding_correction", BusinessID: "funding-concurrent-correction-" + suffix,
				EffectiveAt: 1_800_000_001,
			})
			outcomes <- outcome{result: result, err: err}
		}()
	}
	ready.Wait()
	close(start)

	var succeeded, rejected int
	var posted PostedJournal
	for range 2 {
		outcome := <-outcomes
		switch {
		case outcome.err == nil:
			succeeded++
			posted = outcome.result
		case errors.Is(outcome.err, ErrJournalAlreadyReversed):
			rejected++
		default:
			t.Fatalf("unexpected concurrent reversal error: %v", outcome.err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent reversals: succeeded=%d rejected=%d, want 1 and 1", succeeded, rejected)
	}
	assertBalance(t, posted, "platform-cash-usd", 10000, 10000, 0, 2)
	assertBalance(t, posted, "org-main-available-usd", 10000, 10000, 0, 2)
}

func newLedgerTestStore(t *testing.T) (*PostgresStore, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is required for PostgreSQL ledger tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	schemaName := fmt.Sprintf("ledger_contract_%d", time.Now().UnixNano())
	schemaIdentifier := pgx.Identifier{schemaName}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schemaIdentifier); err != nil {
		admin.Close()
		t.Fatalf("create ledger schema: %v", err)
	}

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL DSN: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect to ledger schema: %v", err)
	}
	schemaPath := filepath.Join("..", "..", "cmd", "enterprise-card", "schema.sql")
	schemaSQL, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if _, err := pool.Exec(ctx, string(schemaSQL)); err != nil {
		t.Fatalf("apply schema: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schemaIdentifier+" CASCADE"); err != nil {
			t.Errorf("drop ledger schema: %v", err)
		}
		admin.Close()
	})
	return NewPostgresStore(pool), pool
}

func seedLedgerAccounts(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO ledger_books(id,legal_entity_id,name,base_currency,status)
		VALUES('book-local','entity-local','Local Book','USD','active');

		INSERT INTO ledger_accounts
			(id,book_id,account_key,account_group_id,account_type,normal_side,owner_type,owner_id,currency,purpose,status)
		VALUES
			('platform-cash-usd','book-local','platform.cash.usd','platform-usd','asset','debit','platform','platform-local','USD','cash','active'),
			('org-main-available-usd','book-local','org.main.available.usd','org-main-usd','liability','credit','organization','org-local','USD','available','active'),
			('org-main-held-usd','book-local','org.main.held.usd','org-main-usd','liability','credit','organization','org-local','USD','held','active'),
			('provider-payable-usd','book-local','provider.payable.usd','provider-usd','liability','credit','provider','provider-local','USD','payable','active');
	`); err != nil {
		t.Fatalf("seed ledger accounts: %v", err)
	}
}

func assertBalance(t *testing.T, result PostedJournal, accountID string, debits, credits, balance, version int64) {
	t.Helper()
	for _, got := range result.Balances {
		if got.AccountID != accountID {
			continue
		}
		if got.PostedDebitsMinor != debits || got.PostedCreditsMinor != credits || got.BalanceMinor != balance || got.Version != version {
			t.Fatalf("balance for %s = %#v", accountID, got)
		}
		return
	}
	t.Fatalf("balance for %s not returned", accountID)
}
