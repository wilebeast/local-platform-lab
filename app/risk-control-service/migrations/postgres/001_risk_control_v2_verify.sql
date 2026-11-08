BEGIN;

INSERT INTO risk_control.policy_activations (
    activation_id,
    environment,
    scene_key,
    rollout_cohort,
    policy_key,
    policy_version,
    snapshot_fingerprint,
    generation,
    previous_generation,
    status,
    approved_by,
    approval_ref
) VALUES
    (
        'activation-schema-verification-1',
        'local',
        'withdrawal',
        'default',
        'withdrawal-risk',
        1,
        repeat('1', 64),
        1,
        0,
        'retired',
        'reviewer-schema-verification',
        'approval-schema-verification-1'
    ),
    (
        'activation-schema-verification-2',
        'local',
        'withdrawal',
        'default',
        'withdrawal-risk',
        2,
        repeat('2', 64),
        2,
        1,
        'active',
        'reviewer-schema-verification',
        'approval-schema-verification-2'
    );

DO $$
DECLARE
    was_rejected BOOLEAN := FALSE;
BEGIN
    BEGIN
        INSERT INTO risk_control.policy_activations (
            activation_id,
            environment,
            scene_key,
            rollout_cohort,
            policy_key,
            policy_version,
            snapshot_fingerprint,
            generation,
            previous_generation,
            status,
            approved_by,
            approval_ref
        ) VALUES (
            'activation-schema-verification-3',
            'local',
            'withdrawal',
            'default',
            'withdrawal-risk',
            3,
            repeat('3', 64),
            3,
            2,
            'active',
            'reviewer-schema-verification',
            'approval-schema-verification-3'
        );
    EXCEPTION WHEN unique_violation THEN
        was_rejected := TRUE;
    END;

    IF NOT was_rejected THEN
        RAISE EXCEPTION 'second active policy activation was not rejected';
    END IF;
END;
$$;

INSERT INTO risk_control.evaluations (
    evaluation_id,
    environment,
    scene_key,
    request_id,
    request_fingerprint,
    subject_type,
    subject_id,
    capability,
    business_object_type,
    business_object_id,
    business_object_version,
    execution_mode,
    evaluation_status,
    decision,
    disposition,
    decision_source,
    risk_level,
    policy_key,
    policy_version,
    snapshot_fingerprint,
    activation_generation,
    observed_at,
    input_cutoff_time,
    decision_time
) VALUES (
    'evaluation-schema-verification',
    'local',
    'withdrawal',
    'request-schema-verification',
    repeat('a', 64),
    'organization',
    'organization-schema-verification',
    'withdrawal',
    'withdrawal_order',
    'withdrawal-schema-verification',
    1,
    'shadow',
    'completed',
    'block',
    'manual_review',
    'live',
    'high',
    'withdrawal-risk',
    1,
    repeat('b', 64),
    1,
    '2026-11-08 00:00:00+00',
    '2026-11-08 00:00:00+00',
    '2026-11-08 00:00:01+00'
);

INSERT INTO risk_control.evaluation_factor_values (
    evaluation_id,
    factor_key,
    factor_version,
    factor_type,
    value_type,
    value_double,
    source_type,
    source_ref,
    observed_at,
    result_status
) VALUES (
    'evaluation-schema-verification',
    'outflow_ratio_24h',
    1,
    'feature',
    'double',
    0.95,
    'feature_snapshot',
    'feature-run-schema-verification',
    '2026-11-08 00:00:00+00',
    'resolved'
);

DO $$
DECLARE
    was_rejected BOOLEAN := FALSE;
BEGIN
    BEGIN
        INSERT INTO risk_control.evaluation_factor_values (
            evaluation_id,
            factor_key,
            factor_version,
            factor_type,
            value_type,
            value_double,
            value_text,
            source_type,
            observed_at,
            result_status
        ) VALUES (
            'evaluation-schema-verification',
            'invalid-typed-factor',
            1,
            'feature',
            'double',
            0.95,
            'must-not-be-populated',
            'feature_snapshot',
            '2026-11-08 00:00:00+00',
            'resolved'
        );
    EXCEPTION WHEN check_violation THEN
        was_rejected := TRUE;
    END;

    IF NOT was_rejected THEN
        RAISE EXCEPTION 'factor value type mismatch was not rejected';
    END IF;
END;
$$;

INSERT INTO risk_control.evaluation_rule_hits (
    evaluation_id,
    rule_key,
    rule_version,
    expression_fingerprint,
    matched,
    evaluation_status
) VALUES (
    'evaluation-schema-verification',
    'rapid-outflow',
    1,
    repeat('c', 64),
    TRUE,
    'matched'
);

INSERT INTO risk_control.evaluation_business_dispositions (
    evaluation_id,
    disposition_type,
    schema_version,
    owner_module,
    value_type,
    value_text
) VALUES (
    'evaluation-schema-verification',
    'withdrawal_state',
    1,
    'withdrawal',
    'string',
    'manual_review'
);

INSERT INTO risk_control.action_intents (
    action_id,
    evaluation_id,
    action_type,
    effect_idempotency_key,
    target_type,
    target_id
) VALUES (
    'action-schema-verification',
    'evaluation-schema-verification',
    'activate_restriction',
    'withdrawal:organization-schema-verification:generation:1',
    'organization',
    'organization-schema-verification'
);

INSERT INTO risk_control.outbox_events (
    event_id,
    evaluation_id,
    aggregate_type,
    aggregate_id,
    event_type,
    event_version,
    schema_id,
    partition_key,
    payload_fingerprint,
    occurred_at
) VALUES (
    'event-schema-verification',
    'evaluation-schema-verification',
    'action',
    'action-schema-verification',
    'risk.action.requested',
    1,
    'risk.action.requested.v1',
    'organization-schema-verification',
    repeat('d', 64),
    '2026-11-08 00:00:01+00'
);

DO $$
DECLARE
    was_rejected BOOLEAN := FALSE;
BEGIN
    BEGIN
        INSERT INTO risk_control.action_intents (
            action_id,
            evaluation_id,
            action_type,
            effect_idempotency_key,
            target_type,
            target_id
        ) VALUES (
            'action-schema-verification-conflict',
            'evaluation-schema-verification',
            'activate_restriction',
            'withdrawal:organization-schema-verification:generation:1',
            'organization',
            'organization-schema-verification'
        );
    EXCEPTION WHEN unique_violation THEN
        was_rejected := TRUE;
    END;

    IF NOT was_rejected THEN
        RAISE EXCEPTION 'action effect idempotency conflict was not rejected';
    END IF;
END;
$$;

UPDATE risk_control.evaluations
   SET sealed = TRUE
 WHERE evaluation_id = 'evaluation-schema-verification';

DO $$
DECLARE
    was_rejected BOOLEAN := FALSE;
BEGIN
    BEGIN
        INSERT INTO risk_control.evaluation_factor_values (
            evaluation_id,
            factor_key,
            factor_version,
            factor_type,
            value_type,
            source_type,
            observed_at,
            result_status
        ) VALUES (
            'evaluation-schema-verification',
            'late-factor',
            1,
            'context',
            'string',
            'request',
            '2026-11-08 00:00:00+00',
            'resolved'
        );
    EXCEPTION WHEN SQLSTATE '55000' THEN
        was_rejected := TRUE;
    END;

    IF NOT was_rejected THEN
        RAISE EXCEPTION 'sealed evaluation accepted new factor evidence';
    END IF;
END;
$$;

DO $$
DECLARE
    was_rejected BOOLEAN := FALSE;
BEGIN
    BEGIN
        UPDATE risk_control.evaluation_factor_values
           SET value_double = 0.10
         WHERE evaluation_id = 'evaluation-schema-verification'
           AND factor_key = 'outflow_ratio_24h'
           AND factor_version = 1;
    EXCEPTION WHEN SQLSTATE '55000' THEN
        was_rejected := TRUE;
    END;

    IF NOT was_rejected THEN
        RAISE EXCEPTION 'immutable factor evidence was updated';
    END IF;
END;
$$;

DO $$
DECLARE
    was_rejected BOOLEAN := FALSE;
BEGIN
    BEGIN
        UPDATE risk_control.evaluations
           SET risk_level = 'low'
         WHERE evaluation_id = 'evaluation-schema-verification';
    EXCEPTION WHEN SQLSTATE '55000' THEN
        was_rejected := TRUE;
    END;

    IF NOT was_rejected THEN
        RAISE EXCEPTION 'sealed evaluation was updated';
    END IF;
END;
$$;

INSERT INTO risk_control.restrictions (
    restriction_id,
    subject_type,
    subject_id,
    capability,
    scope_key,
    generation,
    state,
    source_type,
    source_id
) VALUES (
    'restriction-schema-verification',
    'organization',
    'organization-schema-verification',
    'withdrawal',
    'global',
    1,
    'active',
    'evaluation',
    'evaluation-schema-verification'
);

DO $$
DECLARE
    was_rejected BOOLEAN := FALSE;
BEGIN
    BEGIN
        INSERT INTO risk_control.restrictions (
            restriction_id,
            subject_type,
            subject_id,
            capability,
            scope_key,
            generation,
            state,
            source_type,
            source_id
        ) VALUES (
            'restriction-schema-verification-conflict',
            'organization',
            'organization-schema-verification',
            'withdrawal',
            'global',
            2,
            'active',
            'evaluation',
            'evaluation-schema-verification'
        );
    EXCEPTION WHEN unique_violation THEN
        was_rejected := TRUE;
    END;

    IF NOT was_rejected THEN
        RAISE EXCEPTION 'second active restriction was not rejected';
    END IF;
END;
$$;

SELECT
    (SELECT count(*) FROM risk_control.policy_activations) AS activation_rows,
    (SELECT count(*) FROM risk_control.evaluations) AS evaluation_rows,
    (SELECT count(*) FROM risk_control.evaluation_factor_values) AS factor_rows,
    (SELECT count(*) FROM risk_control.evaluation_rule_hits) AS rule_hit_rows,
    (SELECT count(*) FROM risk_control.evaluation_business_dispositions) AS disposition_rows,
    (SELECT count(*) FROM risk_control.action_intents) AS action_rows,
    (SELECT count(*) FROM risk_control.outbox_events) AS outbox_rows,
    (SELECT count(*) FROM risk_control.restrictions WHERE state = 'active') AS active_restriction_rows;

ROLLBACK;
