CREATE SCHEMA IF NOT EXISTS risk_control;

CREATE OR REPLACE FUNCTION risk_control.reject_change()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION '% is immutable after insertion', TG_TABLE_NAME
        USING ERRCODE = '55000';
END;
$$;

CREATE OR REPLACE FUNCTION risk_control.seal_evaluation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'evaluations are immutable'
            USING ERRCODE = '55000';
    END IF;

    IF OLD.sealed THEN
        RAISE EXCEPTION 'sealed evaluation % is immutable', OLD.evaluation_id
            USING ERRCODE = '55000';
    END IF;

    IF NEW.sealed
       AND (to_jsonb(NEW) - 'sealed') = (to_jsonb(OLD) - 'sealed') THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'evaluation % only permits the transition sealed=false to sealed=true', OLD.evaluation_id
        USING ERRCODE = '55000';
END;
$$;

CREATE OR REPLACE FUNCTION risk_control.require_open_evaluation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    evaluation_is_sealed BOOLEAN;
BEGIN
    SELECT sealed
      INTO evaluation_is_sealed
      FROM risk_control.evaluations
     WHERE evaluation_id = NEW.evaluation_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'evaluation % does not exist', NEW.evaluation_id
            USING ERRCODE = '23503';
    END IF;

    IF evaluation_is_sealed THEN
        RAISE EXCEPTION 'evaluation % is sealed; no evidence may be appended', NEW.evaluation_id
            USING ERRCODE = '55000';
    END IF;

    RETURN NEW;
END;
$$;

CREATE TABLE IF NOT EXISTS risk_control.policy_activations (
    activation_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(activation_id) > 0),
    environment TEXT NOT NULL DEFAULT '' CHECK (environment IN ('local', 'development', 'staging', 'production')),
    scene_key TEXT NOT NULL DEFAULT '' CHECK (length(scene_key) > 0),
    rollout_cohort TEXT NOT NULL DEFAULT 'default' CHECK (length(rollout_cohort) > 0),
    policy_key TEXT NOT NULL DEFAULT '' CHECK (length(policy_key) > 0),
    policy_version INTEGER NOT NULL DEFAULT 0 CHECK (policy_version > 0),
    snapshot_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(snapshot_fingerprint) = 64),
    generation BIGINT NOT NULL DEFAULT 0 CHECK (generation > 0),
    previous_generation BIGINT NOT NULL DEFAULT 0 CHECK (previous_generation >= 0 AND previous_generation < generation),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'retired')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    approved_by TEXT NOT NULL DEFAULT '' CHECK (length(approved_by) > 0),
    approval_ref TEXT NOT NULL DEFAULT '' CHECK (length(approval_ref) > 0),
    activated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (environment, scene_key, rollout_cohort, generation)
);

CREATE UNIQUE INDEX IF NOT EXISTS risk_control_policy_activations_one_active_idx
    ON risk_control.policy_activations(environment, scene_key, rollout_cohort)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS risk_control_policy_activations_snapshot_idx
    ON risk_control.policy_activations(snapshot_fingerprint, status);

CREATE TABLE IF NOT EXISTS risk_control.evaluations (
    evaluation_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(evaluation_id) > 0),
    environment TEXT NOT NULL DEFAULT '' CHECK (environment IN ('local', 'development', 'staging', 'production')),
    scene_key TEXT NOT NULL DEFAULT '' CHECK (length(scene_key) > 0),
    request_id TEXT NOT NULL DEFAULT '' CHECK (length(request_id) > 0),
    request_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(request_fingerprint) = 64),
    subject_type TEXT NOT NULL DEFAULT '' CHECK (length(subject_type) > 0),
    subject_id TEXT NOT NULL DEFAULT '' CHECK (length(subject_id) > 0),
    capability TEXT NOT NULL DEFAULT '' CHECK (length(capability) > 0),
    business_object_type TEXT NOT NULL DEFAULT '' CHECK (length(business_object_type) > 0),
    business_object_id TEXT NOT NULL DEFAULT '' CHECK (length(business_object_id) > 0),
    business_object_version BIGINT NOT NULL DEFAULT 0 CHECK (business_object_version >= 0),
    execution_mode TEXT NOT NULL DEFAULT '' CHECK (
        execution_mode IN ('realtime', 'nearline', 'simulation', 'shadow', 'backfill', 'live_detection', 'remediation_campaign')
    ),
    evaluation_status TEXT NOT NULL DEFAULT '' CHECK (evaluation_status IN ('completed', 'degraded', 'failed')),
    decision TEXT NOT NULL DEFAULT '' CHECK (decision IN ('', 'pass', 'block')),
    disposition TEXT NOT NULL DEFAULT '' CHECK (disposition IN ('', 'reject', 'step_up', 'manual_review', 'defer')),
    decision_source TEXT NOT NULL DEFAULT '' CHECK (decision_source IN ('', 'live', 'last_known_good', 'manual_override')),
    risk_level TEXT NOT NULL DEFAULT '' CHECK (risk_level IN ('', 'no_risk', 'low', 'medium', 'high', 'critical')),
    machine_label TEXT NOT NULL DEFAULT '',
    reason_codes JSONB NOT NULL DEFAULT '[]'::JSONB CHECK (jsonb_typeof(reason_codes) = 'array'),
    obligations JSONB NOT NULL DEFAULT '[]'::JSONB CHECK (jsonb_typeof(obligations) = 'array'),
    degraded_factors JSONB NOT NULL DEFAULT '[]'::JSONB CHECK (jsonb_typeof(degraded_factors) = 'array'),
    failure_code TEXT NOT NULL DEFAULT '',
    retryable BOOLEAN NOT NULL DEFAULT FALSE,
    policy_key TEXT NOT NULL DEFAULT '' CHECK (length(policy_key) > 0),
    policy_version INTEGER NOT NULL DEFAULT 0 CHECK (policy_version > 0),
    snapshot_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(snapshot_fingerprint) = 64),
    activation_generation BIGINT NOT NULL DEFAULT 0 CHECK (activation_generation > 0),
    trusted_context_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(trusted_context_fingerprint) IN (0, 64)),
    input_snapshot_ref TEXT NOT NULL DEFAULT '',
    data_classification TEXT NOT NULL DEFAULT 'confidential' CHECK (
        data_classification IN ('public', 'internal', 'confidential', 'restricted')
    ),
    event_time TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    observed_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00' CHECK (observed_at > '1970-01-01 00:00:00+00'),
    input_cutoff_time TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00' CHECK (input_cutoff_time > '1970-01-01 00:00:00+00'),
    decision_time TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00' CHECK (decision_time > '1970-01-01 00:00:00+00'),
    decision_valid_until TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    resolution_condition JSONB NOT NULL DEFAULT '{}'::JSONB CHECK (jsonb_typeof(resolution_condition) = 'object'),
    recheck_triggers JSONB NOT NULL DEFAULT '[]'::JSONB CHECK (jsonb_typeof(recheck_triggers) = 'array'),
    source_run_id TEXT NOT NULL DEFAULT '',
    correlation_id TEXT NOT NULL DEFAULT '',
    causation_id TEXT NOT NULL DEFAULT '',
    trace_id TEXT NOT NULL DEFAULT '',
    sealed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (environment, scene_key, request_id),
    CHECK (
        (evaluation_status = 'failed' AND decision = '') OR
        (evaluation_status = 'completed' AND decision IN ('pass', 'block')) OR
        (evaluation_status = 'degraded' AND decision IN ('', 'pass', 'block'))
    ),
    CHECK ((decision = '' AND decision_source = '') OR (decision <> '' AND decision_source <> '')),
    CHECK (decision_valid_until = '1970-01-01 00:00:00+00' OR decision_valid_until >= decision_time)
);

CREATE INDEX IF NOT EXISTS risk_control_evaluations_subject_time_idx
    ON risk_control.evaluations(subject_type, subject_id, decision_time DESC);

CREATE INDEX IF NOT EXISTS risk_control_evaluations_business_object_idx
    ON risk_control.evaluations(business_object_type, business_object_id, business_object_version DESC);

CREATE INDEX IF NOT EXISTS risk_control_evaluations_policy_idx
    ON risk_control.evaluations(policy_key, policy_version, decision_time DESC);

DROP TRIGGER IF EXISTS risk_control_seal_evaluation ON risk_control.evaluations;
CREATE TRIGGER risk_control_seal_evaluation
BEFORE UPDATE OR DELETE ON risk_control.evaluations
FOR EACH ROW EXECUTE FUNCTION risk_control.seal_evaluation();

CREATE TABLE IF NOT EXISTS risk_control.evaluation_factor_values (
    evaluation_id TEXT NOT NULL DEFAULT '' REFERENCES risk_control.evaluations(evaluation_id),
    factor_key TEXT NOT NULL DEFAULT '' CHECK (length(factor_key) > 0),
    factor_version INTEGER NOT NULL DEFAULT 0 CHECK (factor_version > 0),
    factor_type TEXT NOT NULL DEFAULT '' CHECK (factor_type IN ('context', 'state', 'feature', 'rpc', 'query', 'expression', 'model')),
    value_type TEXT NOT NULL DEFAULT '' CHECK (value_type IN ('bool', 'int', 'double', 'string', 'json')),
    value_bool BOOLEAN NOT NULL DEFAULT FALSE,
    value_int BIGINT NOT NULL DEFAULT 0,
    value_double DOUBLE PRECISION NOT NULL DEFAULT 0,
    value_text TEXT NOT NULL DEFAULT '',
    value_json JSONB NOT NULL DEFAULT '{}'::JSONB,
    source_type TEXT NOT NULL DEFAULT '' CHECK (length(source_type) > 0),
    source_ref TEXT NOT NULL DEFAULT '',
    source_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(source_fingerprint) IN (0, 64)),
    valid_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    observed_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00' CHECK (observed_at > '1970-01-01 00:00:00+00'),
    freshness_ms BIGINT NOT NULL DEFAULT 0 CHECK (freshness_ms >= 0),
    resolve_duration_ms BIGINT NOT NULL DEFAULT 0 CHECK (resolve_duration_ms >= 0),
    result_status TEXT NOT NULL DEFAULT '' CHECK (result_status IN ('resolved', 'missing', 'stale', 'timeout', 'error')),
    error_code TEXT NOT NULL DEFAULT '',
    data_classification TEXT NOT NULL DEFAULT 'confidential' CHECK (
        data_classification IN ('public', 'internal', 'confidential', 'restricted')
    ),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (evaluation_id, factor_key, factor_version)
);

ALTER TABLE risk_control.evaluation_factor_values
    DROP CONSTRAINT IF EXISTS risk_control_factor_value_type_matches_value;
ALTER TABLE risk_control.evaluation_factor_values
    ADD CONSTRAINT risk_control_factor_value_type_matches_value CHECK (
        (value_type = 'bool' AND value_int = 0 AND value_double = 0 AND value_text = '' AND value_json = '{}'::JSONB) OR
        (value_type = 'int' AND value_bool = FALSE AND value_double = 0 AND value_text = '' AND value_json = '{}'::JSONB) OR
        (value_type = 'double' AND value_bool = FALSE AND value_int = 0 AND value_text = '' AND value_json = '{}'::JSONB) OR
        (value_type = 'string' AND value_bool = FALSE AND value_int = 0 AND value_double = 0 AND value_json = '{}'::JSONB) OR
        (value_type = 'json' AND value_bool = FALSE AND value_int = 0 AND value_double = 0 AND value_text = '')
    );

CREATE INDEX IF NOT EXISTS risk_control_factor_values_source_idx
    ON risk_control.evaluation_factor_values(source_type, source_ref);

CREATE TABLE IF NOT EXISTS risk_control.evaluation_rule_hits (
    evaluation_id TEXT NOT NULL DEFAULT '' REFERENCES risk_control.evaluations(evaluation_id),
    rule_key TEXT NOT NULL DEFAULT '' CHECK (length(rule_key) > 0),
    rule_version INTEGER NOT NULL DEFAULT 0 CHECK (rule_version > 0),
    expression_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(expression_fingerprint) = 64),
    matched BOOLEAN NOT NULL DEFAULT FALSE,
    evaluation_status TEXT NOT NULL DEFAULT '' CHECK (evaluation_status IN ('matched', 'not_matched', 'skipped', 'error')),
    duration_us BIGINT NOT NULL DEFAULT 0 CHECK (duration_us >= 0),
    trace_ref TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (evaluation_id, rule_key, rule_version)
);

CREATE INDEX IF NOT EXISTS risk_control_rule_hits_matched_idx
    ON risk_control.evaluation_rule_hits(rule_key, rule_version, matched)
    WHERE matched;

CREATE TABLE IF NOT EXISTS risk_control.evaluation_business_dispositions (
    evaluation_id TEXT NOT NULL DEFAULT '' REFERENCES risk_control.evaluations(evaluation_id),
    disposition_type TEXT NOT NULL DEFAULT '' CHECK (length(disposition_type) > 0),
    schema_version INTEGER NOT NULL DEFAULT 0 CHECK (schema_version > 0),
    owner_module TEXT NOT NULL DEFAULT '' CHECK (length(owner_module) > 0),
    value_type TEXT NOT NULL DEFAULT '' CHECK (value_type IN ('bool', 'int', 'double', 'string', 'json')),
    value_bool BOOLEAN NOT NULL DEFAULT FALSE,
    value_int BIGINT NOT NULL DEFAULT 0,
    value_double DOUBLE PRECISION NOT NULL DEFAULT 0,
    value_text TEXT NOT NULL DEFAULT '',
    value_json JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (evaluation_id, disposition_type)
);

ALTER TABLE risk_control.evaluation_business_dispositions
    DROP CONSTRAINT IF EXISTS risk_control_disposition_value_type_matches_value;
ALTER TABLE risk_control.evaluation_business_dispositions
    ADD CONSTRAINT risk_control_disposition_value_type_matches_value CHECK (
        (value_type = 'bool' AND value_int = 0 AND value_double = 0 AND value_text = '' AND value_json = '{}'::JSONB) OR
        (value_type = 'int' AND value_bool = FALSE AND value_double = 0 AND value_text = '' AND value_json = '{}'::JSONB) OR
        (value_type = 'double' AND value_bool = FALSE AND value_int = 0 AND value_text = '' AND value_json = '{}'::JSONB) OR
        (value_type = 'string' AND value_bool = FALSE AND value_int = 0 AND value_double = 0 AND value_json = '{}'::JSONB) OR
        (value_type = 'json' AND value_bool = FALSE AND value_int = 0 AND value_double = 0 AND value_text = '')
    );

CREATE TABLE IF NOT EXISTS risk_control.action_intents (
    action_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(action_id) > 0),
    evaluation_id TEXT NOT NULL DEFAULT '' REFERENCES risk_control.evaluations(evaluation_id),
    action_type TEXT NOT NULL DEFAULT '' CHECK (length(action_type) > 0),
    effect_idempotency_key TEXT NOT NULL DEFAULT '' CHECK (length(effect_idempotency_key) > 0),
    target_type TEXT NOT NULL DEFAULT '' CHECK (length(target_type) > 0),
    target_id TEXT NOT NULL DEFAULT '' CHECK (length(target_id) > 0),
    target_object_version BIGINT NOT NULL DEFAULT 0 CHECK (target_object_version >= 0),
    expected_restriction_generation BIGINT NOT NULL DEFAULT 0 CHECK (expected_restriction_generation >= 0),
    params JSONB NOT NULL DEFAULT '{}'::JSONB CHECK (jsonb_typeof(params) = 'object'),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (
        state IN ('pending', 'running', 'retryable_failed', 'unknown_result', 'reconciling', 'succeeded', 'terminal_failed', 'cancelled', 'compensated')
    ),
    attempt_no INTEGER NOT NULL DEFAULT 0 CHECK (attempt_no >= 0),
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_epoch BIGINT NOT NULL DEFAULT 0 CHECK (lease_epoch >= 0),
    lease_until TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    next_retry_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    receipt_ref TEXT NOT NULL DEFAULT '',
    receipt_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(receipt_fingerprint) IN (0, 64)),
    last_error_code TEXT NOT NULL DEFAULT '',
    last_error_message TEXT NOT NULL DEFAULT '',
    superseded_by_action_id TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    UNIQUE (action_type, effect_idempotency_key)
);

CREATE INDEX IF NOT EXISTS risk_control_action_intents_ready_idx
    ON risk_control.action_intents(state, next_retry_at, created_at)
    WHERE state IN ('pending', 'retryable_failed', 'reconciling');

CREATE INDEX IF NOT EXISTS risk_control_action_intents_expired_lease_idx
    ON risk_control.action_intents(lease_until, action_id)
    WHERE state = 'running';

CREATE INDEX IF NOT EXISTS risk_control_action_intents_target_idx
    ON risk_control.action_intents(target_type, target_id, created_at DESC);

CREATE TABLE IF NOT EXISTS risk_control.action_attempts (
    attempt_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(attempt_id) > 0),
    action_id TEXT NOT NULL DEFAULT '' REFERENCES risk_control.action_intents(action_id),
    attempt_no INTEGER NOT NULL DEFAULT 0 CHECK (attempt_no > 0),
    lease_epoch BIGINT NOT NULL DEFAULT 0 CHECK (lease_epoch > 0),
    attempt_type TEXT NOT NULL DEFAULT '' CHECK (attempt_type IN ('execute', 'query', 'compensate')),
    request_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(request_fingerprint) = 64),
    result_type TEXT NOT NULL DEFAULT '' CHECK (result_type IN ('succeeded', 'retryable_failed', 'unknown_result', 'terminal_failed')),
    retryable BOOLEAN NOT NULL DEFAULT FALSE,
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    receipt_ref TEXT NOT NULL DEFAULT '',
    receipt_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(receipt_fingerprint) IN (0, 64)),
    started_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00' CHECK (started_at > '1970-01-01 00:00:00+00'),
    completed_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00' CHECK (completed_at >= started_at),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (action_id, attempt_no)
);

CREATE TABLE IF NOT EXISTS risk_control.outbox_events (
    event_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(event_id) > 0),
    evaluation_id TEXT NOT NULL DEFAULT '' REFERENCES risk_control.evaluations(evaluation_id),
    aggregate_type TEXT NOT NULL DEFAULT '' CHECK (length(aggregate_type) > 0),
    aggregate_id TEXT NOT NULL DEFAULT '' CHECK (length(aggregate_id) > 0),
    event_type TEXT NOT NULL DEFAULT '' CHECK (length(event_type) > 0),
    event_version INTEGER NOT NULL DEFAULT 0 CHECK (event_version > 0),
    schema_id TEXT NOT NULL DEFAULT '' CHECK (length(schema_id) > 0),
    partition_key TEXT NOT NULL DEFAULT '' CHECK (length(partition_key) > 0),
    correlation_id TEXT NOT NULL DEFAULT '',
    causation_id TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}'::JSONB CHECK (jsonb_typeof(payload) = 'object'),
    payload_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(payload_fingerprint) = 64),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'publishing', 'published', 'retryable_failed', 'terminal_failed')),
    attempt_no INTEGER NOT NULL DEFAULT 0 CHECK (attempt_no >= 0),
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_epoch BIGINT NOT NULL DEFAULT 0 CHECK (lease_epoch >= 0),
    lease_until TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    next_retry_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    last_error TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00' CHECK (occurred_at > '1970-01-01 00:00:00+00'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS risk_control_outbox_one_action_request_idx
    ON risk_control.outbox_events(aggregate_id, event_type)
    WHERE aggregate_type = 'action' AND event_type = 'risk.action.requested';

CREATE INDEX IF NOT EXISTS risk_control_outbox_ready_idx
    ON risk_control.outbox_events(state, next_retry_at, created_at)
    WHERE state IN ('pending', 'retryable_failed');

CREATE INDEX IF NOT EXISTS risk_control_outbox_expired_lease_idx
    ON risk_control.outbox_events(lease_until, event_id)
    WHERE state = 'publishing';

CREATE TABLE IF NOT EXISTS risk_control.findings (
    finding_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(finding_id) > 0),
    evaluation_id TEXT NOT NULL DEFAULT '' REFERENCES risk_control.evaluations(evaluation_id),
    subject_type TEXT NOT NULL DEFAULT '' CHECK (length(subject_type) > 0),
    subject_id TEXT NOT NULL DEFAULT '' CHECK (length(subject_id) > 0),
    finding_type TEXT NOT NULL DEFAULT '' CHECK (length(finding_type) > 0),
    severity TEXT NOT NULL DEFAULT '' CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    finding_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(finding_fingerprint) = 64),
    state TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'resolved', 'superseded')),
    resolution_code TEXT NOT NULL DEFAULT '',
    superseded_by_finding_id TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    observed_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00' CHECK (observed_at > '1970-01-01 00:00:00+00'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (evaluation_id, finding_fingerprint)
);

CREATE INDEX IF NOT EXISTS risk_control_findings_subject_idx
    ON risk_control.findings(subject_type, subject_id, state, observed_at DESC);

CREATE TABLE IF NOT EXISTS risk_control.restrictions (
    restriction_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(restriction_id) > 0),
    subject_type TEXT NOT NULL DEFAULT '' CHECK (length(subject_type) > 0),
    subject_id TEXT NOT NULL DEFAULT '' CHECK (length(subject_id) > 0),
    capability TEXT NOT NULL DEFAULT '' CHECK (length(capability) > 0),
    scope_key TEXT NOT NULL DEFAULT 'global' CHECK (length(scope_key) > 0),
    generation BIGINT NOT NULL DEFAULT 0 CHECK (generation > 0),
    state TEXT NOT NULL DEFAULT 'inactive' CHECK (state IN ('inactive', 'active', 'expired', 'released', 'superseded')),
    source_type TEXT NOT NULL DEFAULT '' CHECK (source_type IN ('evaluation', 'review', 'manual_migration')),
    source_id TEXT NOT NULL DEFAULT '' CHECK (length(source_id) > 0),
    activation_action_id TEXT NOT NULL DEFAULT '',
    release_action_id TEXT NOT NULL DEFAULT '',
    reason_codes JSONB NOT NULL DEFAULT '[]'::JSONB CHECK (jsonb_typeof(reason_codes) = 'array'),
    valid_from TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    valid_until TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    released_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    superseded_by_restriction_id TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (subject_type, subject_id, capability, scope_key, generation),
    CHECK (valid_until = '1970-01-01 00:00:00+00' OR valid_until >= valid_from)
);

CREATE UNIQUE INDEX IF NOT EXISTS risk_control_restrictions_one_active_scope_idx
    ON risk_control.restrictions(subject_type, subject_id, capability, scope_key)
    WHERE state = 'active';

CREATE INDEX IF NOT EXISTS risk_control_restrictions_subject_idx
    ON risk_control.restrictions(subject_type, subject_id, capability, generation DESC);

CREATE TABLE IF NOT EXISTS risk_control.list_entries (
    list_entry_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(list_entry_id) > 0),
    list_key TEXT NOT NULL DEFAULT '' CHECK (length(list_key) > 0),
    subject_type TEXT NOT NULL DEFAULT '' CHECK (length(subject_type) > 0),
    subject_id TEXT NOT NULL DEFAULT '' CHECK (length(subject_id) > 0),
    scope_key TEXT NOT NULL DEFAULT 'global' CHECK (length(scope_key) > 0),
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'expired', 'removed', 'superseded')),
    source_type TEXT NOT NULL DEFAULT '' CHECK (source_type IN ('evaluation', 'review', 'external', 'manual_migration')),
    source_id TEXT NOT NULL DEFAULT '' CHECK (length(source_id) > 0),
    reason_codes JSONB NOT NULL DEFAULT '[]'::JSONB CHECK (jsonb_typeof(reason_codes) = 'array'),
    valid_from TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    valid_until TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    approved_by TEXT NOT NULL DEFAULT '',
    approval_ref TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (valid_until = '1970-01-01 00:00:00+00' OR valid_until >= valid_from)
);

CREATE UNIQUE INDEX IF NOT EXISTS risk_control_list_entries_one_active_idx
    ON risk_control.list_entries(list_key, subject_type, subject_id, scope_key)
    WHERE state = 'active';

CREATE TABLE IF NOT EXISTS risk_control.cases (
    case_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(case_id) > 0),
    case_type TEXT NOT NULL DEFAULT '' CHECK (length(case_type) > 0),
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'assigned', 'investigating', 'waiting_evidence', 'escalated', 'decided', 'closed')),
    severity TEXT NOT NULL DEFAULT '' CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    queue_key TEXT NOT NULL DEFAULT 'default' CHECK (length(queue_key) > 0),
    assignee_type TEXT NOT NULL DEFAULT '',
    assignee_id TEXT NOT NULL DEFAULT '',
    case_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(case_fingerprint) = 64),
    sla_due_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    sla_paused_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    sla_paused_seconds BIGINT NOT NULL DEFAULT 0 CHECK (sla_paused_seconds >= 0),
    last_activity_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (case_type, case_fingerprint)
);

CREATE INDEX IF NOT EXISTS risk_control_cases_queue_sla_idx
    ON risk_control.cases(queue_key, status, sla_due_at)
    WHERE status NOT IN ('closed');

CREATE TABLE IF NOT EXISTS risk_control.case_object_links (
    case_id TEXT NOT NULL DEFAULT '' REFERENCES risk_control.cases(case_id),
    object_type TEXT NOT NULL DEFAULT '' CHECK (length(object_type) > 0),
    object_id TEXT NOT NULL DEFAULT '' CHECK (length(object_id) > 0),
    object_version BIGINT NOT NULL DEFAULT 0 CHECK (object_version >= 0),
    relation_type TEXT NOT NULL DEFAULT '' CHECK (length(relation_type) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (case_id, object_type, object_id, relation_type)
);

CREATE INDEX IF NOT EXISTS risk_control_case_object_reverse_idx
    ON risk_control.case_object_links(object_type, object_id, case_id);

CREATE TABLE IF NOT EXISTS risk_control.case_evidence (
    evidence_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(evidence_id) > 0),
    case_id TEXT NOT NULL DEFAULT '' REFERENCES risk_control.cases(case_id),
    evidence_type TEXT NOT NULL DEFAULT '' CHECK (length(evidence_type) > 0),
    object_ref TEXT NOT NULL DEFAULT '' CHECK (length(object_ref) > 0),
    content_sha256 TEXT NOT NULL DEFAULT '' CHECK (length(content_sha256) = 64),
    data_classification TEXT NOT NULL DEFAULT 'confidential' CHECK (
        data_classification IN ('public', 'internal', 'confidential', 'restricted')
    ),
    created_by_type TEXT NOT NULL DEFAULT '' CHECK (length(created_by_type) > 0),
    created_by_id TEXT NOT NULL DEFAULT '' CHECK (length(created_by_id) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS risk_control.case_reviews (
    review_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(review_id) > 0),
    case_id TEXT NOT NULL DEFAULT '' REFERENCES risk_control.cases(case_id),
    review_version BIGINT NOT NULL DEFAULT 0 CHECK (review_version > 0),
    decision TEXT NOT NULL DEFAULT '' CHECK (decision IN ('approved', 'rejected', 'needs_more_evidence', 'escalated')),
    reason_codes JSONB NOT NULL DEFAULT '[]'::JSONB CHECK (jsonb_typeof(reason_codes) = 'array'),
    internal_reason TEXT NOT NULL DEFAULT '',
    customer_visible_reason_code TEXT NOT NULL DEFAULT '',
    reviewer_id TEXT NOT NULL DEFAULT '' CHECK (length(reviewer_id) > 0),
    checker_id TEXT NOT NULL DEFAULT '',
    reviewed_object_versions JSONB NOT NULL DEFAULT '{}'::JSONB CHECK (jsonb_typeof(reviewed_object_versions) = 'object'),
    decision_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(decision_fingerprint) = 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (case_id, review_version)
);

CREATE TABLE IF NOT EXISTS risk_control.feature_runs (
    run_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(run_id) > 0),
    feature_set_key TEXT NOT NULL DEFAULT '' CHECK (length(feature_set_key) > 0),
    feature_set_version INTEGER NOT NULL DEFAULT 0 CHECK (feature_set_version > 0),
    execution_mode TEXT NOT NULL DEFAULT '' CHECK (execution_mode IN ('simulation', 'shadow', 'backfill', 'live_detection', 'remediation_campaign')),
    trigger_type TEXT NOT NULL DEFAULT '' CHECK (trigger_type IN ('scheduled', 'manual', 'backfill', 'recovery')),
    as_of TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00' CHECK (as_of > '1970-01-01 00:00:00+00'),
    input_cutoff_time TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00' CHECK (input_cutoff_time > '1970-01-01 00:00:00+00'),
    input_partitions JSONB NOT NULL DEFAULT '[]'::JSONB CHECK (jsonb_typeof(input_partitions) = 'array'),
    status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'succeeded', 'failed', 'cancelled')),
    snapshot_count BIGINT NOT NULL DEFAULT 0 CHECK (snapshot_count >= 0),
    evaluation_count BIGINT NOT NULL DEFAULT 0 CHECK (evaluation_count >= 0),
    finding_count BIGINT NOT NULL DEFAULT 0 CHECK (finding_count >= 0),
    failure_count BIGINT NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    content_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(content_fingerprint) IN (0, 64)),
    command_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(command_fingerprint) = 64),
    requested_by TEXT NOT NULL DEFAULT '' CHECK (length(requested_by) > 0),
    approval_ref TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS risk_control_feature_runs_lookup_idx
    ON risk_control.feature_runs(feature_set_key, feature_set_version, as_of DESC);

CREATE TABLE IF NOT EXISTS risk_control.feature_run_fingerprints (
    source_run_id TEXT PRIMARY KEY DEFAULT '' REFERENCES risk_control.feature_runs(run_id),
    content_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(content_fingerprint) = 64),
    registered_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS risk_control.feature_quality_results (
    quality_result_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(quality_result_id) > 0),
    run_id TEXT NOT NULL DEFAULT '' REFERENCES risk_control.feature_runs(run_id),
    quality_rule_key TEXT NOT NULL DEFAULT '' CHECK (length(quality_rule_key) > 0),
    quality_rule_version INTEGER NOT NULL DEFAULT 0 CHECK (quality_rule_version > 0),
    required BOOLEAN NOT NULL DEFAULT TRUE,
    status TEXT NOT NULL DEFAULT '' CHECK (status IN ('passed', 'failed', 'not_comparable')),
    observed_value JSONB NOT NULL DEFAULT '{}'::JSONB,
    threshold_value JSONB NOT NULL DEFAULT '{}'::JSONB,
    reason_code TEXT NOT NULL DEFAULT '',
    sample_ref TEXT NOT NULL DEFAULT '',
    checked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id, quality_rule_key, quality_rule_version)
);

CREATE TABLE IF NOT EXISTS risk_control.feature_reconciliations (
    reconciliation_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(reconciliation_id) > 0),
    feature_set_key TEXT NOT NULL DEFAULT '' CHECK (length(feature_set_key) > 0),
    feature_set_version INTEGER NOT NULL DEFAULT 0 CHECK (feature_set_version > 0),
    subject_type TEXT NOT NULL DEFAULT '' CHECK (length(subject_type) > 0),
    subject_id TEXT NOT NULL DEFAULT '' CHECK (length(subject_id) > 0),
    as_of TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00' CHECK (as_of > '1970-01-01 00:00:00+00'),
    stream_run_id TEXT NOT NULL DEFAULT '' CHECK (length(stream_run_id) > 0),
    batch_run_id TEXT NOT NULL DEFAULT '' CHECK (length(batch_run_id) > 0),
    stream_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(stream_fingerprint) = 64),
    batch_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(batch_fingerprint) = 64),
    status TEXT NOT NULL DEFAULT '' CHECK (status IN ('matched', 'drift', 'not_comparable', 'stream_missing', 'batch_missing')),
    differences JSONB NOT NULL DEFAULT '{}'::JSONB CHECK (jsonb_typeof(differences) = 'object'),
    thresholds JSONB NOT NULL DEFAULT '{}'::JSONB CHECK (jsonb_typeof(thresholds) = 'object'),
    alert_status TEXT NOT NULL DEFAULT 'none' CHECK (alert_status IN ('none', 'open', 'acknowledged', 'resolved')),
    compared_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (feature_set_key, feature_set_version, subject_type, subject_id, as_of, stream_fingerprint, batch_fingerprint)
);

CREATE TABLE IF NOT EXISTS risk_control.backfill_runs (
    backfill_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(backfill_id) > 0),
    feature_set_key TEXT NOT NULL DEFAULT '' CHECK (length(feature_set_key) > 0),
    feature_set_version INTEGER NOT NULL DEFAULT 0 CHECK (feature_set_version > 0),
    policy_key TEXT NOT NULL DEFAULT '' CHECK (length(policy_key) > 0),
    policy_version INTEGER NOT NULL DEFAULT 0 CHECK (policy_version > 0),
    execution_mode TEXT NOT NULL DEFAULT 'backfill' CHECK (execution_mode IN ('simulation', 'shadow', 'backfill', 'live_detection', 'remediation_campaign')),
    start_as_of TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00' CHECK (start_as_of > '1970-01-01 00:00:00+00'),
    end_as_of TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00' CHECK (end_as_of >= start_as_of),
    step_seconds BIGINT NOT NULL DEFAULT 0 CHECK (step_seconds > 0),
    status TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'succeeded', 'failed', 'cancelled')),
    total_count BIGINT NOT NULL DEFAULT 0 CHECK (total_count > 0),
    succeeded_count BIGINT NOT NULL DEFAULT 0 CHECK (succeeded_count >= 0),
    failed_count BIGINT NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    pending_count BIGINT NOT NULL DEFAULT 0 CHECK (pending_count >= 0),
    requested_by TEXT NOT NULL DEFAULT '' CHECK (length(requested_by) > 0),
    approval_ref TEXT NOT NULL DEFAULT '',
    command_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(command_fingerprint) = 64),
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    CHECK (succeeded_count + failed_count + pending_count = total_count)
);

CREATE TABLE IF NOT EXISTS risk_control.backfill_points (
    backfill_id TEXT NOT NULL DEFAULT '' REFERENCES risk_control.backfill_runs(backfill_id),
    as_of TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    child_run_id TEXT NOT NULL DEFAULT '' CHECK (length(child_run_id) > 0),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled')),
    attempt_no INTEGER NOT NULL DEFAULT 0 CHECK (attempt_no >= 0),
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_epoch BIGINT NOT NULL DEFAULT 0 CHECK (lease_epoch >= 0),
    lease_until TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    last_error TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    completed_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    PRIMARY KEY (backfill_id, as_of),
    UNIQUE (child_run_id)
);

CREATE TABLE IF NOT EXISTS risk_control.raw_archive_objects (
    object_id TEXT PRIMARY KEY DEFAULT '' CHECK (length(object_id) > 0),
    object_key TEXT NOT NULL DEFAULT '' CHECK (length(object_key) > 0),
    content_sha256 TEXT NOT NULL DEFAULT '' CHECK (length(content_sha256) = 64),
    byte_size BIGINT NOT NULL DEFAULT 0 CHECK (byte_size >= 0),
    schema_id TEXT NOT NULL DEFAULT '' CHECK (length(schema_id) > 0),
    event_type TEXT NOT NULL DEFAULT '' CHECK (length(event_type) > 0),
    event_time_start TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    event_time_end TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    data_classification TEXT NOT NULL DEFAULT 'confidential' CHECK (
        data_classification IN ('public', 'internal', 'confidential', 'restricted')
    ),
    retention_policy_key TEXT NOT NULL DEFAULT '' CHECK (length(retention_policy_key) > 0),
    state TEXT NOT NULL DEFAULT 'committed' CHECK (state IN ('staging', 'committed', 'quarantined', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01 00:00:00+00',
    UNIQUE (object_key),
    CHECK (event_time_end >= event_time_start)
);

DROP TRIGGER IF EXISTS risk_control_factor_values_require_open ON risk_control.evaluation_factor_values;
CREATE TRIGGER risk_control_factor_values_require_open
BEFORE INSERT ON risk_control.evaluation_factor_values
FOR EACH ROW EXECUTE FUNCTION risk_control.require_open_evaluation();

DROP TRIGGER IF EXISTS risk_control_rule_hits_require_open ON risk_control.evaluation_rule_hits;
CREATE TRIGGER risk_control_rule_hits_require_open
BEFORE INSERT ON risk_control.evaluation_rule_hits
FOR EACH ROW EXECUTE FUNCTION risk_control.require_open_evaluation();

DROP TRIGGER IF EXISTS risk_control_dispositions_require_open ON risk_control.evaluation_business_dispositions;
CREATE TRIGGER risk_control_dispositions_require_open
BEFORE INSERT ON risk_control.evaluation_business_dispositions
FOR EACH ROW EXECUTE FUNCTION risk_control.require_open_evaluation();

DROP TRIGGER IF EXISTS risk_control_actions_require_open ON risk_control.action_intents;
CREATE TRIGGER risk_control_actions_require_open
BEFORE INSERT ON risk_control.action_intents
FOR EACH ROW EXECUTE FUNCTION risk_control.require_open_evaluation();

DROP TRIGGER IF EXISTS risk_control_factor_values_immutable ON risk_control.evaluation_factor_values;
CREATE TRIGGER risk_control_factor_values_immutable
BEFORE UPDATE OR DELETE ON risk_control.evaluation_factor_values
FOR EACH ROW EXECUTE FUNCTION risk_control.reject_change();

DROP TRIGGER IF EXISTS risk_control_rule_hits_immutable ON risk_control.evaluation_rule_hits;
CREATE TRIGGER risk_control_rule_hits_immutable
BEFORE UPDATE OR DELETE ON risk_control.evaluation_rule_hits
FOR EACH ROW EXECUTE FUNCTION risk_control.reject_change();

DROP TRIGGER IF EXISTS risk_control_dispositions_immutable ON risk_control.evaluation_business_dispositions;
CREATE TRIGGER risk_control_dispositions_immutable
BEFORE UPDATE OR DELETE ON risk_control.evaluation_business_dispositions
FOR EACH ROW EXECUTE FUNCTION risk_control.reject_change();

DROP TRIGGER IF EXISTS risk_control_attempts_immutable ON risk_control.action_attempts;
CREATE TRIGGER risk_control_attempts_immutable
BEFORE UPDATE OR DELETE ON risk_control.action_attempts
FOR EACH ROW EXECUTE FUNCTION risk_control.reject_change();

DROP TRIGGER IF EXISTS risk_control_case_evidence_immutable ON risk_control.case_evidence;
CREATE TRIGGER risk_control_case_evidence_immutable
BEFORE UPDATE OR DELETE ON risk_control.case_evidence
FOR EACH ROW EXECUTE FUNCTION risk_control.reject_change();

DROP TRIGGER IF EXISTS risk_control_case_reviews_immutable ON risk_control.case_reviews;
CREATE TRIGGER risk_control_case_reviews_immutable
BEFORE UPDATE OR DELETE ON risk_control.case_reviews
FOR EACH ROW EXECUTE FUNCTION risk_control.reject_change();
