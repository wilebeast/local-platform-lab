CREATE TABLE IF NOT EXISTS card_lab_applications (
    idempotency_key VARCHAR(128) PRIMARY KEY,
    organization_id VARCHAR(128) NOT NULL,
    holder_id VARCHAR(128) NOT NULL,
    alias VARCHAR(160) NOT NULL,
    card_id VARCHAR(128) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS card_lab_cards (
    id VARCHAR(128) PRIMARY KEY,
    organization_id VARCHAR(128) NOT NULL,
    holder_id VARCHAR(128) NOT NULL,
    alias VARCHAR(160) NOT NULL,
    status VARCHAR(32) NOT NULL,
    currency CHAR(3) NOT NULL,
    balance_minor BIGINT NOT NULL DEFAULT 0,
    provider_card_id VARCHAR(160) NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	provider_status_occurred_at BIGINT NOT NULL DEFAULT 0,
	provider_status_event_id VARCHAR(160) NOT NULL DEFAULT '',
    CHECK (status IN ('pending', 'active', 'frozen', 'closed')),
    CHECK (balance_minor >= 0)
);

ALTER TABLE card_lab_cards ADD COLUMN IF NOT EXISTS provider_card_id VARCHAR(160) NOT NULL DEFAULT '';
ALTER TABLE card_lab_cards ADD COLUMN IF NOT EXISTS provider_status_occurred_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE card_lab_cards ADD COLUMN IF NOT EXISTS provider_status_event_id VARCHAR(160) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS card_lab_cards_organization_idx
    ON card_lab_cards (organization_id, created_at, id);

CREATE UNIQUE INDEX IF NOT EXISTS card_lab_cards_provider_card_id_unique_idx
    ON card_lab_cards (provider_card_id)
    WHERE provider_card_id <> '';

CREATE TABLE IF NOT EXISTS card_lab_channel_operations (
    operation_id VARCHAR(160) PRIMARY KEY,
    card_id VARCHAR(128) NOT NULL REFERENCES card_lab_cards(id),
    operation_type VARCHAR(32) NOT NULL,
    status VARCHAR(24) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL UNIQUE,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT,
    created_at BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (status IN ('pending', 'processing', 'completed', 'retry', 'dead_letter'))
);

CREATE TABLE IF NOT EXISTS card_lab_provider_events (
    event_id VARCHAR(160) PRIMARY KEY,
    event_type VARCHAR(80) NOT NULL,
    event_version INTEGER NOT NULL,
    provider_card_id VARCHAR(160) NOT NULL,
    target_status VARCHAR(32) NOT NULL,
    raw_body JSONB NOT NULL,
    processing_status VARCHAR(24) NOT NULL DEFAULT 'pending',
	processing_result VARCHAR(32) NOT NULL DEFAULT '',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    occurred_at BIGINT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ,
    last_error TEXT,
    CHECK (target_status IN ('active', 'frozen')),
    CHECK (processing_status IN ('pending', 'processing', 'processed', 'dead_letter'))
);

ALTER TABLE card_lab_provider_events ADD COLUMN IF NOT EXISTS processing_result VARCHAR(32) NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS card_lab_accounts (
    card_id VARCHAR(128) PRIMARY KEY REFERENCES card_lab_cards(id),
    currency CHAR(3) NOT NULL,
    posted_balance_minor BIGINT NOT NULL DEFAULT 0,
    held_amount_minor BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (posted_balance_minor >= 0),
    CHECK (held_amount_minor >= 0),
    CHECK (held_amount_minor <= posted_balance_minor)
);

CREATE TABLE IF NOT EXISTS card_lab_ledger_entries (
    entry_id VARCHAR(160) PRIMARY KEY,
    card_id VARCHAR(128) NOT NULL REFERENCES card_lab_cards(id),
    entry_type VARCHAR(32) NOT NULL,
    currency CHAR(3) NOT NULL,
    amount_minor BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (entry_type IN ('funding','settlement','refund')),
    CHECK (amount_minor > 0)
);

ALTER TABLE card_lab_ledger_entries DROP CONSTRAINT IF EXISTS card_lab_ledger_entries_entry_type_check;
ALTER TABLE card_lab_ledger_entries ADD CONSTRAINT card_lab_ledger_entries_entry_type_check CHECK (entry_type IN ('funding','settlement','refund'));

CREATE TABLE IF NOT EXISTS card_lab_authorizations (
    authorization_id VARCHAR(160) PRIMARY KEY,
    provider_card_id VARCHAR(160) NOT NULL,
    card_id VARCHAR(128) REFERENCES card_lab_cards(id),
    currency CHAR(3) NOT NULL,
    amount_minor BIGINT NOT NULL,
    decision VARCHAR(24) NOT NULL,
    reason VARCHAR(64) NOT NULL DEFAULT '',
    posted_balance_after BIGINT NOT NULL,
    held_amount_after BIGINT NOT NULL,
    available_balance_after BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (amount_minor > 0),
    CHECK (decision IN ('approved','declined'))
);

ALTER TABLE card_lab_authorizations ADD COLUMN IF NOT EXISTS authorization_status VARCHAR(24) NOT NULL DEFAULT 'authorized';
UPDATE card_lab_authorizations SET authorization_status='declined' WHERE decision='declined' AND authorization_status='authorized';
ALTER TABLE card_lab_authorizations DROP CONSTRAINT IF EXISTS card_lab_authorizations_status_check;
ALTER TABLE card_lab_authorizations ADD CONSTRAINT card_lab_authorizations_status_check CHECK (
    (decision = 'declined' AND authorization_status = 'declined') OR
    (decision = 'approved' AND authorization_status IN ('authorized','reversed','settled','refunded'))
);

CREATE TABLE IF NOT EXISTS card_lab_authorization_reversals (
    reversal_id VARCHAR(160) PRIMARY KEY,
    authorization_id VARCHAR(160) NOT NULL UNIQUE REFERENCES card_lab_authorizations(authorization_id),
    released_amount_minor BIGINT NOT NULL,
    posted_balance_after BIGINT NOT NULL,
    held_amount_after BIGINT NOT NULL,
    available_balance_after BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (released_amount_minor > 0)
);

CREATE TABLE IF NOT EXISTS card_lab_authorization_settlements (
    settlement_id VARCHAR(160) PRIMARY KEY,
    authorization_id VARCHAR(160) NOT NULL UNIQUE REFERENCES card_lab_authorizations(authorization_id),
    settled_amount_minor BIGINT NOT NULL,
    posted_balance_after BIGINT NOT NULL,
    held_amount_after BIGINT NOT NULL,
    available_balance_after BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (settled_amount_minor > 0)
);

CREATE TABLE IF NOT EXISTS card_lab_settlement_refunds (
    refund_id VARCHAR(160) PRIMARY KEY,
    settlement_id VARCHAR(160) NOT NULL UNIQUE REFERENCES card_lab_authorization_settlements(settlement_id),
    refunded_amount_minor BIGINT NOT NULL,
    posted_balance_after BIGINT NOT NULL,
    held_amount_after BIGINT NOT NULL,
    available_balance_after BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (refunded_amount_minor > 0)
);

CREATE TABLE IF NOT EXISTS ledger_books (
    id VARCHAR(64) PRIMARY KEY,
    legal_entity_id VARCHAR(128) NOT NULL DEFAULT '',
    name VARCHAR(128) NOT NULL DEFAULT '',
    base_currency CHAR(3) NOT NULL DEFAULT 'USD',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (id <> ''),
    CHECK (legal_entity_id <> ''),
    CHECK (name <> ''),
    CHECK (base_currency ~ '^[A-Z]{3}$'),
    CHECK (status IN ('active','closed'))
);

CREATE TABLE IF NOT EXISTS ledger_accounts (
    id VARCHAR(96) PRIMARY KEY,
    book_id VARCHAR(64) NOT NULL DEFAULT '' REFERENCES ledger_books(id),
    account_key VARCHAR(160) NOT NULL DEFAULT '',
    account_group_id VARCHAR(96) NOT NULL DEFAULT '',
    account_type VARCHAR(16) NOT NULL DEFAULT '',
    normal_side VARCHAR(8) NOT NULL DEFAULT '',
    owner_type VARCHAR(24) NOT NULL DEFAULT '',
    owner_id VARCHAR(128) NOT NULL DEFAULT '',
    currency CHAR(3) NOT NULL DEFAULT 'USD',
    purpose VARCHAR(32) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (book_id, account_key),
    CHECK (id <> ''),
    CHECK (book_id <> ''),
    CHECK (account_key <> ''),
    CHECK (account_group_id <> ''),
    CHECK (account_type IN ('asset','liability','revenue','expense')),
    CHECK (normal_side IN ('debit','credit')),
    CHECK (owner_type IN ('platform','organization','provider')),
    CHECK (owner_id <> ''),
    CHECK (currency ~ '^[A-Z]{3}$'),
    CHECK (purpose IN ('cash','available','held','payable','receivable','fee')),
    CHECK (status IN ('active','frozen','closed')),
    CHECK (version >= 0),
    CHECK (
        (account_type IN ('asset','expense') AND normal_side = 'debit') OR
        (account_type IN ('liability','revenue') AND normal_side = 'credit')
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS ledger_accounts_funding_pool_purpose_unique_idx
    ON ledger_accounts(book_id,account_group_id,purpose)
    WHERE purpose IN ('available','held');

CREATE TABLE IF NOT EXISTS card_funding_sources (
    id VARCHAR(96) PRIMARY KEY,
    card_id VARCHAR(128) NOT NULL REFERENCES card_lab_cards(id),
    account_id VARCHAR(96) NOT NULL REFERENCES ledger_accounts(id),
    priority INTEGER NOT NULL DEFAULT 0,
    spending_limit_minor BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    effective_from BIGINT NOT NULL DEFAULT 0,
    effective_to BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (id <> ''),
    CHECK (priority = 0),
    CHECK (spending_limit_minor >= 0),
    CHECK (status IN ('active','inactive')),
    CHECK (effective_from >= 0),
    CHECK (effective_to = 0 OR effective_to > effective_from)
);

CREATE UNIQUE INDEX IF NOT EXISTS card_funding_sources_one_active_per_card_idx
    ON card_funding_sources(card_id)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS card_funding_sources_account_status_idx
    ON card_funding_sources(account_id,status);

CREATE OR REPLACE FUNCTION validate_active_card_funding_source()
RETURNS TRIGGER AS $$
DECLARE
    account_owner_type VARCHAR(24);
    account_owner_id VARCHAR(128);
    account_purpose VARCHAR(32);
    account_currency CHAR(3);
    account_status VARCHAR(16);
    card_organization_id VARCHAR(128);
    card_currency CHAR(3);
    has_matching_held_account BOOLEAN;
BEGIN
    IF NEW.status <> 'active' THEN
        RETURN NEW;
    END IF;

    SELECT owner_type, owner_id, purpose, currency, status
    INTO account_owner_type, account_owner_id, account_purpose, account_currency, account_status
    FROM ledger_accounts
    WHERE id = NEW.account_id;

    IF account_purpose <> 'available' THEN
        RAISE EXCEPTION 'funding source account must have available purpose'
            USING ERRCODE = '23514';
    END IF;

    SELECT organization_id, currency
    INTO card_organization_id, card_currency
    FROM card_lab_cards
    WHERE id = NEW.card_id;

    IF account_currency <> card_currency THEN
        RAISE EXCEPTION 'card and funding source account use different currencies'
            USING ERRCODE = '23514';
    END IF;

    IF account_status = 'closed' THEN
        RAISE EXCEPTION 'closed ledger account cannot be a funding source'
            USING ERRCODE = '23514';
    END IF;

    IF account_owner_type <> 'organization' OR account_owner_id <> card_organization_id THEN
        RAISE EXCEPTION 'card and funding source account belong to different organizations'
            USING ERRCODE = '23514';
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM ledger_accounts available
        JOIN ledger_accounts held
          ON held.book_id = available.book_id
         AND held.account_group_id = available.account_group_id
         AND held.owner_type = available.owner_type
         AND held.owner_id = available.owner_id
         AND held.currency = available.currency
         AND held.purpose = 'held'
         AND held.status <> 'closed'
        WHERE available.id = NEW.account_id
    )
    INTO has_matching_held_account;

    IF NOT has_matching_held_account THEN
        RAISE EXCEPTION 'funding source account must have a matching held account'
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS card_funding_sources_valid_when_active ON card_funding_sources;
CREATE TRIGGER card_funding_sources_valid_when_active
BEFORE INSERT OR UPDATE ON card_funding_sources
FOR EACH ROW EXECUTE FUNCTION validate_active_card_funding_source();

ALTER TABLE card_lab_authorizations ADD COLUMN IF NOT EXISTS funding_source_id VARCHAR(96) NOT NULL DEFAULT '';
ALTER TABLE card_lab_authorizations ADD COLUMN IF NOT EXISTS available_account_id VARCHAR(96) NOT NULL DEFAULT '';
ALTER TABLE card_lab_authorizations ADD COLUMN IF NOT EXISTS held_account_id VARCHAR(96) NOT NULL DEFAULT '';
ALTER TABLE card_lab_authorizations ADD COLUMN IF NOT EXISTS hold_journal_id VARCHAR(160) NOT NULL DEFAULT '';
ALTER TABLE card_lab_authorizations ADD COLUMN IF NOT EXISTS ledger_version SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE card_lab_authorization_reversals ADD COLUMN IF NOT EXISTS release_journal_id VARCHAR(160) NOT NULL DEFAULT '';
ALTER TABLE card_lab_authorization_reversals ADD COLUMN IF NOT EXISTS ledger_version SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE card_lab_authorization_settlements ADD COLUMN IF NOT EXISTS settlement_journal_id VARCHAR(160) NOT NULL DEFAULT '';
ALTER TABLE card_lab_authorization_settlements ADD COLUMN IF NOT EXISTS ledger_version SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE card_lab_settlement_refunds ADD COLUMN IF NOT EXISTS refund_journal_id VARCHAR(160) NOT NULL DEFAULT '';
ALTER TABLE card_lab_settlement_refunds ADD COLUMN IF NOT EXISTS ledger_version SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE card_lab_authorizations DROP CONSTRAINT IF EXISTS card_lab_authorizations_ledger_version_check;
ALTER TABLE card_lab_authorizations ADD CONSTRAINT card_lab_authorizations_ledger_version_check CHECK (ledger_version IN (0,1));
ALTER TABLE card_lab_authorization_reversals DROP CONSTRAINT IF EXISTS card_lab_authorization_reversals_ledger_version_check;
ALTER TABLE card_lab_authorization_reversals ADD CONSTRAINT card_lab_authorization_reversals_ledger_version_check CHECK (ledger_version IN (0,1));
ALTER TABLE card_lab_authorization_settlements DROP CONSTRAINT IF EXISTS card_lab_authorization_settlements_ledger_version_check;
ALTER TABLE card_lab_authorization_settlements ADD CONSTRAINT card_lab_authorization_settlements_ledger_version_check CHECK (ledger_version IN (0,1));
ALTER TABLE card_lab_settlement_refunds DROP CONSTRAINT IF EXISTS card_lab_settlement_refunds_ledger_version_check;
ALTER TABLE card_lab_settlement_refunds ADD CONSTRAINT card_lab_settlement_refunds_ledger_version_check CHECK (ledger_version IN (0,1));

CREATE TABLE IF NOT EXISTS ledger_journals (
    id VARCHAR(160) PRIMARY KEY,
    book_id VARCHAR(64) NOT NULL DEFAULT '' REFERENCES ledger_books(id),
    business_type VARCHAR(48) NOT NULL DEFAULT '',
    business_id VARCHAR(160) NOT NULL DEFAULT '',
    event_type VARCHAR(48) NOT NULL DEFAULT '',
    currency CHAR(3) NOT NULL DEFAULT 'USD',
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    reversal_of VARCHAR(160) NOT NULL DEFAULT '',
    command_fingerprint CHAR(64) NOT NULL DEFAULT '',
    effective_at BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    posted_at BIGINT NOT NULL DEFAULT 0,
    UNIQUE (book_id,business_type,business_id,event_type),
    CHECK (id <> ''),
    CHECK (book_id <> ''),
    CHECK (business_type <> ''),
    CHECK (business_id <> ''),
    CHECK (event_type IN ('opening_balance','funding','hold','hold_release','settlement','provider_payout','refund','refund_recovery','reversal')),
    CHECK (currency ~ '^[A-Z]{3}$'),
    CHECK (status IN ('draft','posted')),
    CHECK (command_fingerprint = '' OR command_fingerprint ~ '^[0-9a-f]{64}$'),
    CHECK (effective_at >= 0),
    CHECK ((status = 'draft' AND posted_at = 0) OR (status = 'posted' AND posted_at > 0))
);

CREATE UNIQUE INDEX IF NOT EXISTS ledger_journals_reversal_once_idx
    ON ledger_journals(reversal_of)
    WHERE reversal_of <> '';

CREATE TABLE IF NOT EXISTS ledger_postings (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    journal_id VARCHAR(160) NOT NULL DEFAULT '' REFERENCES ledger_journals(id),
    sequence_no SMALLINT NOT NULL DEFAULT 0,
    account_id VARCHAR(96) NOT NULL DEFAULT '' REFERENCES ledger_accounts(id),
    side VARCHAR(8) NOT NULL DEFAULT '',
    amount_minor BIGINT NOT NULL DEFAULT 0,
    currency CHAR(3) NOT NULL DEFAULT 'USD',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (journal_id,sequence_no),
    CHECK (journal_id <> ''),
    CHECK (sequence_no > 0),
    CHECK (account_id <> ''),
    CHECK (side IN ('debit','credit')),
    CHECK (amount_minor > 0),
    CHECK (currency ~ '^[A-Z]{3}$')
);

CREATE OR REPLACE FUNCTION validate_ledger_journal_when_posted()
RETURNS TRIGGER AS $$
DECLARE
    posting_count BIGINT;
    debit_total BIGINT;
    credit_total BIGINT;
    has_account_from_another_book BOOLEAN;
    has_account_with_another_currency BOOLEAN;
    has_posting_with_another_currency BOOLEAN;
    has_closed_account BOOLEAN;
BEGIN
    IF NEW.status <> 'posted' THEN
        RETURN NEW;
    END IF;

    IF TG_OP = 'UPDATE' AND OLD.status = 'posted' THEN
        RETURN NEW;
    END IF;

    SELECT
        COUNT(*),
        COALESCE(SUM(amount_minor) FILTER (WHERE side = 'debit'), 0),
        COALESCE(SUM(amount_minor) FILTER (WHERE side = 'credit'), 0)
    INTO posting_count, debit_total, credit_total
    FROM ledger_postings
    WHERE journal_id = NEW.id;

    IF posting_count < 2 OR debit_total <> credit_total THEN
        RAISE EXCEPTION 'ledger journal is not balanced'
            USING ERRCODE = '23514';
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM ledger_postings posting
        JOIN ledger_accounts account ON account.id = posting.account_id
        WHERE posting.journal_id = NEW.id
          AND account.book_id <> NEW.book_id
    )
    INTO has_account_from_another_book;

    IF has_account_from_another_book THEN
        RAISE EXCEPTION 'ledger posting account does not belong to journal book'
            USING ERRCODE = '23514';
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM ledger_postings posting
        JOIN ledger_accounts account ON account.id = posting.account_id
        WHERE posting.journal_id = NEW.id
          AND account.currency <> NEW.currency
    )
    INTO has_account_with_another_currency;

    IF has_account_with_another_currency THEN
        RAISE EXCEPTION 'ledger posting account currency does not match journal currency'
            USING ERRCODE = '23514';
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM ledger_postings posting
        WHERE posting.journal_id = NEW.id
          AND posting.currency <> NEW.currency
    )
    INTO has_posting_with_another_currency;

    IF has_posting_with_another_currency THEN
        RAISE EXCEPTION 'ledger posting currency does not match journal currency'
            USING ERRCODE = '23514';
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM ledger_postings posting
        JOIN ledger_accounts account ON account.id = posting.account_id
        WHERE posting.journal_id = NEW.id
          AND account.status = 'closed'
    )
    INTO has_closed_account;

    IF has_closed_account THEN
        RAISE EXCEPTION 'closed ledger account cannot receive postings'
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS ledger_journals_balanced_when_posted ON ledger_journals;
CREATE TRIGGER ledger_journals_balanced_when_posted
BEFORE INSERT OR UPDATE ON ledger_journals
FOR EACH ROW EXECUTE FUNCTION validate_ledger_journal_when_posted();

CREATE OR REPLACE FUNCTION reject_posted_ledger_journal_mutation()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status = 'posted' THEN
        RAISE EXCEPTION 'posted ledger journal is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS ledger_journals_immutable_when_posted ON ledger_journals;
CREATE TRIGGER ledger_journals_immutable_when_posted
BEFORE UPDATE OR DELETE ON ledger_journals
FOR EACH ROW EXECUTE FUNCTION reject_posted_ledger_journal_mutation();

CREATE OR REPLACE FUNCTION reject_posted_ledger_posting_mutation()
RETURNS TRIGGER AS $$
DECLARE
    journal_status VARCHAR(16);
BEGIN
    IF TG_OP IN ('UPDATE','DELETE') THEN
        SELECT status INTO journal_status
        FROM ledger_journals
        WHERE id = OLD.journal_id;
        IF journal_status = 'posted' THEN
            RAISE EXCEPTION 'posted ledger postings are immutable'
                USING ERRCODE = '55000';
        END IF;
    END IF;

    IF TG_OP IN ('INSERT','UPDATE') THEN
        SELECT status INTO journal_status
        FROM ledger_journals
        WHERE id = NEW.journal_id;
        IF journal_status = 'posted' THEN
            RAISE EXCEPTION 'posted ledger postings are immutable'
                USING ERRCODE = '55000';
        END IF;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS ledger_postings_immutable_when_posted ON ledger_postings;
CREATE TRIGGER ledger_postings_immutable_when_posted
BEFORE INSERT OR UPDATE OR DELETE ON ledger_postings
FOR EACH ROW EXECUTE FUNCTION reject_posted_ledger_posting_mutation();

CREATE TABLE IF NOT EXISTS ledger_balances (
    account_id VARCHAR(96) PRIMARY KEY REFERENCES ledger_accounts(id),
    posted_debits_minor BIGINT NOT NULL DEFAULT 0,
    posted_credits_minor BIGINT NOT NULL DEFAULT 0,
    version BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (posted_debits_minor >= 0),
    CHECK (posted_credits_minor >= 0),
    CHECK (version >= 0)
);

CREATE TABLE IF NOT EXISTS ledger_migrations (
    id VARCHAR(96) PRIMARY KEY,
    source_type VARCHAR(32) NOT NULL,
    source_id VARCHAR(160) NOT NULL,
    source_fingerprint CHAR(64) NOT NULL,
    opening_journal_id VARCHAR(160) NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    UNIQUE (source_type,source_id),
    CHECK (source_type IN ('legacy_card_account')),
    CHECK (source_fingerprint ~ '^[0-9a-f]{64}$'),
    CHECK (status IN ('pending','completed','failed')),
    CHECK (attempt_count >= 0)
);

CREATE OR REPLACE FUNCTION validate_ledger_account_when_closed()
RETURNS TRIGGER AS $$
DECLARE
    posted_debits BIGINT;
    posted_credits BIGINT;
    effective_balance BIGINT;
    has_active_funding_source BOOLEAN;
    has_outstanding_authorization BOOLEAN;
BEGIN
    IF NEW.status <> 'closed' OR OLD.status = 'closed' THEN
        RETURN NEW;
    END IF;

    SELECT posted_debits_minor, posted_credits_minor
    INTO posted_debits, posted_credits
    FROM ledger_balances
    WHERE account_id = NEW.id;

    posted_debits := COALESCE(posted_debits, 0);
    posted_credits := COALESCE(posted_credits, 0);
    IF NEW.normal_side = 'debit' THEN
        effective_balance := posted_debits - posted_credits;
    ELSE
        effective_balance := posted_credits - posted_debits;
    END IF;

    IF effective_balance <> 0 THEN
        RAISE EXCEPTION 'non-zero ledger account cannot be closed'
            USING ERRCODE = '23514';
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM card_funding_sources
        WHERE account_id = NEW.id
          AND status = 'active'
          AND effective_from <= EXTRACT(EPOCH FROM clock_timestamp())::BIGINT
          AND (
              effective_to = 0 OR
              effective_to > EXTRACT(EPOCH FROM clock_timestamp())::BIGINT
          )
    )
    INTO has_active_funding_source;

    IF has_active_funding_source THEN
        RAISE EXCEPTION 'ledger account with active funding source cannot be closed'
            USING ERRCODE = '23514';
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM card_lab_authorizations auth
        JOIN card_funding_sources funding_source
          ON funding_source.card_id = auth.card_id
        WHERE funding_source.account_id = NEW.id
          AND auth.decision = 'approved'
          AND auth.authorization_status = 'authorized'
    )
    INTO has_outstanding_authorization;

    IF has_outstanding_authorization THEN
        RAISE EXCEPTION 'ledger account with outstanding authorization cannot be closed'
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS ledger_accounts_valid_when_closed ON ledger_accounts;
CREATE TRIGGER ledger_accounts_valid_when_closed
BEFORE UPDATE ON ledger_accounts
FOR EACH ROW EXECUTE FUNCTION validate_ledger_account_when_closed();
