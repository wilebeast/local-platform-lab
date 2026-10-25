CREATE TABLE IF NOT EXISTS risk_customers (
    customer_id TEXT PRIMARY KEY,
    verification_status TEXT NOT NULL,
    residence_country TEXT NOT NULL,
    poa_verified BOOLEAN NOT NULL,
    questionnaire_completed BOOLEAN NOT NULL,
    fact_version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS risk_policy_versions (
    product_code TEXT NOT NULL,
    policy_version INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('draft', 'published', 'retired')),
    config JSONB NOT NULL,
    published_at TIMESTAMPTZ,
    PRIMARY KEY (product_code, policy_version)
);

INSERT INTO risk_policy_versions(product_code, policy_version, status, config, published_at)
VALUES (
    'business_card', 1, 'published',
    '{"required_verification_status":"approved","require_poa":true,"require_questionnaire":false,"denied_residence_countries":["KP","IR"]}',
    NOW()
)
ON CONFLICT (product_code, policy_version) DO NOTHING;

CREATE TABLE IF NOT EXISTS risk_evaluations (
    evaluation_id TEXT PRIMARY KEY,
    customer_id TEXT NOT NULL REFERENCES risk_customers(customer_id),
    product_code TEXT NOT NULL,
    policy_version INTEGER NOT NULL,
    fact_version BIGINT NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('allow', 'review', 'deny')),
    reason_codes JSONB NOT NULL,
    fact_snapshot JSONB NOT NULL,
    command_fingerprint TEXT NOT NULL,
    evaluated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS risk_actions (
    action_id TEXT PRIMARY KEY,
    evaluation_id TEXT NOT NULL REFERENCES risk_evaluations(evaluation_id),
    customer_id TEXT NOT NULL REFERENCES risk_customers(customer_id),
    action_type TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('open', 'completed')),
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS risk_actions_customer_status_idx
    ON risk_actions(customer_id, status);
