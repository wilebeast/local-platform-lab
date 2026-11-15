CREATE SCHEMA IF NOT EXISTS risk_control;

CREATE TABLE IF NOT EXISTS risk_control.feature_subject_types (
    subject_type_key TEXT PRIMARY KEY CHECK (length(subject_type_key) > 0),
    description TEXT NOT NULL CHECK (length(description) > 0),
    join_key_schema JSONB NOT NULL CHECK (jsonb_typeof(join_key_schema) = 'object'),
    owner_key TEXT NOT NULL CHECK (length(owner_key) > 0),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deprecated')),
    content_fingerprint TEXT NOT NULL CHECK (length(content_fingerprint) = 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS risk_control.feature_sources (
    source_key TEXT NOT NULL CHECK (length(source_key) > 0),
    source_version INTEGER NOT NULL CHECK (source_version > 0),
    source_domain TEXT NOT NULL CHECK (length(source_domain) > 0),
    source_type TEXT NOT NULL CHECK (source_type IN ('dwd_table', 'kafka_topic', 'managed_snapshot', 'external')),
    schema_ref TEXT NOT NULL CHECK (length(schema_ref) > 0),
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    event_time_field TEXT NOT NULL CHECK (length(event_time_field) > 0),
    subject_mapping JSONB NOT NULL CHECK (jsonb_typeof(subject_mapping) = 'object'),
    data_classification TEXT NOT NULL CHECK (data_classification IN ('public', 'internal', 'confidential', 'restricted')),
    owner_key TEXT NOT NULL CHECK (length(owner_key) > 0),
    content_fingerprint TEXT NOT NULL CHECK (length(content_fingerprint) = 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (source_key, source_version)
);

CREATE OR REPLACE FUNCTION risk_control.reject_registry_identity_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'versioned registry identity is immutable; create a new version';
END;
$$;

DROP TRIGGER IF EXISTS protect_feature_subject_type_identity ON risk_control.feature_subject_types;
CREATE TRIGGER protect_feature_subject_type_identity
BEFORE UPDATE OR DELETE ON risk_control.feature_subject_types
FOR EACH ROW EXECUTE FUNCTION risk_control.reject_registry_identity_mutation();

DROP TRIGGER IF EXISTS protect_feature_source_version_identity ON risk_control.feature_sources;
CREATE TRIGGER protect_feature_source_version_identity
BEFORE UPDATE OR DELETE ON risk_control.feature_sources
FOR EACH ROW EXECUTE FUNCTION risk_control.reject_registry_identity_mutation();

CREATE TABLE IF NOT EXISTS risk_control.feature_set_versions (
    feature_set_key TEXT NOT NULL CHECK (length(feature_set_key) > 0),
    feature_set_version INTEGER NOT NULL CHECK (feature_set_version > 0),
    subject_type_key TEXT NOT NULL REFERENCES risk_control.feature_subject_types(subject_type_key),
    description TEXT NOT NULL CHECK (length(description) > 0),
    owner_key TEXT NOT NULL CHECK (length(owner_key) > 0),
    window_type TEXT NOT NULL CHECK (window_type IN ('none', 'tumbling', 'sliding', 'rolling', 'snapshot')),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'validated', 'published', 'deprecated', 'retired')),
    content_fingerprint TEXT NOT NULL DEFAULT '' CHECK (length(content_fingerprint) IN (0, 64)),
    validated_at TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    deprecated_at TIMESTAMPTZ,
    retired_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (feature_set_key, feature_set_version)
);

CREATE TABLE IF NOT EXISTS risk_control.feature_definitions (
    feature_set_key TEXT NOT NULL,
    feature_set_version INTEGER NOT NULL,
    feature_key TEXT NOT NULL CHECK (length(feature_key) > 0),
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    value_type TEXT NOT NULL CHECK (value_type IN ('bool', 'int', 'double', 'decimal', 'string', 'timestamp', 'duration', 'json')),
    description TEXT NOT NULL CHECK (length(description) > 0),
    unit TEXT NOT NULL DEFAULT '',
    nullable BOOLEAN NOT NULL DEFAULT FALSE,
    defined_when TEXT NOT NULL DEFAULT '',
    missing_semantics TEXT NOT NULL CHECK (missing_semantics IN ('missing', 'undefined', 'zero', 'false', 'empty')),
    max_age_seconds BIGINT NOT NULL CHECK (max_age_seconds > 0),
    supported_evaluation_modes JSONB NOT NULL CHECK (jsonb_typeof(supported_evaluation_modes) = 'array'),
    data_classification TEXT NOT NULL CHECK (data_classification IN ('public', 'internal', 'confidential', 'restricted')),
    PRIMARY KEY (feature_set_key, feature_set_version, feature_key),
    UNIQUE (feature_set_key, feature_set_version, ordinal),
    FOREIGN KEY (feature_set_key, feature_set_version)
        REFERENCES risk_control.feature_set_versions(feature_set_key, feature_set_version)
        ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS risk_control.feature_computations (
    feature_set_key TEXT NOT NULL,
    feature_set_version INTEGER NOT NULL,
    computation_key TEXT NOT NULL CHECK (length(computation_key) > 0),
    computation_mode TEXT NOT NULL CHECK (computation_mode IN ('batch', 'streaming', 'external')),
    artifact_ref TEXT NOT NULL CHECK (length(artifact_ref) > 0),
    artifact_digest TEXT NOT NULL CHECK (length(artifact_digest) = 64),
    window_spec JSONB NOT NULL CHECK (jsonb_typeof(window_spec) = 'object'),
    output_schema JSONB NOT NULL CHECK (jsonb_typeof(output_schema) = 'object'),
    trigger_spec JSONB NOT NULL CHECK (jsonb_typeof(trigger_spec) = 'object'),
    supports_backfill BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (feature_set_key, feature_set_version),
    UNIQUE (computation_key),
    FOREIGN KEY (feature_set_key, feature_set_version)
        REFERENCES risk_control.feature_set_versions(feature_set_key, feature_set_version)
        ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS risk_control.feature_computation_sources (
    feature_set_key TEXT NOT NULL,
    feature_set_version INTEGER NOT NULL,
    source_key TEXT NOT NULL,
    source_version INTEGER NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    PRIMARY KEY (feature_set_key, feature_set_version, source_key, source_version),
    UNIQUE (feature_set_key, feature_set_version, ordinal),
    FOREIGN KEY (feature_set_key, feature_set_version)
        REFERENCES risk_control.feature_computations(feature_set_key, feature_set_version)
        ON DELETE CASCADE,
    FOREIGN KEY (source_key, source_version)
        REFERENCES risk_control.feature_sources(source_key, source_version)
);

CREATE TABLE IF NOT EXISTS risk_control.feature_quality_contracts (
    feature_set_key TEXT NOT NULL,
    feature_set_version INTEGER NOT NULL,
    freshness_seconds BIGINT NOT NULL CHECK (freshness_seconds > 0),
    minimum_completeness NUMERIC(8,7) NOT NULL CHECK (minimum_completeness >= 0 AND minimum_completeness <= 1),
    maximum_reconciliation NUMERIC(8,7) NOT NULL CHECK (maximum_reconciliation >= 0 AND maximum_reconciliation <= 1),
    required_checks JSONB NOT NULL CHECK (jsonb_typeof(required_checks) = 'array'),
    PRIMARY KEY (feature_set_key, feature_set_version),
    FOREIGN KEY (feature_set_key, feature_set_version)
        REFERENCES risk_control.feature_set_versions(feature_set_key, feature_set_version)
        ON DELETE CASCADE
);

CREATE OR REPLACE FUNCTION risk_control.reject_published_feature_set_content_change()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    parent_status TEXT;
    parent_key TEXT;
    parent_version INTEGER;
BEGIN
    parent_key := COALESCE(NEW.feature_set_key, OLD.feature_set_key);
    parent_version := COALESCE(NEW.feature_set_version, OLD.feature_set_version);
    SELECT status INTO parent_status
    FROM risk_control.feature_set_versions
    WHERE feature_set_key = parent_key AND feature_set_version = parent_version;
    IF parent_status IN ('published', 'deprecated', 'retired') THEN
        RAISE EXCEPTION 'published feature set content is immutable';
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$;

DO $$
DECLARE
    table_name TEXT;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'feature_definitions',
        'feature_computations',
        'feature_computation_sources',
        'feature_quality_contracts'
    ] LOOP
        EXECUTE format('DROP TRIGGER IF EXISTS protect_published_feature_set_content ON risk_control.%I', table_name);
        EXECUTE format(
            'CREATE TRIGGER protect_published_feature_set_content BEFORE INSERT OR UPDATE OR DELETE ON risk_control.%I FOR EACH ROW EXECUTE FUNCTION risk_control.reject_published_feature_set_content_change()',
            table_name
        );
    END LOOP;
END;
$$;

CREATE TABLE IF NOT EXISTS risk_control.feature_factor_bindings (
    consumer_type TEXT NOT NULL CHECK (length(consumer_type) > 0),
    consumer_key TEXT NOT NULL CHECK (length(consumer_key) > 0),
    consumer_version INTEGER NOT NULL CHECK (consumer_version > 0),
    factor_key TEXT NOT NULL CHECK (length(factor_key) > 0),
    feature_set_key TEXT NOT NULL,
    feature_set_version INTEGER NOT NULL,
    feature_key TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'retired')),
    bound_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retired_at TIMESTAMPTZ,
    PRIMARY KEY (consumer_type, consumer_key, consumer_version, factor_key),
    FOREIGN KEY (feature_set_key, feature_set_version, feature_key)
        REFERENCES risk_control.feature_definitions(feature_set_key, feature_set_version, feature_key)
);

CREATE INDEX IF NOT EXISTS feature_factor_bindings_feature_lookup_idx
    ON risk_control.feature_factor_bindings(feature_set_key, feature_set_version, status);

CREATE TABLE IF NOT EXISTS risk_control.feature_serving_shadow_comparisons (
    comparison_id TEXT PRIMARY KEY CHECK (length(comparison_id) = 64),
    request_id TEXT NOT NULL CHECK (length(request_id) > 0),
    feature_set_key TEXT NOT NULL CHECK (length(feature_set_key) > 0),
    feature_set_version INTEGER NOT NULL CHECK (feature_set_version > 0),
    subject_type TEXT NOT NULL CHECK (length(subject_type) > 0),
    subject_keys JSONB NOT NULL DEFAULT '{}'::JSONB,
    cutoff_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01T00:00:00Z',
    candidate_as_of TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01T00:00:00Z',
    reference_as_of TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01T00:00:00Z',
    candidate_latency_millis BIGINT NOT NULL DEFAULT 0 CHECK (candidate_latency_millis >= 0),
    reference_latency_millis BIGINT NOT NULL DEFAULT 0 CHECK (reference_latency_millis >= 0),
    comparison_status TEXT NOT NULL CHECK (comparison_status IN (
        'matched', 'mismatch', 'missing_candidate', 'missing_reference', 'candidate_error',
        'reference_error'
    )),
    differences JSONB NOT NULL DEFAULT '[]'::JSONB,
    content_fingerprint TEXT NOT NULL CHECK (length(content_fingerprint) = 64),
    compared_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE risk_control.feature_serving_shadow_comparisons
    ADD COLUMN IF NOT EXISTS cutoff_at TIMESTAMPTZ NOT NULL DEFAULT '1970-01-01T00:00:00Z';
ALTER TABLE risk_control.feature_serving_shadow_comparisons
    ADD COLUMN IF NOT EXISTS candidate_latency_millis BIGINT NOT NULL DEFAULT 0;
ALTER TABLE risk_control.feature_serving_shadow_comparisons
    ADD COLUMN IF NOT EXISTS reference_latency_millis BIGINT NOT NULL DEFAULT 0;
ALTER TABLE risk_control.feature_serving_shadow_comparisons
    DROP CONSTRAINT IF EXISTS feature_serving_shadow_comparisons_comparison_status_check;
ALTER TABLE risk_control.feature_serving_shadow_comparisons
    ADD CONSTRAINT feature_serving_shadow_comparisons_comparison_status_check
    CHECK (comparison_status IN (
        'matched', 'mismatch', 'missing_candidate', 'missing_reference', 'candidate_error',
        'reference_error'
    ));

CREATE INDEX IF NOT EXISTS feature_serving_shadow_comparisons_status_idx
    ON risk_control.feature_serving_shadow_comparisons(
        feature_set_key, feature_set_version, comparison_status, compared_at DESC
    );

CREATE TABLE IF NOT EXISTS risk_control.feature_serving_promotion_assessments (
    assessment_id TEXT PRIMARY KEY CHECK (length(assessment_id) = 64),
    request_id TEXT NOT NULL CHECK (length(request_id) > 0),
    feature_set_key TEXT NOT NULL,
    feature_set_version INTEGER NOT NULL,
    evidence_start TIMESTAMPTZ NOT NULL,
    evidence_end TIMESTAMPTZ NOT NULL,
    bucket_seconds BIGINT NOT NULL CHECK (bucket_seconds > 0),
    criteria JSONB NOT NULL CHECK (jsonb_typeof(criteria) = 'object'),
    evidence JSONB NOT NULL CHECK (jsonb_typeof(evidence) = 'object'),
    eligible BOOLEAN NOT NULL,
    reasons JSONB NOT NULL CHECK (jsonb_typeof(reasons) = 'array'),
    requested_by TEXT NOT NULL CHECK (length(requested_by) > 0),
    request_reason TEXT NOT NULL CHECK (length(request_reason) > 0),
    content_fingerprint TEXT NOT NULL CHECK (length(content_fingerprint) = 64),
    assessed_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (feature_set_key, feature_set_version)
        REFERENCES risk_control.feature_set_versions(feature_set_key, feature_set_version)
);

CREATE INDEX IF NOT EXISTS feature_serving_promotion_assessments_lookup_idx
    ON risk_control.feature_serving_promotion_assessments(
        feature_set_key, feature_set_version, assessed_at DESC
    );

CREATE TABLE IF NOT EXISTS risk_control.feature_serving_change_requests (
    request_id TEXT PRIMARY KEY CHECK (length(request_id) > 0),
    change_type TEXT NOT NULL CHECK (change_type IN ('promotion')),
    feature_set_key TEXT NOT NULL,
    feature_set_version INTEGER NOT NULL,
    request_payload JSONB NOT NULL CHECK (jsonb_typeof(request_payload) = 'object'),
    request_status TEXT NOT NULL CHECK (request_status IN (
        'pending', 'approved', 'rejected', 'blocked'
    )),
    requested_by TEXT NOT NULL CHECK (length(requested_by) > 0),
    reviewed_by TEXT NOT NULL DEFAULT '',
    review_reason TEXT NOT NULL DEFAULT '',
    assessment_id TEXT NOT NULL DEFAULT '',
    content_fingerprint TEXT NOT NULL CHECK (length(content_fingerprint) = 64),
    created_at TIMESTAMPTZ NOT NULL,
    reviewed_at TIMESTAMPTZ,
    FOREIGN KEY (feature_set_key, feature_set_version)
        REFERENCES risk_control.feature_set_versions(feature_set_key, feature_set_version)
);

CREATE INDEX IF NOT EXISTS feature_serving_change_requests_pending_idx
    ON risk_control.feature_serving_change_requests(
        feature_set_key, feature_set_version, request_status, created_at DESC
    );

CREATE TABLE IF NOT EXISTS risk_control.feature_serving_routes (
    feature_set_key TEXT NOT NULL,
    feature_set_version INTEGER NOT NULL,
    serving_mode TEXT NOT NULL DEFAULT 'shadow' CHECK (serving_mode IN ('shadow', 'online')),
    generation BIGINT NOT NULL DEFAULT 0 CHECK (generation >= 0),
    last_event_id TEXT NOT NULL DEFAULT '',
    changed_by TEXT NOT NULL DEFAULT '',
    change_reason TEXT NOT NULL DEFAULT '',
    changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (feature_set_key, feature_set_version),
    FOREIGN KEY (feature_set_key, feature_set_version)
        REFERENCES risk_control.feature_set_versions(feature_set_key, feature_set_version)
);

CREATE TABLE IF NOT EXISTS risk_control.feature_serving_route_events (
    event_id TEXT PRIMARY KEY CHECK (length(event_id) = 64),
    request_id TEXT NOT NULL CHECK (length(request_id) > 0),
    feature_set_key TEXT NOT NULL,
    feature_set_version INTEGER NOT NULL,
    change_type TEXT NOT NULL CHECK (change_type IN ('promotion', 'rollback')),
    from_mode TEXT NOT NULL CHECK (from_mode IN ('shadow', 'online')),
    to_mode TEXT NOT NULL CHECK (to_mode IN ('shadow', 'online')),
    generation BIGINT NOT NULL CHECK (generation >= 0),
    changed_by TEXT NOT NULL CHECK (length(changed_by) > 0),
    change_reason TEXT NOT NULL CHECK (length(change_reason) > 0),
    content_fingerprint TEXT NOT NULL CHECK (length(content_fingerprint) = 64),
    changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (feature_set_key, feature_set_version)
        REFERENCES risk_control.feature_set_versions(feature_set_key, feature_set_version)
);

CREATE TABLE IF NOT EXISTS risk_control.feature_serving_health_alerts (
    alert_id TEXT PRIMARY KEY CHECK (length(alert_id) = 64),
    check_id TEXT NOT NULL CHECK (length(check_id) > 0),
    feature_set_key TEXT NOT NULL,
    feature_set_version INTEGER NOT NULL,
    route_generation BIGINT NOT NULL CHECK (route_generation >= 0),
    alert_status TEXT NOT NULL CHECK (alert_status IN ('open', 'resolved')),
    reason_codes JSONB NOT NULL CHECK (jsonb_typeof(reason_codes) = 'array'),
    evidence JSONB NOT NULL CHECK (jsonb_typeof(evidence) = 'object'),
    criteria JSONB NOT NULL CHECK (jsonb_typeof(criteria) = 'object'),
    recommended_action TEXT NOT NULL CHECK (recommended_action IN ('rollback')),
    content_fingerprint TEXT NOT NULL CHECK (length(content_fingerprint) = 64),
    created_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ,
    FOREIGN KEY (feature_set_key, feature_set_version)
        REFERENCES risk_control.feature_set_versions(feature_set_key, feature_set_version)
);

CREATE INDEX IF NOT EXISTS feature_serving_health_alerts_open_idx
    ON risk_control.feature_serving_health_alerts(
        feature_set_key, feature_set_version, alert_status, created_at DESC
    );

CREATE OR REPLACE FUNCTION risk_control.enforce_feature_set_version_lifecycle()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.status <> 'draft' THEN
            RAISE EXCEPTION 'feature set version must be created as draft';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        IF OLD.status <> 'draft' THEN
            RAISE EXCEPTION 'only draft feature set versions can be deleted';
        END IF;
        RETURN OLD;
    END IF;

    IF OLD.status <> NEW.status AND NOT (
        (OLD.status = 'draft' AND NEW.status = 'validated') OR
        (OLD.status = 'validated' AND NEW.status IN ('draft', 'published')) OR
        (OLD.status = 'published' AND NEW.status = 'deprecated') OR
        (OLD.status = 'deprecated' AND NEW.status IN ('published', 'retired'))
    ) THEN
        RAISE EXCEPTION 'invalid feature set state transition: % -> %', OLD.status, NEW.status;
    END IF;

    IF OLD.status IN ('published', 'deprecated', 'retired') AND (
        NEW.subject_type_key IS DISTINCT FROM OLD.subject_type_key OR
        NEW.description IS DISTINCT FROM OLD.description OR
        NEW.owner_key IS DISTINCT FROM OLD.owner_key OR
        NEW.window_type IS DISTINCT FROM OLD.window_type OR
        NEW.content_fingerprint IS DISTINCT FROM OLD.content_fingerprint
    ) THEN
        RAISE EXCEPTION 'published feature set metadata is immutable';
    END IF;

    IF NEW.status = 'published' AND (
        length(NEW.content_fingerprint) <> 64 OR NEW.published_at IS NULL
    ) THEN
        RAISE EXCEPTION 'published feature set requires fingerprint and published_at';
    END IF;

    IF NEW.status = 'retired' AND EXISTS (
        SELECT 1
        FROM risk_control.feature_factor_bindings fb
        WHERE fb.feature_set_key = NEW.feature_set_key
          AND fb.feature_set_version = NEW.feature_set_version
          AND fb.status = 'active'
    ) THEN
        RAISE EXCEPTION 'feature set version still has active feature factors';
    END IF;
    IF NEW.status = 'retired' AND EXISTS (
        SELECT 1
        FROM risk_control.feature_serving_routes sr
        WHERE sr.feature_set_key = NEW.feature_set_key
          AND sr.feature_set_version = NEW.feature_set_version
          AND sr.serving_mode = 'online'
    ) THEN
        RAISE EXCEPTION 'feature set version is still routed to online serving';
    END IF;
    IF NEW.status = 'retired' AND EXISTS (
        SELECT 1
        FROM risk_control.feature_serving_change_requests cr
        WHERE cr.feature_set_key = NEW.feature_set_key
          AND cr.feature_set_version = NEW.feature_set_version
          AND cr.request_status = 'pending'
    ) THEN
        RAISE EXCEPTION 'feature set version still has a pending serving change';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS enforce_feature_set_version_lifecycle ON risk_control.feature_set_versions;
CREATE TRIGGER enforce_feature_set_version_lifecycle
BEFORE INSERT OR UPDATE OR DELETE ON risk_control.feature_set_versions
FOR EACH ROW EXECUTE FUNCTION risk_control.enforce_feature_set_version_lifecycle();
