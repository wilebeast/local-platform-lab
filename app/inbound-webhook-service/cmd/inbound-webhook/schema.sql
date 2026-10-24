CREATE TABLE IF NOT EXISTS provider_webhook_inbox (
    id BIGSERIAL PRIMARY KEY,
    integration_code VARCHAR(64) NOT NULL,
    domain VARCHAR(32) NOT NULL,
    external_event_id VARCHAR(160) NOT NULL,
    event_type VARCHAR(160) NOT NULL,
    event_version INTEGER NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    credential_key_id VARCHAR(100) NOT NULL,
    raw_headers JSONB NOT NULL,
    raw_body BYTEA NOT NULL,
    body_sha256 CHAR(64) NOT NULL,
    processing_status VARCHAR(24) NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_at TIMESTAMPTZ,
    processed_at TIMESTAMPTZ,
    last_error TEXT,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (integration_code, external_event_id),
    CHECK (processing_status IN ('pending', 'processing', 'retry', 'processed', 'dead_letter'))
);

CREATE INDEX IF NOT EXISTS provider_webhook_inbox_ready_idx
    ON provider_webhook_inbox (processing_status, next_attempt_at, id);

CREATE TABLE IF NOT EXISTS demo_payment_projection (
    resource_id VARCHAR(160) PRIMARY KEY,
    status VARCHAR(64) NOT NULL,
    source_integration VARCHAR(64) NOT NULL,
    source_event_id VARCHAR(160) NOT NULL,
    source_occurred_at BIGINT NOT NULL,
    payload JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS demo_card_projection (
    LIKE demo_payment_projection INCLUDING ALL
);

CREATE TABLE IF NOT EXISTS demo_verification_projection (
    LIKE demo_payment_projection INCLUDING ALL
);
