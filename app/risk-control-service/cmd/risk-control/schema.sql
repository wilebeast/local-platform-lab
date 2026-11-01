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

CREATE TABLE IF NOT EXISTS risk_activity_events (
    event_id TEXT PRIMARY KEY,
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    event_type TEXT NOT NULL CHECK (event_type IN ('funds_in', 'funds_out')),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS risk_activity_events_window_idx
    ON risk_activity_events(occurred_at, subject_type, subject_id);

CREATE TABLE IF NOT EXISTS risk_offline_policy_versions (
    policy_key TEXT NOT NULL,
    policy_version INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('draft', 'published', 'retired')),
    config JSONB NOT NULL,
    published_at TIMESTAMPTZ,
    PRIMARY KEY (policy_key, policy_version)
);

INSERT INTO risk_offline_policy_versions(policy_key, policy_version, status, config, published_at)
VALUES (
    'rapid_fund_movement', 1, 'published',
    '{"window_hours":24,"min_inflow_minor":50000,"outflow_ratio":0.9,"capability":"withdrawal"}',
    NOW()
)
ON CONFLICT (policy_key, policy_version) DO NOTHING;

CREATE TABLE IF NOT EXISTS risk_decision_policy_versions (
    policy_key TEXT NOT NULL,
    policy_version INTEGER NOT NULL CHECK (policy_version > 0),
    status TEXT NOT NULL CHECK (status IN ('draft', 'published', 'retired')),
    default_decision TEXT NOT NULL CHECK (default_decision IN ('pass', 'block')),
    default_reason_codes JSONB NOT NULL DEFAULT '[]'::JSONB,
    content_fingerprint TEXT NOT NULL DEFAULT '',
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (policy_key, policy_version)
);

ALTER TABLE risk_decision_policy_versions
    ADD COLUMN IF NOT EXISTS content_fingerprint TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS risk_decision_factor_definitions (
    policy_key TEXT NOT NULL,
    policy_version INTEGER NOT NULL,
    factor_key TEXT NOT NULL,
    factor_type TEXT NOT NULL CHECK (factor_type IN ('value', 'expression', 'rpc', 'warehouse_feature')),
    value_type TEXT NOT NULL CHECK (value_type IN ('int', 'double', 'string', 'bool', 'list<string>')),
    param_key TEXT NOT NULL DEFAULT '',
    expression TEXT NOT NULL DEFAULT '',
    resolver_key TEXT NOT NULL DEFAULT '',
    timeout_ms BIGINT NOT NULL DEFAULT 0 CHECK (timeout_ms >= 0),
    PRIMARY KEY (policy_key, policy_version, factor_key),
    FOREIGN KEY (policy_key, policy_version)
        REFERENCES risk_decision_policy_versions(policy_key, policy_version)
);

ALTER TABLE risk_decision_factor_definitions
    DROP CONSTRAINT IF EXISTS risk_decision_factor_definitions_factor_type_check;
ALTER TABLE risk_decision_factor_definitions
    ADD CONSTRAINT risk_decision_factor_definitions_factor_type_check
    CHECK (factor_type IN ('value', 'expression', 'rpc', 'warehouse_feature'));

CREATE TABLE IF NOT EXISTS risk_decision_rules (
    policy_key TEXT NOT NULL,
    policy_version INTEGER NOT NULL,
    rule_key TEXT NOT NULL,
    rule_version INTEGER NOT NULL CHECK (rule_version > 0),
    expression TEXT NOT NULL,
    PRIMARY KEY (policy_key, policy_version, rule_key),
    FOREIGN KEY (policy_key, policy_version)
        REFERENCES risk_decision_policy_versions(policy_key, policy_version)
);

CREATE TABLE IF NOT EXISTS risk_decision_strategies (
    policy_key TEXT NOT NULL,
    policy_version INTEGER NOT NULL,
    strategy_key TEXT NOT NULL,
    rule_key TEXT NOT NULL,
    priority INTEGER NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('pass', 'block')),
    reason_codes JSONB NOT NULL DEFAULT '[]'::JSONB,
    PRIMARY KEY (policy_key, policy_version, strategy_key),
    FOREIGN KEY (policy_key, policy_version)
        REFERENCES risk_decision_policy_versions(policy_key, policy_version),
    FOREIGN KEY (policy_key, policy_version, rule_key)
        REFERENCES risk_decision_rules(policy_key, policy_version, rule_key)
);

CREATE TABLE IF NOT EXISTS risk_decision_strategy_actions (
    policy_key TEXT NOT NULL,
    policy_version INTEGER NOT NULL,
    strategy_key TEXT NOT NULL,
    sequence_no INTEGER NOT NULL CHECK (sequence_no > 0),
    action_code TEXT NOT NULL,
    params JSONB NOT NULL DEFAULT '{}'::JSONB,
    PRIMARY KEY (policy_key, policy_version, strategy_key, sequence_no),
    FOREIGN KEY (policy_key, policy_version, strategy_key)
        REFERENCES risk_decision_strategies(policy_key, policy_version, strategy_key)
);

INSERT INTO risk_decision_policy_versions(
    policy_key, policy_version, status, default_decision, default_reason_codes
)
SELECT 'warehouse_rapid_outflow', 1, 'draft', 'pass', '[]'::JSONB
WHERE NOT EXISTS (
    SELECT 1 FROM risk_decision_policy_versions
    WHERE policy_key='warehouse_rapid_outflow' AND policy_version=1
);

INSERT INTO risk_decision_factor_definitions(
    policy_key, policy_version, factor_key, factor_type, value_type,
    param_key, expression, resolver_key, timeout_ms
)
SELECT * FROM (VALUES
    ('warehouse_rapid_outflow', 1, 'inflow_minor_24h', 'warehouse_feature', 'int', '', '', 'subject_fund_activity_24h/inflow_minor', 0::BIGINT),
    ('warehouse_rapid_outflow', 1, 'outflow_ratio_24h', 'warehouse_feature', 'double', '', '', 'subject_fund_activity_24h/outflow_ratio', 0::BIGINT)
) AS definition(policy_key, policy_version, factor_key, factor_type, value_type, param_key, expression, resolver_key, timeout_ms)
WHERE EXISTS (
    SELECT 1 FROM risk_decision_policy_versions
    WHERE policy_key='warehouse_rapid_outflow' AND policy_version=1 AND status='draft'
)
ON CONFLICT DO NOTHING;

INSERT INTO risk_decision_rules(
    policy_key, policy_version, rule_key, rule_version, expression
)
SELECT 'warehouse_rapid_outflow', 1, 'rapid_outflow', 1,
       'inflow_minor_24h >= 50000 && outflow_ratio_24h >= 0.90'
WHERE EXISTS (
    SELECT 1 FROM risk_decision_policy_versions
    WHERE policy_key='warehouse_rapid_outflow' AND policy_version=1 AND status='draft'
)
ON CONFLICT DO NOTHING;

INSERT INTO risk_decision_strategies(
    policy_key, policy_version, strategy_key, rule_key, priority, decision, reason_codes
)
SELECT 'warehouse_rapid_outflow', 1, 'block_rapid_outflow', 'rapid_outflow', 100,
       'block', '["rapid_outflow"]'::JSONB
WHERE EXISTS (
    SELECT 1 FROM risk_decision_policy_versions
    WHERE policy_key='warehouse_rapid_outflow' AND policy_version=1 AND status='draft'
)
ON CONFLICT DO NOTHING;

INSERT INTO risk_decision_strategy_actions(
    policy_key, policy_version, strategy_key, sequence_no, action_code, params
)
SELECT 'warehouse_rapid_outflow', 1, 'block_rapid_outflow', 1,
       'activate_restriction', '{"capability":"withdrawal"}'::JSONB
WHERE EXISTS (
    SELECT 1 FROM risk_decision_policy_versions
    WHERE policy_key='warehouse_rapid_outflow' AND policy_version=1 AND status='draft'
)
ON CONFLICT DO NOTHING;

UPDATE risk_decision_policy_versions
SET status='published', published_at=NOW()
WHERE policy_key='warehouse_rapid_outflow' AND policy_version=1 AND status='draft';

CREATE OR REPLACE FUNCTION reject_published_policy_definition_change()
RETURNS TRIGGER AS $$
DECLARE
    target_policy_key TEXT;
    target_policy_version INTEGER;
BEGIN
    IF TG_OP = 'INSERT' THEN
        target_policy_key := NEW.policy_key;
        target_policy_version := NEW.policy_version;
    ELSE
        target_policy_key := OLD.policy_key;
        target_policy_version := OLD.policy_version;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM risk_decision_policy_versions
        WHERE policy_key = target_policy_key
          AND policy_version = target_policy_version
          AND status = 'published'
    ) THEN
        RAISE EXCEPTION 'published policy definition is immutable: % version %',
            target_policy_key, target_policy_version
            USING ERRCODE = '55000';
    END IF;

    IF TG_OP = 'UPDATE'
       AND (NEW.policy_key, NEW.policy_version)
           IS DISTINCT FROM (OLD.policy_key, OLD.policy_version)
       AND EXISTS (
           SELECT 1
           FROM risk_decision_policy_versions
           WHERE policy_key = NEW.policy_key
             AND policy_version = NEW.policy_version
             AND status = 'published'
       ) THEN
        RAISE EXCEPTION 'published policy definition is immutable: % version %',
            NEW.policy_key, NEW.policy_version
            USING ERRCODE = '55000';
    END IF;

    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS protect_published_factor_definitions
    ON risk_decision_factor_definitions;
CREATE TRIGGER protect_published_factor_definitions
BEFORE INSERT OR UPDATE OR DELETE ON risk_decision_factor_definitions
FOR EACH ROW EXECUTE FUNCTION reject_published_policy_definition_change();

DROP TRIGGER IF EXISTS protect_published_rules ON risk_decision_rules;
CREATE TRIGGER protect_published_rules
BEFORE INSERT OR UPDATE OR DELETE ON risk_decision_rules
FOR EACH ROW EXECUTE FUNCTION reject_published_policy_definition_change();

DROP TRIGGER IF EXISTS protect_published_strategies ON risk_decision_strategies;
CREATE TRIGGER protect_published_strategies
BEFORE INSERT OR UPDATE OR DELETE ON risk_decision_strategies
FOR EACH ROW EXECUTE FUNCTION reject_published_policy_definition_change();

DROP TRIGGER IF EXISTS protect_published_strategy_actions
    ON risk_decision_strategy_actions;
CREATE TRIGGER protect_published_strategy_actions
BEFORE INSERT OR UPDATE OR DELETE ON risk_decision_strategy_actions
FOR EACH ROW EXECUTE FUNCTION reject_published_policy_definition_change();

CREATE TABLE IF NOT EXISTS risk_realtime_evaluations (
    evaluation_id TEXT PRIMARY KEY,
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    policy_key TEXT NOT NULL,
    policy_version INTEGER NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('pass', 'block')),
    reason_codes JSONB NOT NULL,
    matched_strategy_key TEXT,
    matched_rule_key TEXT,
    factor_snapshot JSONB NOT NULL,
    strategy_results JSONB NOT NULL,
    command_fingerprint TEXT NOT NULL,
    evaluated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS risk_policy_action_jobs (
    action_id TEXT PRIMARY KEY,
    evaluation_id TEXT NOT NULL REFERENCES risk_realtime_evaluations(evaluation_id),
    sequence_no INTEGER NOT NULL CHECK (sequence_no > 0),
    action_code TEXT NOT NULL,
    params JSONB NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'completed', 'retryable', 'dead_letter')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (evaluation_id, sequence_no)
);

CREATE INDEX IF NOT EXISTS risk_policy_action_jobs_ready_idx
    ON risk_policy_action_jobs(next_attempt_at, created_at)
    WHERE status IN ('pending', 'retryable');

CREATE TABLE IF NOT EXISTS risk_offline_runs (
    run_id TEXT PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('completed', 'failed')),
    as_of TIMESTAMPTZ NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    policy_key TEXT NOT NULL,
    policy_version INTEGER NOT NULL,
    subjects_evaluated INTEGER NOT NULL,
    findings_created INTEGER NOT NULL,
    restrictions_activated INTEGER NOT NULL,
    command_fingerprint TEXT NOT NULL,
    result_json JSONB NOT NULL,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS risk_feature_snapshots (
    run_id TEXT NOT NULL REFERENCES risk_offline_runs(run_id),
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    currency TEXT NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    features JSONB NOT NULL,
    PRIMARY KEY (run_id, subject_type, subject_id, currency)
);

CREATE TABLE IF NOT EXISTS risk_warehouse_evaluations (
    evaluation_id TEXT PRIMARY KEY,
    dag_run_id TEXT NOT NULL,
    trigger_type TEXT NOT NULL,
    run_reason TEXT NOT NULL DEFAULT '',
    feature_run_id TEXT NOT NULL,
    feature_set_key TEXT NOT NULL,
    feature_set_version INTEGER NOT NULL CHECK (feature_set_version > 0),
    as_of TIMESTAMPTZ NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    currency TEXT NOT NULL,
    policy_key TEXT NOT NULL,
    policy_version INTEGER NOT NULL CHECK (policy_version > 0),
    decision TEXT NOT NULL CHECK (decision IN ('pass', 'block')),
    matched_strategy_key TEXT,
    matched_rule_key TEXT,
    reason_codes JSONB NOT NULL,
    actions JSONB NOT NULL,
    factors JSONB NOT NULL,
    command_fingerprint TEXT NOT NULL,
    evaluated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (dag_run_id, subject_type, subject_id, currency)
);

CREATE TABLE IF NOT EXISTS risk_warehouse_runs (
    dag_run_id TEXT PRIMARY KEY,
    trigger_type TEXT NOT NULL,
    run_reason TEXT NOT NULL DEFAULT '',
    as_of TIMESTAMPTZ NOT NULL,
    feature_set_key TEXT NOT NULL,
    feature_set_version INTEGER NOT NULL CHECK (feature_set_version > 0),
    policy_key TEXT NOT NULL,
    policy_version INTEGER NOT NULL CHECK (policy_version > 0),
    status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    attempt_count INTEGER NOT NULL DEFAULT 1 CHECK (attempt_count > 0),
    snapshot_count INTEGER NOT NULL DEFAULT 0 CHECK (snapshot_count >= 0),
    decision_count INTEGER NOT NULL DEFAULT 0 CHECK (decision_count >= 0),
    failure_count INTEGER NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    command_fingerprint TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS risk_warehouse_backfills (
    backfill_id TEXT PRIMARY KEY,
    start_as_of TIMESTAMPTZ NOT NULL,
    end_as_of TIMESTAMPTZ NOT NULL,
    step_seconds BIGINT NOT NULL CHECK (step_seconds > 0),
    status TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    attempt_count INTEGER NOT NULL DEFAULT 1 CHECK (attempt_count > 0),
    total_count INTEGER NOT NULL CHECK (total_count > 0),
    succeeded_count INTEGER NOT NULL DEFAULT 0 CHECK (succeeded_count >= 0),
    failed_count INTEGER NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    pending_count INTEGER NOT NULL DEFAULT 0 CHECK (pending_count >= 0),
    command_fingerprint TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    CHECK (end_as_of >= start_as_of),
    CHECK (succeeded_count + failed_count + pending_count = total_count)
);

CREATE TABLE IF NOT EXISTS risk_warehouse_backfill_points (
    backfill_id TEXT NOT NULL REFERENCES risk_warehouse_backfills(backfill_id),
    as_of TIMESTAMPTZ NOT NULL,
    child_run_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_error TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (backfill_id, as_of)
);

ALTER TABLE risk_warehouse_evaluations
    DROP CONSTRAINT IF EXISTS risk_warehouse_evaluations_run_fk;
ALTER TABLE risk_warehouse_evaluations
    ADD CONSTRAINT risk_warehouse_evaluations_run_fk
    FOREIGN KEY (dag_run_id) REFERENCES risk_warehouse_runs(dag_run_id);

CREATE TABLE IF NOT EXISTS risk_warehouse_lineage_audits (
    audit_id BIGSERIAL PRIMARY KEY,
    -- Deliberately not a foreign key: an audit must be able to preserve
    -- evidence that the referenced warehouse run itself is missing.
    dag_run_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('healthy', 'broken')),
    feature_run_id TEXT,
    feature_run_ids JSONB NOT NULL DEFAULT '[]'::JSONB,
    evaluation_count INTEGER NOT NULL DEFAULT 0 CHECK (evaluation_count >= 0),
    issues JSONB NOT NULL,
    audited_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS risk_warehouse_feature_run_fingerprints (
    source_run_id TEXT PRIMARY KEY,
    content_fingerprint TEXT NOT NULL CHECK (length(content_fingerprint) = 64),
    registered_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE risk_warehouse_lineage_audits
    ADD COLUMN IF NOT EXISTS feature_run_ids JSONB NOT NULL DEFAULT '[]'::JSONB;
ALTER TABLE risk_warehouse_lineage_audits
    ADD COLUMN IF NOT EXISTS evaluation_count INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS risk_offline_evaluations (
    run_id TEXT NOT NULL REFERENCES risk_offline_runs(run_id),
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    currency TEXT NOT NULL,
    policy_key TEXT NOT NULL,
    policy_version INTEGER NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('pass', 'block')),
    matched_strategy_key TEXT,
    matched_rule_key TEXT,
    reason_codes JSONB NOT NULL,
    features JSONB NOT NULL,
    evaluated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (run_id, subject_type, subject_id, currency)
);

ALTER TABLE risk_offline_evaluations
    ADD COLUMN IF NOT EXISTS matched_strategy_key TEXT;

ALTER TABLE risk_offline_evaluations
    ADD COLUMN IF NOT EXISTS matched_rule_key TEXT;

ALTER TABLE risk_offline_evaluations
    DROP CONSTRAINT IF EXISTS risk_offline_evaluations_decision_check;

UPDATE risk_offline_evaluations
SET decision = CASE decision
    WHEN 'clear' THEN 'pass'
    WHEN 'restrict' THEN 'block'
    ELSE decision
END
WHERE decision IN ('clear', 'restrict');

ALTER TABLE risk_offline_evaluations
    ADD CONSTRAINT risk_offline_evaluations_decision_check
    CHECK (decision IN ('pass', 'block'));

CREATE TABLE IF NOT EXISTS risk_findings (
    finding_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES risk_offline_runs(run_id),
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    risk_type TEXT NOT NULL,
    severity TEXT NOT NULL CHECK (severity IN ('low', 'medium', 'high')),
    policy_key TEXT NOT NULL,
    policy_version INTEGER NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('open', 'resolved')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS risk_restrictions (
    restriction_id TEXT PRIMARY KEY,
    finding_id TEXT NOT NULL UNIQUE REFERENCES risk_findings(finding_id),
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    capability TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'lifted')),
    activated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lifted_at TIMESTAMPTZ
);

ALTER TABLE risk_restrictions
    ALTER COLUMN finding_id DROP NOT NULL;

CREATE INDEX IF NOT EXISTS risk_restrictions_active_idx
    ON risk_restrictions(subject_type, subject_id, capability)
    WHERE status = 'active';

WITH ranked_active_restrictions AS (
    SELECT
        restriction_id,
        row_number() OVER (
            PARTITION BY subject_type, subject_id, capability
            ORDER BY activated_at DESC, restriction_id DESC
        ) AS row_number
    FROM risk_restrictions
    WHERE status = 'active'
)
UPDATE risk_restrictions AS restriction
SET status = 'lifted',
    lifted_at = COALESCE(restriction.lifted_at, NOW())
FROM ranked_active_restrictions AS ranked
WHERE restriction.restriction_id = ranked.restriction_id
  AND ranked.row_number > 1;

CREATE UNIQUE INDEX IF NOT EXISTS risk_restrictions_one_active_capability_idx
    ON risk_restrictions(subject_type, subject_id, capability)
    WHERE status = 'active';
