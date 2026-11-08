BEGIN;

INSERT INTO risk_control.feature_runs (
    run_id,
    feature_set_key,
    feature_set_version,
    execution_mode,
    trigger_type,
    as_of,
    input_cutoff_time,
    input_partitions,
    status,
    snapshot_count,
    content_fingerprint,
    command_fingerprint,
    requested_by,
    approval_ref,
    started_at,
    completed_at
) VALUES
    ('risk-domain-demo-20261108-fund', 'subject_fund_activity', 1, 'shadow', 'manual', '2026-11-08 00:00:00+00', '2026-11-08 00:00:00+00', '["2026-11-07"]', 'succeeded', 1, repeat('1', 64), repeat('a', 64), 'coach-lab', 'local-demo', '2026-11-08 00:00:01+00', '2026-11-08 00:00:03+00'),
    ('risk-domain-demo-20261108-acquiring', 'merchant_acquiring_activity', 1, 'shadow', 'manual', '2026-11-08 00:00:00+00', '2026-11-08 00:00:00+00', '["2026-11-07"]', 'succeeded', 1, repeat('2', 64), repeat('b', 64), 'coach-lab', 'local-demo', '2026-11-08 00:00:01+00', '2026-11-08 00:00:03+00'),
    ('risk-domain-demo-20261108-card', 'subject_card_activity', 1, 'shadow', 'manual', '2026-11-08 00:00:00+00', '2026-11-08 00:00:00+00', '["2026-11-07"]', 'succeeded', 1, repeat('3', 64), repeat('c', 64), 'coach-lab', 'local-demo', '2026-11-08 00:00:01+00', '2026-11-08 00:00:03+00'),
    ('risk-domain-demo-20261108-withdrawal', 'subject_withdrawal_activity', 1, 'shadow', 'manual', '2026-11-08 00:00:00+00', '2026-11-08 00:00:00+00', '["2026-11-07"]', 'succeeded', 1, repeat('4', 64), repeat('d', 64), 'coach-lab', 'local-demo', '2026-11-08 00:00:01+00', '2026-11-08 00:00:03+00'),
    ('risk-domain-demo-20261108-security', 'subject_security_activity', 1, 'shadow', 'manual', '2026-11-08 00:00:00+00', '2026-11-08 00:00:00+00', '["2026-11-07"]', 'succeeded', 1, repeat('5', 64), repeat('e', 64), 'coach-lab', 'local-demo', '2026-11-08 00:00:01+00', '2026-11-08 00:00:03+00'),
    ('risk-domain-demo-20261108-profile', 'subject_profile_risk', 1, 'shadow', 'manual', '2026-11-08 00:00:00+00', '2026-11-08 00:00:00+00', '["2026-11-07"]', 'succeeded', 1, repeat('6', 64), repeat('f', 64), 'coach-lab', 'local-demo', '2026-11-08 00:00:01+00', '2026-11-08 00:00:03+00'),
    ('risk-domain-demo-20261108-relationship', 'subject_relationship_risk', 1, 'shadow', 'manual', '2026-11-08 00:00:00+00', '2026-11-08 00:00:00+00', '["2026-11-07"]', 'succeeded', 1, repeat('7', 64), repeat('0', 64), 'coach-lab', 'local-demo', '2026-11-08 00:00:01+00', '2026-11-08 00:00:03+00'),
    ('risk-domain-demo-20261108-provider', 'provider_channel_quality', 1, 'shadow', 'manual', '2026-11-08 00:00:00+00', '2026-11-08 00:00:00+00', '["2026-11-07"]', 'succeeded', 2, repeat('8', 64), repeat('9', 64), 'coach-lab', 'local-demo', '2026-11-08 00:00:01+00', '2026-11-08 00:00:03+00')
ON CONFLICT (run_id) DO NOTHING;

INSERT INTO risk_control.feature_run_fingerprints (source_run_id, content_fingerprint)
VALUES
    ('risk-domain-demo-20261108-fund', repeat('1', 64)),
    ('risk-domain-demo-20261108-acquiring', repeat('2', 64)),
    ('risk-domain-demo-20261108-card', repeat('3', 64)),
    ('risk-domain-demo-20261108-withdrawal', repeat('4', 64)),
    ('risk-domain-demo-20261108-security', repeat('5', 64)),
    ('risk-domain-demo-20261108-profile', repeat('6', 64)),
    ('risk-domain-demo-20261108-relationship', repeat('7', 64)),
    ('risk-domain-demo-20261108-provider', repeat('8', 64))
ON CONFLICT (source_run_id) DO NOTHING;

DO $$
DECLARE
    mismatched_runs BIGINT;
BEGIN
    WITH expected_runs (
        run_id,
        feature_set_key,
        feature_set_version,
        as_of,
        content_fingerprint
    ) AS (
        VALUES
            ('risk-domain-demo-20261108-fund', 'subject_fund_activity', 1, '2026-11-08 00:00:00+00'::TIMESTAMPTZ, repeat('1', 64)),
            ('risk-domain-demo-20261108-acquiring', 'merchant_acquiring_activity', 1, '2026-11-08 00:00:00+00'::TIMESTAMPTZ, repeat('2', 64)),
            ('risk-domain-demo-20261108-card', 'subject_card_activity', 1, '2026-11-08 00:00:00+00'::TIMESTAMPTZ, repeat('3', 64)),
            ('risk-domain-demo-20261108-withdrawal', 'subject_withdrawal_activity', 1, '2026-11-08 00:00:00+00'::TIMESTAMPTZ, repeat('4', 64)),
            ('risk-domain-demo-20261108-security', 'subject_security_activity', 1, '2026-11-08 00:00:00+00'::TIMESTAMPTZ, repeat('5', 64)),
            ('risk-domain-demo-20261108-profile', 'subject_profile_risk', 1, '2026-11-08 00:00:00+00'::TIMESTAMPTZ, repeat('6', 64)),
            ('risk-domain-demo-20261108-relationship', 'subject_relationship_risk', 1, '2026-11-08 00:00:00+00'::TIMESTAMPTZ, repeat('7', 64)),
            ('risk-domain-demo-20261108-provider', 'provider_channel_quality', 1, '2026-11-08 00:00:00+00'::TIMESTAMPTZ, repeat('8', 64))
    )
    SELECT count(*)
      INTO mismatched_runs
      FROM expected_runs e
      LEFT JOIN risk_control.feature_runs r
        ON r.run_id = e.run_id
      LEFT JOIN risk_control.feature_run_fingerprints f
        ON f.source_run_id = e.run_id
     WHERE r.run_id IS NULL
        OR f.source_run_id IS NULL
        OR r.feature_set_key IS DISTINCT FROM e.feature_set_key
        OR r.feature_set_version IS DISTINCT FROM e.feature_set_version
        OR r.as_of IS DISTINCT FROM e.as_of
        OR r.content_fingerprint IS DISTINCT FROM e.content_fingerprint
        OR f.content_fingerprint IS DISTINCT FROM e.content_fingerprint;

    IF mismatched_runs <> 0 THEN
        RAISE EXCEPTION 'feature run seed conflicts with % existing or missing run(s)', mismatched_runs;
    END IF;

    IF (SELECT count(*) FROM risk_control.feature_runs WHERE run_id LIKE 'risk-domain-demo-20261108-%') <> 8 THEN
        RAISE EXCEPTION 'expected 8 registered feature runs';
    END IF;
END;
$$;

COMMIT;
