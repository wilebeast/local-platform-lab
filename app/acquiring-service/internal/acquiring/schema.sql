CREATE TABLE IF NOT EXISTS acquiring_payment_orders (
    payment_id VARCHAR(64) PRIMARY KEY,
    merchant_id VARCHAR(64) NOT NULL,
    merchant_order_id VARCHAR(128) NOT NULL,
    idempotency_key VARCHAR(160) NOT NULL,
    command_fingerprint CHAR(64) NOT NULL,
    currency CHAR(3) NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    fee_minor BIGINT NOT NULL CHECK (fee_minor >= 0 AND fee_minor < amount_minor),
    merchant_net_minor BIGINT NOT NULL CHECK (merchant_net_minor = amount_minor - fee_minor),
    status VARCHAR(24) NOT NULL CHECK (status IN ('created','processing','succeeded','refunded','paid_out')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (merchant_id, idempotency_key),
    UNIQUE (merchant_id, merchant_order_id)
);

ALTER TABLE acquiring_payment_orders DROP CONSTRAINT IF EXISTS acquiring_payment_orders_status_check;
ALTER TABLE acquiring_payment_orders ADD CONSTRAINT acquiring_payment_orders_status_check
    CHECK (status IN ('created','processing','succeeded','refunded','paid_out'));

CREATE TABLE IF NOT EXISTS acquiring_payment_attempts (
    attempt_id VARCHAR(64) PRIMARY KEY,
    payment_id VARCHAR(64) NOT NULL REFERENCES acquiring_payment_orders(payment_id),
    payment_method VARCHAR(32) NOT NULL,
    provider VARCHAR(48) NOT NULL,
    status VARCHAR(24) NOT NULL CHECK (status IN ('created','provider_pending','succeeded','failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (payment_id, attempt_id)
);

CREATE TABLE IF NOT EXISTS acquiring_channel_operations (
    operation_id VARCHAR(64) PRIMARY KEY,
    attempt_id VARCHAR(64) NOT NULL UNIQUE REFERENCES acquiring_payment_attempts(attempt_id),
    provider VARCHAR(48) NOT NULL,
    provider_request_id VARCHAR(64) NOT NULL UNIQUE,
    status VARCHAR(24) NOT NULL CHECK (status IN ('pending','executing','unknown','succeeded','failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS acquiring_provider_inbox (
    inbox_id BIGSERIAL PRIMARY KEY,
    provider VARCHAR(48) NOT NULL,
    event_id VARCHAR(160) NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    provider_request_id VARCHAR(64) NOT NULL,
    occurred_at BIGINT NOT NULL,
    raw_body JSONB NOT NULL,
    processing_status VARCHAR(24) NOT NULL DEFAULT 'pending'
        CHECK (processing_status IN ('pending','processing','processed','dead_letter')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error TEXT NOT NULL DEFAULT '',
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ,
    UNIQUE (provider, event_id)
);

CREATE TABLE IF NOT EXISTS acquiring_refunds (
    refund_id VARCHAR(64) PRIMARY KEY,
    merchant_id VARCHAR(64) NOT NULL,
    payment_id VARCHAR(64) NOT NULL UNIQUE REFERENCES acquiring_payment_orders(payment_id),
    idempotency_key VARCHAR(160) NOT NULL,
    command_fingerprint CHAR(64) NOT NULL,
    currency CHAR(3) NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    fee_minor BIGINT NOT NULL CHECK (fee_minor >= 0 AND fee_minor < amount_minor),
    status VARCHAR(24) NOT NULL CHECK (status IN ('refunded')),
    refund_journal_id VARCHAR(160) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (merchant_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS acquiring_channel_settlements (
    settlement_id VARCHAR(64) PRIMARY KEY,
    merchant_id VARCHAR(64) NOT NULL,
    payment_id VARCHAR(64) NOT NULL UNIQUE REFERENCES acquiring_payment_orders(payment_id),
    refund_id VARCHAR(64) NOT NULL UNIQUE REFERENCES acquiring_refunds(refund_id),
    provider VARCHAR(48) NOT NULL,
    idempotency_key VARCHAR(160) NOT NULL,
    command_fingerprint CHAR(64) NOT NULL,
    currency CHAR(3) NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    status VARCHAR(24) NOT NULL CHECK (status IN ('netted')),
    settlement_journal_id VARCHAR(160) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (merchant_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS acquiring_channel_cash_settlements (
    settlement_id VARCHAR(64) PRIMARY KEY,
    merchant_id VARCHAR(64) NOT NULL,
    payment_id VARCHAR(64) NOT NULL UNIQUE REFERENCES acquiring_payment_orders(payment_id),
    provider VARCHAR(48) NOT NULL,
    idempotency_key VARCHAR(160) NOT NULL,
    command_fingerprint CHAR(64) NOT NULL,
    currency CHAR(3) NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    status VARCHAR(24) NOT NULL CHECK (status IN ('received')),
    settlement_journal_id VARCHAR(160) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (merchant_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS acquiring_merchant_payouts (
    payout_id VARCHAR(64) PRIMARY KEY,
    merchant_id VARCHAR(64) NOT NULL,
    payment_id VARCHAR(64) NOT NULL UNIQUE REFERENCES acquiring_payment_orders(payment_id),
    cash_settlement_id VARCHAR(64) NOT NULL UNIQUE REFERENCES acquiring_channel_cash_settlements(settlement_id),
    idempotency_key VARCHAR(160) NOT NULL,
    command_fingerprint CHAR(64) NOT NULL,
    currency CHAR(3) NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    status VARCHAR(24) NOT NULL CHECK (status IN ('paid')),
    payout_journal_id VARCHAR(160) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (merchant_id, idempotency_key)
);

ALTER TABLE acquiring_merchant_payouts DROP CONSTRAINT IF EXISTS acquiring_merchant_payouts_status_check;
ALTER TABLE acquiring_merchant_payouts ADD CONSTRAINT acquiring_merchant_payouts_status_check
    CHECK (status IN ('paid','reversed'));
ALTER TABLE acquiring_merchant_payouts DROP CONSTRAINT IF EXISTS acquiring_merchant_payouts_payment_id_key;
ALTER TABLE acquiring_merchant_payouts DROP CONSTRAINT IF EXISTS acquiring_merchant_payouts_cash_settlement_id_key;
CREATE UNIQUE INDEX IF NOT EXISTS acquiring_merchant_payouts_active_payment_idx
    ON acquiring_merchant_payouts(payment_id) WHERE status='paid';
CREATE UNIQUE INDEX IF NOT EXISTS acquiring_merchant_payouts_active_cash_settlement_idx
    ON acquiring_merchant_payouts(cash_settlement_id) WHERE status='paid';

CREATE TABLE IF NOT EXISTS acquiring_reconciliation_runs (
    run_id VARCHAR(64) PRIMARY KEY,
    provider VARCHAR(48) NOT NULL,
    statement_id VARCHAR(160) NOT NULL,
    idempotency_key VARCHAR(160) NOT NULL,
    command_fingerprint CHAR(64) NOT NULL,
    period_start BIGINT NOT NULL,
    period_end BIGINT NOT NULL CHECK (period_end > period_start),
    status VARCHAR(24) NOT NULL CHECK (status IN ('completed')),
    matched_count INTEGER NOT NULL DEFAULT 0 CHECK (matched_count >= 0),
    issue_count INTEGER NOT NULL DEFAULT 0 CHECK (issue_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (provider, statement_id),
    UNIQUE (provider, idempotency_key)
);

CREATE TABLE IF NOT EXISTS acquiring_reconciliation_lines (
    run_id VARCHAR(64) NOT NULL REFERENCES acquiring_reconciliation_runs(run_id),
    provider_payout_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (run_id, provider_payout_id)
);

CREATE TABLE IF NOT EXISTS acquiring_reconciliation_issues (
    issue_id VARCHAR(64) PRIMARY KEY,
    run_id VARCHAR(64) NOT NULL REFERENCES acquiring_reconciliation_runs(run_id),
    issue_type VARCHAR(48) NOT NULL CHECK (issue_type IN ('missing_provider_payout','unexpected_provider_payout')),
    business_type VARCHAR(48) NOT NULL,
    business_id VARCHAR(64) NOT NULL,
    currency CHAR(3) NOT NULL,
    expected_amount_minor BIGINT NOT NULL CHECK (expected_amount_minor >= 0),
    status VARCHAR(24) NOT NULL CHECK (status IN ('open','resolved')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ,
    UNIQUE (run_id, issue_type, business_id)
);

CREATE TABLE IF NOT EXISTS acquiring_reconciliation_resolutions (
    resolution_id VARCHAR(64) PRIMARY KEY,
    issue_id VARCHAR(64) NOT NULL UNIQUE REFERENCES acquiring_reconciliation_issues(issue_id),
    idempotency_key VARCHAR(160) NOT NULL UNIQUE,
    command_fingerprint CHAR(64) NOT NULL,
    resolution_type VARCHAR(48) NOT NULL CHECK (resolution_type IN ('provider_confirmed_late')),
    evidence_reference VARCHAR(240) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE acquiring_reconciliation_resolutions ADD COLUMN IF NOT EXISTS reversal_journal_id VARCHAR(160) NOT NULL DEFAULT '';
ALTER TABLE acquiring_reconciliation_resolutions DROP CONSTRAINT IF EXISTS acquiring_reconciliation_resolutions_resolution_type_check;
ALTER TABLE acquiring_reconciliation_resolutions ADD CONSTRAINT acquiring_reconciliation_resolutions_resolution_type_check
    CHECK (resolution_type IN ('provider_confirmed_late','local_payout_not_sent'));

INSERT INTO ledger_books(id,legal_entity_id,name,base_currency,status)
VALUES('book-acquiring-local','platform-local','Acquiring Operational Ledger','USD','active')
ON CONFLICT(id) DO NOTHING;

ALTER TABLE ledger_journals DROP CONSTRAINT IF EXISTS ledger_journals_event_type_check;
ALTER TABLE ledger_journals ADD CONSTRAINT ledger_journals_event_type_check CHECK (
    event_type IN ('opening_balance','funding','hold','hold_release','settlement','provider_payout',
                   'refund','refund_recovery','reversal','capture','channel_settlement','merchant_payout')
);
