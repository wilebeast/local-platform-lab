# Enterprise Card Operational Ledger Technical Design

## 1. Status and scope

Status: implemented locally through the first-phase lifecycle; not committed or deployed.

This document defines the first production-shaped operational ledger for the local enterprise-card lab. It records real-time customer funds, authorization holds, settlement, reversal, and refund through balanced double-entry journals.

It intentionally does not implement:

- statutory financial reporting or period close;
- foreign exchange or cross-currency journals;
- partial settlement, partial reversal, or multiple partial refunds;
- provider cash payout and bank-statement reconciliation;
- replacement of a finance organization's general ledger.

## 2. Confirmed decisions

1. Use an independent operational ledger rather than extending `card_lab_ledger_entries` with debit and credit columns.
2. Use one ledger book per legal-entity accounting boundary; the first lab deployment has one book.
3. Permit multiple single-currency ledger accounts for the same organization and currency.
4. Cards do not own balances. A card binds to a shared organization funding source by default and may bind to a dedicated funding source.
5. Authorization holds are represented in the operational ledger by balanced movements between available and held accounts.
6. Every journal is single-currency, immutable after posting, and corrected only through a reversal journal.
7. Journal, postings, balance projection, and business-state mutation commit atomically.
8. Existing card balances migrate through opening-balance journals. The old tables remain read-only evidence during migration.

## 3. Evidence from the current implementation

The current lab has useful transaction and idempotency behavior but is not double-entry:

- `card_lab_accounts` uses `card_id` as its primary key and stores `posted_balance_minor` and `held_amount_minor` directly.
- `card_lab_ledger_entries` has one positive amount per funding, settlement, or refund; it has no journal, account, debit, or credit dimensions.
- authorization updates `held_amount_minor` directly;
- settlement decreases posted and held balances directly;
- refund increases posted balance directly;
- the business document and balance changes already occur in PostgreSQL transactions and use row or advisory locks.

The migration should retain those concurrency and idempotency lessons while replacing the financial fact model.

## 4. Fact ownership

| Fact | Authoritative owner | Stable identifier | Mutable fields | Terminal evidence |
| --- | --- | --- | --- | --- |
| Card lifecycle | enterprise-card service | `card_id` | card status, provider reference | card state history/provider event |
| Funding-source binding | enterprise-card service | `binding_id` | policy, limit, effective interval | binding row and audit event |
| Authorization | enterprise-card service | `authorization_id` | lifecycle status | authorization row and linked journals |
| Settlement/refund | enterprise-card service | provider business ID | lifecycle status | business row and linked journal |
| Funds movement | operational ledger | `journal_id` | draft fields only | immutable posted journal and postings |
| Account balance | operational ledger projection | `account_id` | derived totals/version | rebuild from postings |
| Provider reality | provider adapter/inbox | provider event or request ID | delivery/processing status | raw message, normalized fact, reconciliation result |

Neither the card table nor a cached balance is the source of truth for funds after cutover.

## 5. Architecture

```text
Provider/Card API
       |
       v
Enterprise Card Application Use Case
       |  resolves card funding source and business state
       v
Operational Ledger Posting Service
       |
       +-- ledger_journals
       +-- ledger_postings
       +-- ledger_balances
       |
       v
PostgreSQL transaction commit
       |
       +-- business fact committed
       +-- balanced journal posted
       +-- balance projection updated
```

The ledger is initially a module in the enterprise-card service and the same PostgreSQL database. It has a strict internal API and separate tables. A network service is deferred until a second concrete product needs the same write model or independent scaling/ownership becomes necessary.

## 6. Entity relationships

```text
ledger_books 1 ------ * ledger_accounts
     |                        |
     |                        +------ 1 ledger_balances
     |
     +------ * ledger_journals 1 ------ * ledger_postings * ------ 1 ledger_accounts
                       |
                       +------ 0..1 reversal_of -> ledger_journals

card_lab_cards 1 ------ * card_funding_sources * ------ 1 ledger_accounts

card_lab_authorizations ------ 1 hold journal
                           +-- 0..1 reversal journal
                           +-- 0..1 settlement journal

card_lab_settlement_refunds ------ 1 refund journal
```

## 7. Database design

The names below are implementation contracts. Exact migration syntax remains subject to tests and review.

### 7.1 `ledger_books`

| Column | Type | Constraint/meaning |
| --- | --- | --- |
| `id` | varchar(64) | primary key, nonempty |
| `legal_entity_id` | varchar(128) | nonempty |
| `name` | varchar(128) | nonempty |
| `base_currency` | char(3) | reporting metadata; does not permit mixed-currency journals |
| `status` | varchar(16) | `active`, `closed` |
| `created_at` | timestamptz | immutable creation time |

Unique: `(legal_entity_id, id)`; one active lab book is created by migration seed data.

### 7.2 `ledger_accounts`

| Column | Type | Constraint/meaning |
| --- | --- | --- |
| `id` | varchar(96) | primary key |
| `book_id` | varchar(64) | foreign key to book |
| `account_key` | varchar(160) | stable idempotent business key |
| `account_group_id` | varchar(96) | stable group linking available and held accounts for one funding pool |
| `account_type` | varchar(16) | `asset`, `liability`, `revenue`, `expense` |
| `normal_side` | varchar(8) | `debit`, `credit`; checked against account type policy |
| `owner_type` | varchar(24) | `platform`, `organization`, `provider` |
| `owner_id` | varchar(128) | owner reference; platform uses an explicit stable ID |
| `currency` | char(3) | uppercase ISO-style code used by all postings |
| `purpose` | varchar(32) | `cash`, `available`, `held`, `payable`, `receivable`, `fee` |
| `status` | varchar(16) | `active`, `frozen`, `closed` |
| `version` | bigint | optimistic concurrency/audit version |
| `created_at`, `updated_at` | timestamptz | audit timestamps |

Unique: `(book_id, account_key)`.

There is deliberately no uniqueness constraint on owner, currency, and purpose: an organization may have multiple USD budget accounts. New accounts are added as rows, not columns or new tables.

For the `available` and `held` purposes, unique `(book_id, account_group_id, purpose)` guarantees exactly one account of each purpose in a funding pool. Both rows must have the same owner and currency. An organization may create multiple account groups for its main balance, department budgets, or dedicated card balances.

Account close requires zero effective balance and no active card binding or outstanding authorization.

Account status semantics are deliberately asymmetric:

- `active`: normal posting is allowed, subject to the business command and balance invariants.
- `frozen`: a temporary control signal. It does not by itself reject every ledger posting because existing authorizations may still need settlement, release, refund, or reversal. New-spend eligibility is enforced by the card and funding-source policy before calling the ledger.
- `closed`: terminal. No new posting may reference the account; PostgreSQL enforces this again when a journal transitions to `posted`.

Therefore `frozen` must not be treated as an alias for `closed`. If future requirements need event-specific restrictions for frozen accounts, they must define an explicit policy matrix for `hold`, `settlement`, `hold_release`, `refund`, and `reversal`, with recovery paths tested before enforcement.

Account closure uses the controlled `CloseAccount` transaction and requires all three conditions:

1. the effective ledger balance is zero;
2. no currently effective active card funding source references the account;
3. no approved authorization linked through a historical funding-source binding remains `authorized`.

`CloseAccount` locks the account row, evaluates these facts, and performs the terminal status transition in one transaction. PostgreSQL repeats the same checks in a trigger so an administrative SQL update cannot bypass the invariants. Posting and closing contend on the same account row: concurrent execution may allow either operation to finish first, but they cannot both succeed.

### 7.3 `card_funding_sources`

| Column | Type | Constraint/meaning |
| --- | --- | --- |
| `id` | varchar(96) | primary key |
| `card_id` | varchar(128) | foreign key to card |
| `account_id` | varchar(96) | available-funds ledger account |
| `priority` | integer | deterministic source selection; first phase requires `0` |
| `spending_limit_minor` | bigint | `0` means no additional per-card limit |
| `status` | varchar(16) | `active`, `inactive` |
| `effective_from`, `effective_to` | bigint | Unix seconds; `0` means open boundary |
| `created_at`, `updated_at` | timestamptz | audit timestamps |

First-phase invariant: exactly one effective active binding per card. Split funding is deferred.

The selected account must be an active `available` account in the same currency as the card operation. Its corresponding held account is resolved by `account_group_id`, never inferred by string concatenation.

### 7.4 `ledger_journals`

| Column | Type | Constraint/meaning |
| --- | --- | --- |
| `id` | varchar(160) | primary key |
| `book_id` | varchar(64) | accounting boundary |
| `business_type` | varchar(48) | bounded type such as `card_authorization` |
| `business_id` | varchar(160) | stable external/business ID |
| `event_type` | varchar(48) | `opening_balance`, `funding`, `hold`, `hold_release`, `settlement`, `refund`, `reversal` |
| `currency` | char(3) | one currency per journal |
| `status` | varchar(16) | `draft`, `posted` |
| `reversal_of` | varchar(160) | empty or foreign key to original journal |
| `effective_at` | bigint | business effective time |
| `created_at` | timestamptz | immutable creation time |
| `posted_at` | bigint | Unix seconds, `0` while draft |

Unique: `(book_id, business_type, business_id, event_type)`.

`posted` journals and postings cannot be updated or deleted. Reversal state is derived from a new posted journal whose `reversal_of` references the original; the original remains `posted`. A partial unique index on nonempty `reversal_of` prevents a second reversal.

Because an empty optional relationship cannot satisfy a normal foreign key, `reversal_of` uses an empty string for “not a reversal” and a deferred constraint trigger validates every nonempty value as an existing posted journal in the same book and currency.

### 7.5 `ledger_postings`

| Column | Type | Constraint/meaning |
| --- | --- | --- |
| `id` | bigint generated identity | primary key |
| `journal_id` | varchar(160) | foreign key to journal |
| `sequence_no` | smallint | deterministic line order |
| `account_id` | varchar(96) | foreign key to account |
| `side` | varchar(8) | `debit`, `credit` |
| `amount_minor` | bigint | strictly positive |
| `currency` | char(3) | must match journal and account |
| `created_at` | timestamptz | immutable creation time |

Unique: `(journal_id, sequence_no)`.

Only draft journals accept postings. Posting rows cannot be updated or deleted after the journal becomes posted.

### 7.6 `ledger_balances`

| Column | Type | Constraint/meaning |
| --- | --- | --- |
| `account_id` | varchar(96) | primary key |
| `posted_debits_minor` | bigint | nonnegative cumulative debit |
| `posted_credits_minor` | bigint | nonnegative cumulative credit |
| `version` | bigint | increments once per journal affecting account |
| `updated_at` | timestamptz | projection update time |

The effective balance is interpreted using `normal_side`; it is not a freely writable signed balance. This row is updated in the same transaction as journal posting and can be rebuilt from immutable postings.

Pending debit/credit columns are deferred because first-phase holds are posted reclassifications between available and held accounts.

## 8. Account catalog for the first phase

| Account | Type | Normal side | Owner | Purpose |
| --- | --- | --- | --- | --- |
| Platform cash USD | asset | debit | platform | cash |
| Organization USD available | liability | credit | organization | available |
| Organization USD held | liability | credit | organization | held |
| Provider USD payable | liability | credit | provider | payable |
| Provider USD receivable | asset | debit | provider | receivable |

The catalog is data-driven. Creating another organization USD budget or EUR wallet creates account rows and funding-source bindings without schema changes.

## 9. Posting matrix

All examples use amount `A` and one currency.

| Business event | Debit | Credit | Result |
| --- | --- | --- | --- |
| Opening balance/funding | Platform cash asset `A` | Organization available liability `A` | available increases |
| Authorization approved | Organization available liability `A` | Organization held liability `A` | available decreases, held increases |
| Authorization reversed/expired | Organization held liability `A` | Organization available liability `A` | hold released |
| Settlement | Organization held liability `A` | Provider payable liability `A` | held consumed, provider obligation created |
| Provider payout | Provider payable liability `A` | Platform cash asset `A` | payable and cash decrease |
| Refund before provider recovery | Provider receivable asset `A` | Organization available liability `A` | customer funds restored, provider owes platform |
| Provider refund recovery | Platform cash asset `A` | Provider receivable asset `A` | receivable cleared |
| Journal correction | exact opposite sides of original | exact opposite sides of original | original remains immutable |

The accounting interpretation assumes the platform holds customer funds and owes them to the organization. A production finance owner must confirm the legal custody and account catalog before launch; the lab uses this explicit assumption.

## 10. Balance semantics

For a credit-normal organization liability account:

```text
balance = posted_credits_minor - posted_debits_minor
```

For a debit-normal asset account:

```text
balance = posted_debits_minor - posted_credits_minor
```

An organization's spendable balance is the balance of its selected `available` account. Held funds are the balance of the paired `held` account. They must not be recomputed from authorization rows for authorization decisions after cutover.

## 11. Posting transaction and invariants

The write path is a single controlled operation, provisionally named:

```go
PostJournal(ctx context.Context, command PostJournalCommand) (PostedJournal, error)
```

`PostJournalCommand` contains a journal identity, business identity, currency, effective time, and at least two posting lines. It is internal and does not accept client-selected arbitrary account IDs at public HTTP boundaries.

Ordered transaction:

```text
BEGIN
  1. Acquire idempotency lock for journal business key
  2. Return existing identical posted result, or reject conflicting reuse
  3. Resolve and lock all affected balance rows in account_id order
  4. Validate book, currency, account status and business preconditions
  5. Insert draft journal
  6. Insert all debit and credit postings
  7. Verify debit total == credit total for the journal/currency
  8. Verify resulting constrained balances are nonnegative
  9. Update balance projections and versions
 10. Update linked business state
 11. Mark journal posted
 12. Deferred database constraint rechecks balance at COMMIT
COMMIT
```

Mandatory invariants:

1. Every posted journal has at least two postings.
2. Debit sum equals credit sum for each posted journal and currency.
3. Journal, account, and posting currencies match.
4. All accounts belong to the journal's book.
5. A posted journal and its postings are immutable.
6. A business event maps to at most one financial effect.
7. Idempotent replay returns the original result only when the complete command fingerprint matches.
8. Account locks are acquired in deterministic order to reduce deadlocks.
9. Available and held liability accounts cannot become negative in first-phase card flows.
10. Balance projection version increments atomically with every affecting journal.
11. Business state cannot become authorized, settled, reversed, or refunded unless its matching journal posts in the same transaction.

PostgreSQL cannot express a multi-row sum invariant with an ordinary row `CHECK`. Use a controlled posting function/repository plus a deferred constraint trigger that raises on an unbalanced posted journal. Direct table mutation is denied to the application role except through the approved write path.

## 12. Use-case contracts

### UC-L01 Create ledger account

| Item | Contract |
| --- | --- |
| Actor/trigger | authorized operator or provisioning workflow creates a currency/purpose account |
| Inputs | book, account key, owner, currency, purpose, type, normal side |
| Output | active zero-balance account and balance projection |
| Rules | idempotent account key; supported currency; valid purpose/type/side combination |
| Owner | operational ledger module |
| Safety | one transaction; conflicting account-key reuse fails |
| Exclusions | funding, card binding, FX |
| Verification | repeat, conflict, unsupported currency, invalid catalog combination |

### UC-L02 Bind card funding source

| Item | Contract |
| --- | --- |
| Actor/trigger | enterprise-card policy binds a card to shared or dedicated available account |
| Inputs | card, available account, optional limit, effective interval |
| Output | one effective active binding |
| Rules | owner policy, matching currency, active account, no overlapping effective binding |
| Owner | enterprise-card service |
| Safety | transaction plus exclusion/uniqueness enforcement |
| Exclusions | split funding and fallback chains |
| Verification | shared account, dedicated account, overlap, currency mismatch, closed account |

### UC-L03 Fund/open balance

| Item | Contract |
| --- | --- |
| Trigger | trusted funding or migration fact |
| Inputs | business ID, platform cash account, organization available account, amount/currency |
| Output | balanced posted journal and increased available balance |
| Rules | positive integer amount; one currency; idempotent business key |
| Safety | journal, postings, balances and funding fact in one transaction |
| Verification | success, duplicate, conflict, currency mismatch, crash rollback |

### UC-L04 Authorize and hold

| Item | Contract |
| --- | --- |
| Trigger | verified provider authorization request |
| Inputs | authorization, card, effective funding source, amount/currency |
| Output | approved authorization plus hold journal, or declined authorization without journal |
| Rules | active card/account/binding; card limit; sufficient available funds |
| Safety | account lock, balance condition, authorization and journal in one transaction |
| Verification | approval, insufficient funds, inactive card, concurrent double-spend, duplicate/conflict |

### UC-L05 Release hold

| Item | Contract |
| --- | --- |
| Trigger | provider reversal or expiry for an authorized hold |
| Inputs | original authorization and release business ID |
| Output | reversed authorization and balanced release journal |
| Rules | only an outstanding hold may be released once |
| Safety | lock authorization and accounts; state plus journal atomic |
| Verification | success, repeat, settle/reverse race, unknown authorization |

### UC-L06 Settle authorization

| Item | Contract |
| --- | --- |
| Trigger | verified provider settlement fact |
| Inputs | outstanding authorization, provider payable account, settlement ID |
| Output | settled authorization and held-to-payable journal |
| Rules | full settlement only in phase one; amount/currency must equal authorization |
| Safety | lock authorization and accounts; unique settlement business key |
| Verification | success, repeat, amount conflict, reverse/settle race, insufficient held anomaly |

### UC-L07 Refund settlement

| Item | Contract |
| --- | --- |
| Trigger | verified provider refund fact |
| Inputs | settled transaction, provider receivable, organization available, refund ID |
| Output | refunded business fact and receivable-to-available journal |
| Rules | one full refund in phase one; cannot exceed settled amount |
| Safety | settlement lock, unique refund key, atomic journal/business state |
| Verification | success, repeat, conflicting ID, unsettled source, refund/duplicate race |

### UC-L08 Reverse erroneous journal

| Item | Contract |
| --- | --- |
| Trigger | authorized operational correction after investigation |
| Inputs | original posted journal, reason, actor, correction ID |
| Output | exact opposite journal linked through `reversal_of` |
| Rules | no update/delete of original; same book/currency; one active reversal |
| Safety | privileged operation, audit evidence, balance conditions where applicable |
| Verification | exact negation, repeat, unauthorized actor, reversal of draft/already reversed journal |

## 13. Failure and concurrency matrix

| Failure point | Expected result | Recovery/evidence |
| --- | --- | --- |
| Before transaction | no business or funds fact | safe retry with same business key |
| After draft journal, before postings | transaction rollback | no visible journal |
| After postings, before balance update | transaction rollback | no partial financial effect |
| Unbalanced postings | deferred check rejects commit | metric and structured error |
| Concurrent authorizations | deterministic balance-row lock serializes decisions | one may decline for insufficient funds |
| Duplicate identical request | return original posted result | unique business key and fingerprint |
| Duplicate key with changed payload | reject idempotency conflict | retain original fact |
| Reversal races settlement | authorization lock allows one terminal transition | losing operation returns domain conflict |
| Service crashes after commit before response | retry discovers posted journal | no second posting |
| Balance projection mismatch | block affected account, rebuild and compare postings | reconciliation incident record |
| Provider/local mismatch | do not mutate history silently | reconciliation case and explicit correcting journal |

## 14. Migration and rollout

### 14.1 Why history is not replayed

The old single-sided ledger omits authorization holds and reversals and therefore cannot reconstruct a complete balanced history. Fabricating counterpart entries from incomplete history would create false audit precision.

### 14.2 Opening-balance migration

For each old card account at a defined cutover point:

1. freeze old writes or place the account behind a migration lock;
2. read posted and held balances;
3. resolve/create organization available and held accounts;
4. create one balanced opening journal whose combined organization liability equals old posted balance;
5. bind the card to the new available account;
6. compare old available/held with new available/held;
7. mark migration row completed with source fingerprint and journal ID;
8. enable new-ledger writes for that card;
9. keep old tables read-only for audit.

Example for old posted `100000`, held `3000`:

```text
Debit  Platform cash asset                 100000
Credit Organization available liability     97000
Credit Organization held liability           3000
```

This journal is balanced and preserves both spendable and held facts without pretending to reconstruct missing history.

### 14.3 Migration control table

`ledger_migrations` records source type/ID, source balance fingerprint, opening journal, status, attempt count, timestamps, and last error. Unique source identity prevents a second opening journal.

### 14.4 Rollback boundary

Before an account's first new-ledger write, the binding/feature flag can return to the old path. After any new journal posts, rollback must not resume old writes; recovery uses forward correction or completes cutover.

## 15. Reconciliation and observability

Required automated checks:

- unbalanced posted journals: must always be zero;
- postings whose currency/book differs from account or journal: zero;
- balance projection versus posting-derived balance difference: zero;
- business terminal states missing expected journal: zero;
- duplicate business effects: zero;
- negative available/held constrained balances: zero;
- old/new balance difference during migration: zero before cutover.

Required metrics:

```text
ledger_journals_posted_total{event_type,currency}
ledger_post_journal_failures_total{reason}
ledger_idempotent_replays_total{event_type}
ledger_idempotency_conflicts_total{event_type}
ledger_account_lock_seconds
ledger_reconciliation_mismatches_total{type}
ledger_migration_accounts_total{status}
```

Logs must include journal ID, business type/ID, account IDs, amount, currency, result, and trace ID; they must not contain secrets or full provider credentials.

## 16. Implementation roadmap

```text
S0 baseline and contracts
  -> S1 immutable ledger schema and posting engine
      -> S2 account provisioning and card funding binding
          -> S3 opening-balance migration tooling
              -> S4 funding and authorization hold/release
                  -> S5 settlement and refund
                      -> S6 cutover, reconciliation and old-write retirement
```

| Step | Outcome | Depends on | Deployable behavior | Completion gate |
| --- | --- | --- | --- | --- |
| S0 | freeze existing behavior and approved contracts | none | no behavior change | completed: shared lifecycle fixture passes in memory and isolated PostgreSQL; race/tests/vet/E2E pass |
| S1 | books, accounts, journals, postings, balances and balanced posting engine | S0 | dark, internal only | unit/integration tests prove balance, atomicity, immutability, concurrency |
| S2 | shared/dedicated funding accounts and card binding | S1 | feature-flagged, no funds mutation | overlap/currency/status policies proven |
| S3 | idempotent opening-balance migration and reconciliation | S2 | per-card shadow/cutover flag | old/new balances match for available and held |
| S4 | funding, authorization and release use new ledger | S3 | per-card new write path | duplicate, conflict, double-spend and race E2E pass |
| S5 | settlement, refund and correcting reversal | S4 | complete first-phase lifecycle | lifecycle posting matrix and races pass |
| S6 | reconciliation alerts and old write path disabled | S5 | full cutover | zero mismatches; rollback boundary documented and exercised |

### Commit boundaries

| Commit/stage | Must include | Must not include |
| --- | --- | --- |
| C01/S0 | baseline tests and contract fixtures | new schema or behavior |
| C02/S1 | migrations, posting engine, constraints, tests | card handler changes |
| C03/S2 | provisioning/binding model and tests | balance migration |
| C04/S3 | migration/reconciliation tool and tests | automatic global cutover |
| C05/S4 | funding/hold/release integration and E2E | settlement/refund |
| C06/S5 | settlement/refund/reversal integration and E2E | unrelated product features |
| C07/S6 | monitoring, cutover configuration, old-write retirement | table deletion |

### Progress

| Step | Status | Commit/PR | Verification evidence | Blockers |
| --- | --- | --- | --- | --- |
| S0 | Completed locally | not committed | memory contract; isolated PostgreSQL contract; `go test -race ./...`; `go vet ./...`; Kind provider-slice E2E | none |
| S1 | Completed locally | not committed | balanced posting, idempotency, immutability, close and concurrency tests | none |
| S2 | Completed locally | not committed | controlled binding tests for purpose, currency, status, ownership, held pairing and concurrency | none |
| S3 | Completed locally | not committed | idempotent opening-balance migration with source fingerprint and three-line available/held fixture | none |
| S4 | Completed locally | not committed | funding, hold and release use the operational ledger; insufficient funds leave no draft journal | none |
| S5 | Completed locally | not committed | settlement and refund use linked journals; settlement/reversal race has one winner | none |
| S6 | Completed locally | not committed | old runtime write methods removed; full service tests pass; Kind deployment and healthy reconciliation verified | GitOps commit and production rollout remain |

Current execution entry: perform the coach-led end-to-end acceptance against the verified local Kind deployment. GitOps commit, production rollout, external providers, FX, partial lifecycle operations and statutory accounting remain out of scope.

## 17. Verification strategy

The implementation uses the already agreed four public seams:

1. HTTP/provider interface behavior;
2. finance application-service behavior;
3. PostgreSQL transaction and invariant behavior;
4. Kind end-to-end behavior.

High-risk tests include:

- reject an unbalanced journal at commit;
- reject mixed currency or mixed ledger book;
- prove posted rows cannot be updated/deleted;
- prove exact retry returns one journal and changed retry conflicts;
- run concurrent authorizations against the same available account and prevent overspend;
- race reversal against settlement and allow only one terminal transition;
- crash/retry simulation around commit ambiguity;
- rebuild balances from postings and compare projections;
- migrate a balance with both available and held components exactly once;
- add a second USD budget account without schema change;
- bind two cards to one shared account and one card to a dedicated account.

## 18. Deferred decisions

These are not implementation blockers for the first phase:

- pending postings versus posted available/held reclassification;
- partial settlement, incremental authorization, and multiple refunds;
- cross-currency journals and foreign-exchange gain/loss;
- split funding across multiple funding sources;
- extracting the ledger module into an independently deployed service;
- statutory general-ledger export and period close;
- provider payout and bank-statement reconciliation.

Each requires a new business use case and a revision to this design before implementation.

## 19. Reference principles

- Modern Treasury documents balanced entries, write atomicity, immutability, idempotency, ledger isolation, and account-version locking: <https://docs.moderntreasury.com/ledgers/docs/ledgers-guarantees>
- Modern Treasury distinguishes posted, pending, and available balances: <https://docs.moderntreasury.com/ledgers/docs/transaction-status-and-balances>
- Formance models immutable transactions as containers for postings and derives balances from posting history: <https://docs.formance.com/v3.2/modules/ledger/core-concepts/transactions>
- TigerBeetle models authorization-like reservation as a pending transfer resolved by post, void, or expiry: <https://docs.tigerbeetle.com/coding/two-phase-transfers/>
- PostgreSQL supports deferred constraint triggers that can reject a transaction at commit: <https://www.postgresql.org/docs/17/sql-createtrigger.html>
