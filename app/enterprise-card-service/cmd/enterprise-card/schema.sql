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
