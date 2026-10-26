package card

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type legacyFinanceContractStore interface {
	FinanceStore
	AuthorizationStore
	AuthorizationReversalStore
	AuthorizationSettlementStore
	SettlementRefundStore
}

type legacyFinanceContract struct {
	Funding      AccountBalance                `json:"funding"`
	Hold         AuthorizationResult           `json:"hold"`
	Settlement   AuthorizationSettlementResult `json:"settlement"`
	Refund       SettlementRefundResult        `json:"refund"`
	SecondHold   AuthorizationResult           `json:"second_hold"`
	Reversal     AuthorizationReversalResult   `json:"reversal"`
	Insufficient AuthorizationResult           `json:"insufficient"`
}

func TestLegacyFinanceLifecycleContractWithMemoryStore(t *testing.T) {
	store := NewMemoryFinanceStore()
	store.SetProviderCard("provider-card-contract", "card-contract", "active")
	runLegacyFinanceLifecycleContract(t, store)
}

func TestLegacyFinanceLifecycleContractWithPostgresStore(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is required for the PostgreSQL contract")
	}

	pool := newContractPostgresPool(t, dsn)
	store := NewPostgresStore(pool)
	_, err := pool.Exec(context.Background(), `INSERT INTO card_lab_cards
		(id,organization_id,holder_id,alias,status,currency,balance_minor,provider_card_id,created_at)
		VALUES('card-contract','org-contract','user-contract','Contract Card','active','USD',0,'provider-card-contract',1800000000)`)
	if err != nil {
		t.Fatalf("seed contract card: %v", err)
	}

	runLegacyFinanceLifecycleContract(t, store)

	var legacyAccounts, legacyEntries, journals, postings, declinedHoldJournals int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM card_lab_accounts`).Scan(&legacyAccounts); err != nil {
		t.Fatalf("count legacy accounts: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM card_lab_ledger_entries`).Scan(&legacyEntries); err != nil {
		t.Fatalf("count legacy entries: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM ledger_journals WHERE status='posted'`).Scan(&journals); err != nil {
		t.Fatalf("count posted journals: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM ledger_postings`).Scan(&postings); err != nil {
		t.Fatalf("count postings: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM ledger_journals
		WHERE business_type='card_authorization' AND business_id='auth-contract-insufficient'`).Scan(&declinedHoldJournals); err != nil {
		t.Fatalf("count declined hold journals: %v", err)
	}
	if legacyAccounts != 0 || legacyEntries != 0 {
		t.Fatalf("legacy finance writes: accounts=%d entries=%d, want both 0", legacyAccounts, legacyEntries)
	}
	if journals != 6 || postings != 12 {
		t.Fatalf("operational ledger facts: journals=%d postings=%d, want 6 and 12", journals, postings)
	}
	if declinedHoldJournals != 0 {
		t.Fatalf("declined authorization hold journals = %d, want 0", declinedHoldJournals)
	}
	report, err := store.ReconcileOperationalLedger(context.Background())
	if err != nil {
		t.Fatalf("reconcile operational ledger: %v", err)
	}
	if !report.Healthy {
		t.Fatalf("operational ledger reconciliation = %#v, want healthy", report)
	}
}

func TestOperationalLedgerConcurrentAuthorizationsCannotOverspend(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is required for the PostgreSQL contract")
	}
	pool := newContractPostgresPool(t, dsn)
	store := NewPostgresStore(pool)
	if _, err := pool.Exec(context.Background(), `INSERT INTO card_lab_cards
		(id,organization_id,holder_id,alias,status,currency,balance_minor,provider_card_id,created_at)
		VALUES('card-concurrent-ledger','org-concurrent-ledger','user-concurrent-ledger','Concurrent Ledger Card','active','USD',0,'provider-card-concurrent-ledger',1800000000)`); err != nil {
		t.Fatalf("seed concurrent ledger card: %v", err)
	}
	if _, err := store.Fund(context.Background(), FundParams{"fund-concurrent-ledger", "card-concurrent-ledger", "USD", 10000}); err != nil {
		t.Fatalf("fund concurrent ledger card: %v", err)
	}

	start := make(chan struct{})
	results := make(chan AuthorizationResult, 2)
	errorsCh := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, authorizationID := range []string{"auth-concurrent-ledger-a", "auth-concurrent-ledger-b"} {
		authorizationID := authorizationID
		go func() {
			ready.Done()
			<-start
			result, err := store.Authorize(context.Background(), AuthorizationParams{authorizationID, "provider-card-concurrent-ledger", "USD", 7000})
			results <- result
			errorsCh <- err
		}()
	}
	ready.Wait()
	close(start)

	approved, declined := 0, 0
	for range 2 {
		result := <-results
		if err := <-errorsCh; err != nil {
			t.Fatalf("concurrent authorization: %v", err)
		}
		switch {
		case result.Decision == "approved":
			approved++
		case result.Decision == "declined" && result.Reason == "insufficient_funds":
			declined++
		default:
			t.Fatalf("unexpected concurrent authorization result: %#v", result)
		}
	}
	if approved != 1 || declined != 1 {
		t.Fatalf("concurrent decisions: approved=%d declined=%d, want 1 and 1", approved, declined)
	}

	var holdJournals int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM ledger_journals WHERE event_type='hold' AND status='posted'`).Scan(&holdJournals); err != nil {
		t.Fatalf("count concurrent hold journals: %v", err)
	}
	if holdJournals != 1 {
		t.Fatalf("posted hold journals = %d, want 1", holdJournals)
	}
}

func TestLegacyCardBalanceMigratesOnceIntoBalancedOpeningJournal(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is required for the PostgreSQL contract")
	}
	pool := newContractPostgresPool(t, dsn)
	store := NewPostgresStore(pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO card_lab_cards
			(id,organization_id,holder_id,alias,status,currency,balance_minor,provider_card_id,created_at)
		VALUES
			('card-migration','org-migration','user-migration','Migration Card','active','USD',0,'provider-card-migration',1800000000);
		INSERT INTO card_lab_accounts(card_id,currency,posted_balance_minor,held_amount_minor)
		VALUES('card-migration','USD',100000,3000);
	`); err != nil {
		t.Fatalf("seed legacy card balance: %v", err)
	}

	first, err := store.MigrateCardLedger(context.Background(), "card-migration")
	if err != nil {
		t.Fatalf("migrate legacy card balance: %v", err)
	}
	second, err := store.MigrateCardLedger(context.Background(), "card-migration")
	if err != nil {
		t.Fatalf("repeat legacy card migration: %v", err)
	}
	want := AccountBalance{CardID: "card-migration", Currency: "USD", PostedBalanceMinor: 100000, HeldAmountMinor: 3000, AvailableBalanceMinor: 97000}
	if first != want || second != want {
		t.Fatalf("migration balances: first=%#v second=%#v want=%#v", first, second, want)
	}

	var migrationRows, openingJournals, postings int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM ledger_migrations
		WHERE source_type='legacy_card_account' AND source_id='card-migration' AND status='completed'`).Scan(&migrationRows); err != nil {
		t.Fatalf("count completed migration rows: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM ledger_journals
		WHERE business_type='legacy_card_account' AND business_id='card-migration' AND event_type='opening_balance' AND status='posted'`).Scan(&openingJournals); err != nil {
		t.Fatalf("count opening journals: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM ledger_postings p
		JOIN ledger_journals j ON j.id=p.journal_id
		WHERE j.business_type='legacy_card_account' AND j.business_id='card-migration'`).Scan(&postings); err != nil {
		t.Fatalf("count opening postings: %v", err)
	}
	if migrationRows != 1 || openingJournals != 1 || postings != 3 {
		t.Fatalf("migration evidence: rows=%d journals=%d postings=%d, want 1,1,3", migrationRows, openingJournals, postings)
	}
}

func TestOperationalLedgerReconciliationDetectsProjectionDrift(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is required for the PostgreSQL contract")
	}
	pool := newContractPostgresPool(t, dsn)
	store := NewPostgresStore(pool)
	if _, err := pool.Exec(context.Background(), `INSERT INTO card_lab_cards
		(id,organization_id,holder_id,alias,status,currency,balance_minor,provider_card_id,created_at)
		VALUES('card-reconcile','org-reconcile','user-reconcile','Reconcile Card','active','USD',0,'provider-card-reconcile',1800000000)`); err != nil {
		t.Fatalf("seed reconcile card: %v", err)
	}
	if _, err := store.Fund(context.Background(), FundParams{"fund-reconcile", "card-reconcile", "USD", 10000}); err != nil {
		t.Fatalf("fund reconcile card: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE ledger_balances
		SET posted_credits_minor=posted_credits_minor+1
		WHERE account_id IN (SELECT account_id FROM card_funding_sources WHERE card_id='card-reconcile')`); err != nil {
		t.Fatalf("tamper balance projection: %v", err)
	}
	report, err := store.ReconcileOperationalLedger(context.Background())
	if err != nil {
		t.Fatalf("reconcile tampered ledger: %v", err)
	}
	if report.Healthy || report.BalanceProjectionMismatches != 1 {
		t.Fatalf("tampered reconciliation = %#v, want one balance projection mismatch", report)
	}
}

func TestOperationalLedgerReconciliationDetectsMissingReversalJournal(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is required for the PostgreSQL contract")
	}
	pool := newContractPostgresPool(t, dsn)
	store := NewPostgresStore(pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO card_lab_cards
			(id,organization_id,holder_id,alias,status,currency,balance_minor,provider_card_id,created_at)
		VALUES
			('card-reversal-reconcile','org-reversal-reconcile','user-reversal-reconcile','Reversal Reconcile Card','active','USD',0,'provider-card-reversal-reconcile',1800000000);
		INSERT INTO card_lab_authorizations
			(authorization_id,provider_card_id,card_id,currency,amount_minor,decision,reason,
			 posted_balance_after,held_amount_after,available_balance_after,authorization_status,ledger_version)
		VALUES
			('auth-reversal-reconcile','provider-card-reversal-reconcile','card-reversal-reconcile','USD',100,'approved','',
			 1000,0,1000,'reversed',0);
		INSERT INTO card_lab_authorization_reversals
			(reversal_id,authorization_id,released_amount_minor,posted_balance_after,held_amount_after,
			 available_balance_after,release_journal_id,ledger_version)
		VALUES
			('reversal-reconcile','auth-reversal-reconcile',100,1000,0,1000,'missing-release-journal',1);
	`); err != nil {
		t.Fatalf("seed missing reversal journal: %v", err)
	}

	report, err := store.ReconcileOperationalLedger(context.Background())
	if err != nil {
		t.Fatalf("reconcile missing reversal journal: %v", err)
	}
	if report.Healthy || report.ReversalsMissingJournal != 1 {
		t.Fatalf("missing reversal reconciliation = %#v, want one missing reversal journal", report)
	}
}

func TestOperationalLedgerEnforcesFundingSourceSpendPolicy(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is required for the PostgreSQL contract")
	}
	pool := newContractPostgresPool(t, dsn)
	store := NewPostgresStore(pool)
	if _, err := pool.Exec(context.Background(), `INSERT INTO card_lab_cards
		(id,organization_id,holder_id,alias,status,currency,balance_minor,provider_card_id,created_at)
		VALUES('card-spend-policy','org-spend-policy','user-spend-policy','Spend Policy Card','active','USD',0,'provider-card-spend-policy',1800000000)`); err != nil {
		t.Fatalf("seed spend policy card: %v", err)
	}
	if _, err := store.Fund(context.Background(), FundParams{"fund-spend-policy", "card-spend-policy", "USD", 10000}); err != nil {
		t.Fatalf("fund spend policy card: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE card_funding_sources SET spending_limit_minor=2000 WHERE card_id='card-spend-policy'`); err != nil {
		t.Fatalf("set card spending limit: %v", err)
	}
	limited, err := store.Authorize(context.Background(), AuthorizationParams{"auth-over-card-limit", "provider-card-spend-policy", "USD", 2500})
	if err != nil {
		t.Fatalf("authorize over card limit: %v", err)
	}
	if limited.Decision != "declined" || limited.Reason != "spending_limit_exceeded" || limited.AvailableBalanceMinor != 10000 {
		t.Fatalf("over-limit authorization = %#v", limited)
	}

	if _, err := pool.Exec(context.Background(), `UPDATE card_funding_sources SET spending_limit_minor=0 WHERE card_id='card-spend-policy';
		UPDATE ledger_accounts SET status='frozen'
		WHERE id=(SELECT account_id FROM card_funding_sources WHERE card_id='card-spend-policy' AND status='active')`); err != nil {
		t.Fatalf("freeze funding source account: %v", err)
	}
	frozen, err := store.Authorize(context.Background(), AuthorizationParams{"auth-frozen-funding-source", "provider-card-spend-policy", "USD", 100})
	if err != nil {
		t.Fatalf("authorize frozen funding source: %v", err)
	}
	if frozen.Decision != "declined" || frozen.Reason != "funding_source_not_active" || frozen.AvailableBalanceMinor != 10000 {
		t.Fatalf("frozen-source authorization = %#v", frozen)
	}

	var holdJournals int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM ledger_journals WHERE event_type='hold'`).Scan(&holdJournals); err != nil {
		t.Fatalf("count rejected policy hold journals: %v", err)
	}
	if holdJournals != 0 {
		t.Fatalf("rejected policy hold journals = %d, want 0", holdJournals)
	}
}

func TestOperationalLedgerSettlementAndReversalRaceHasOneWinner(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is required for the PostgreSQL contract")
	}
	pool := newContractPostgresPool(t, dsn)
	store := NewPostgresStore(pool)
	if _, err := pool.Exec(context.Background(), `INSERT INTO card_lab_cards
		(id,organization_id,holder_id,alias,status,currency,balance_minor,provider_card_id,created_at)
		VALUES('card-terminal-race','org-terminal-race','user-terminal-race','Terminal Race Card','active','USD',0,'provider-card-terminal-race',1800000000)`); err != nil {
		t.Fatalf("seed terminal race card: %v", err)
	}
	if _, err := store.Fund(context.Background(), FundParams{"fund-terminal-race", "card-terminal-race", "USD", 10000}); err != nil {
		t.Fatalf("fund terminal race card: %v", err)
	}
	if _, err := store.Authorize(context.Background(), AuthorizationParams{"auth-terminal-race", "provider-card-terminal-race", "USD", 2500}); err != nil {
		t.Fatalf("authorize terminal race: %v", err)
	}

	start := make(chan struct{})
	outcomes := make(chan error, 2)
	go func() {
		<-start
		_, err := store.SettleAuthorization(context.Background(), AuthorizationSettlementParams{"settlement-terminal-race", "auth-terminal-race"})
		outcomes <- err
	}()
	go func() {
		<-start
		_, err := store.ReverseAuthorization(context.Background(), AuthorizationReversalParams{"reversal-terminal-race", "auth-terminal-race"})
		outcomes <- err
	}()
	close(start)

	succeeded, rejected := 0, 0
	for range 2 {
		err := <-outcomes
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrAuthorizationNotSettleable), errors.Is(err, ErrAuthorizationNotReversible):
			rejected++
		default:
			t.Fatalf("unexpected terminal race error: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("terminal race: succeeded=%d rejected=%d, want 1 and 1", succeeded, rejected)
	}

	var terminalFacts int
	if err := pool.QueryRow(context.Background(), `SELECT
		(SELECT COUNT(*) FROM card_lab_authorization_settlements WHERE authorization_id='auth-terminal-race') +
		(SELECT COUNT(*) FROM card_lab_authorization_reversals WHERE authorization_id='auth-terminal-race')`).Scan(&terminalFacts); err != nil {
		t.Fatalf("count terminal race facts: %v", err)
	}
	if terminalFacts != 1 {
		t.Fatalf("terminal business facts = %d, want 1", terminalFacts)
	}
}

func runLegacyFinanceLifecycleContract(t *testing.T, store legacyFinanceContractStore) {
	t.Helper()
	contract := loadLegacyFinanceContract(t)
	ctx := context.Background()

	fundingParams := FundParams{"fund-contract", "card-contract", "USD", 100000}
	funding, err := store.Fund(ctx, fundingParams)
	assertContractResult(t, "funding", funding, err, contract.Funding)
	duplicateFunding, err := store.Fund(ctx, fundingParams)
	assertContractResult(t, "duplicate funding", duplicateFunding, err, contract.Funding)
	if _, err := store.Fund(ctx, FundParams{"fund-contract", "card-contract", "USD", 100001}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting funding error = %v, want %v", err, ErrIdempotencyConflict)
	}

	holdParams := AuthorizationParams{"auth-contract-settle", "provider-card-contract", "USD", 2500}
	hold, err := store.Authorize(ctx, holdParams)
	assertContractResult(t, "hold", hold, err, contract.Hold)
	duplicateHold, err := store.Authorize(ctx, holdParams)
	assertContractResult(t, "duplicate hold", duplicateHold, err, contract.Hold)
	if _, err := store.Authorize(ctx, AuthorizationParams{"auth-contract-settle", "provider-card-contract", "USD", 2501}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting authorization error = %v, want %v", err, ErrIdempotencyConflict)
	}

	settlementParams := AuthorizationSettlementParams{"settlement-contract", "auth-contract-settle"}
	settlement, err := store.SettleAuthorization(ctx, settlementParams)
	assertContractResult(t, "settlement", settlement, err, contract.Settlement)
	duplicateSettlement, err := store.SettleAuthorization(ctx, settlementParams)
	assertContractResult(t, "duplicate settlement", duplicateSettlement, err, contract.Settlement)

	refundParams := SettlementRefundParams{"refund-contract", "settlement-contract"}
	refund, err := store.RefundSettlement(ctx, refundParams)
	assertContractResult(t, "refund", refund, err, contract.Refund)
	duplicateRefund, err := store.RefundSettlement(ctx, refundParams)
	assertContractResult(t, "duplicate refund", duplicateRefund, err, contract.Refund)

	secondHoldParams := AuthorizationParams{"auth-contract-reverse", "provider-card-contract", "USD", 4000}
	secondHold, err := store.Authorize(ctx, secondHoldParams)
	assertContractResult(t, "second hold", secondHold, err, contract.SecondHold)
	reversalParams := AuthorizationReversalParams{"reversal-contract", "auth-contract-reverse"}
	reversal, err := store.ReverseAuthorization(ctx, reversalParams)
	assertContractResult(t, "reversal", reversal, err, contract.Reversal)
	duplicateReversal, err := store.ReverseAuthorization(ctx, reversalParams)
	assertContractResult(t, "duplicate reversal", duplicateReversal, err, contract.Reversal)

	insufficient, err := store.Authorize(ctx, AuthorizationParams{"auth-contract-insufficient", "provider-card-contract", "USD", 200000})
	assertContractResult(t, "insufficient authorization", insufficient, err, contract.Insufficient)
}

func loadLegacyFinanceContract(t *testing.T) legacyFinanceContract {
	t.Helper()
	contents, err := os.ReadFile("testdata/legacy_finance_lifecycle.json")
	if err != nil {
		t.Fatalf("read legacy finance contract: %v", err)
	}
	var contract legacyFinanceContract
	if err := json.Unmarshal(contents, &contract); err != nil {
		t.Fatalf("decode legacy finance contract: %v", err)
	}
	return contract
}

func assertContractResult[T comparable](t *testing.T, step string, got T, err error, want T) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s error: %v", step, err)
	}
	if got != want {
		t.Fatalf("%s result = %#v, want %#v", step, got, want)
	}
}

func newContractPostgresPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}

	schemaName := fmt.Sprintf("card_contract_%d", time.Now().UnixNano())
	schemaIdentifier := pgx.Identifier{schemaName}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schemaIdentifier); err != nil {
		admin.Close()
		t.Fatalf("create contract schema: %v", err)
	}

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schemaIdentifier+" CASCADE")
		admin.Close()
		t.Fatalf("parse PostgreSQL DSN: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schemaIdentifier+" CASCADE")
		admin.Close()
		t.Fatalf("connect to contract schema: %v", err)
	}

	schemaPath := filepath.Join("..", "..", "cmd", "enterprise-card", "schema.sql")
	schemaSQL, err := os.ReadFile(schemaPath)
	if err != nil {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schemaIdentifier+" CASCADE")
		admin.Close()
		t.Fatalf("read enterprise-card schema: %v", err)
	}
	if _, err := pool.Exec(ctx, string(schemaSQL)); err != nil {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schemaIdentifier+" CASCADE")
		admin.Close()
		t.Fatalf("apply enterprise-card schema: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schemaIdentifier+" CASCADE"); err != nil {
			t.Errorf("drop contract schema: %v", err)
		}
		admin.Close()
	})
	return pool
}
