INSERT INTO risk_warehouse.dws_subject_fund_activity_history_v2
SELECT
    'subject_fund_activity', 1, '24h', subject_type, subject_id, currency,
    1794009600000, 1794096000000, 1794096000000, 1794096000000,
    sumIf(amount_minor, direction = 'inflow'),
    sumIf(amount_minor, direction = 'outflow'),
    if(sumIf(amount_minor, direction = 'inflow') = 0, 0,
       sumIf(amount_minor, direction = 'outflow') / sumIf(amount_minor, direction = 'inflow')),
    if(sumIf(amount_minor, direction = 'inflow') = 0, 0, 1),
    count(),
    'risk-domain-demo-20261108-fund',
    lower(hex(SHA256(concat(subject_id, currency, toString(sum(amount_minor)), toString(count()))))),
    'passed', 1794096002000, 1794096003000,
    'risk-demo/features/subject-fund-activity-24h.json',
    lower(hex(SHA256('risk-demo/features/subject-fund-activity-24h.json')))
FROM risk_warehouse.dwd_fund_movements_v2 FINAL
WHERE occurred_at_millis > 1794009600000 AND occurred_at_millis <= 1794096000000
GROUP BY subject_type, subject_id, currency;

INSERT INTO risk_warehouse.dws_subject_fund_activity_current_v2
SELECT
    feature_set_key, feature_set_version, window_type, subject_type, subject_id, currency,
    window_start_millis, window_end_millis, as_of_millis, input_cutoff_millis,
    inflow_minor, outflow_minor, outflow_ratio, outflow_ratio_defined, source_event_count,
    source_run_id, content_fingerprint, quality_status, computed_at_millis, ingested_at_millis
FROM risk_warehouse.dws_subject_fund_activity_history_v2 FINAL
WHERE source_run_id = 'risk-domain-demo-20261108-fund';

INSERT INTO risk_warehouse.dws_merchant_acquiring_activity_history_v1
SELECT
    'merchant_acquiring_activity', 1, '24h', merchant_id, currency,
    1794009600000, 1794096000000, 1794096000000,
    count(), countIf(result_status = 'succeeded'), countIf(result_status = 'failed'),
    sum(amount_minor), sumIf(amount_minor, result_status = 'succeeded'),
    countIf(result_status = 'succeeded') / count(), uniqExact(payer_subject_id),
    'risk-domain-demo-20261108-acquiring',
    lower(hex(SHA256(concat(merchant_id, currency, toString(count()), toString(sum(amount_minor)))))),
    1794096002000, 1794096003000
FROM risk_warehouse.dwd_acquiring_payment_attempts_v1 FINAL
WHERE occurred_at_millis > 1794009600000 AND occurred_at_millis <= 1794096000000
GROUP BY merchant_id, currency;

INSERT INTO risk_warehouse.dws_merchant_acquiring_activity_current_v1
SELECT *
FROM risk_warehouse.dws_merchant_acquiring_activity_history_v1 FINAL
WHERE source_run_id = 'risk-domain-demo-20261108-acquiring';

INSERT INTO risk_warehouse.dws_subject_card_activity_history_v1
SELECT
    'subject_card_activity', 1, '24h', subject_type, subject_id, currency,
    1794009600000, 1794096000000, 1794096000000,
    count(), countIf(decision = 'approved'), countIf(decision = 'declined'),
    sum(amount_minor), sumIf(amount_minor, decision = 'approved'),
    countIf(decision = 'declined') / count(),
    countIf(merchant_country != home_country), uniqExact(merchant_id),
    'risk-domain-demo-20261108-card',
    lower(hex(SHA256(concat(subject_id, currency, toString(count()), toString(sum(amount_minor)))))),
    1794096002000, 1794096003000
FROM risk_warehouse.dwd_card_authorizations_v1 FINAL
WHERE occurred_at_millis > 1794009600000 AND occurred_at_millis <= 1794096000000
GROUP BY subject_type, subject_id, currency;

INSERT INTO risk_warehouse.dws_subject_card_activity_current_v1
SELECT *
FROM risk_warehouse.dws_subject_card_activity_history_v1 FINAL
WHERE source_run_id = 'risk-domain-demo-20261108-card';

INSERT INTO risk_warehouse.dws_subject_withdrawal_activity_history_v1
SELECT
    'subject_withdrawal_activity', 1, '24h', subject_type, subject_id, currency,
    1794009600000, 1794096000000, 1794096000000,
    count(), countIf(status = 'completed'), countIf(status = 'failed'), countIf(status = 'pending_review'),
    sum(amount_minor), sumIf(amount_minor, status = 'completed'),
    countIf(destination_first_seen = 1), countIf(status = 'failed') / count(),
    'risk-domain-demo-20261108-withdrawal',
    lower(hex(SHA256(concat(subject_id, currency, toString(count()), toString(sum(amount_minor)))))),
    1794096002000, 1794096003000
FROM risk_warehouse.dwd_withdrawal_orders_v1 FINAL
WHERE occurred_at_millis > 1794009600000 AND occurred_at_millis <= 1794096000000
GROUP BY subject_type, subject_id, currency;

INSERT INTO risk_warehouse.dws_subject_withdrawal_activity_current_v1
SELECT *
FROM risk_warehouse.dws_subject_withdrawal_activity_history_v1 FINAL
WHERE source_run_id = 'risk-domain-demo-20261108-withdrawal';

INSERT INTO risk_warehouse.dws_subject_security_activity_history_v1
SELECT
    'subject_security_activity', 1, '24h', subject_type, subject_id,
    1794009600000, 1794096000000, 1794096000000,
    count(), countIf(security_event_type = 'login_failed'), countIf(security_event_type = 'new_device_login'),
    countIf(security_event_type = 'password_reset'), countIf(security_event_type = 'two_factor_changed'),
    countIf(security_event_type = 'administrator_added'), countIf(risk_signal = 'high'),
    'risk-domain-demo-20261108-security',
    lower(hex(SHA256(concat(subject_id, toString(count()), toString(countIf(risk_signal = 'high')))))),
    1794096002000, 1794096003000
FROM risk_warehouse.dwd_account_security_events_v1 FINAL
WHERE occurred_at_millis > 1794009600000 AND occurred_at_millis <= 1794096000000
GROUP BY subject_type, subject_id;

INSERT INTO risk_warehouse.dws_subject_security_activity_current_v1
SELECT *
FROM risk_warehouse.dws_subject_security_activity_history_v1 FINAL
WHERE source_run_id = 'risk-domain-demo-20261108-security';

INSERT INTO risk_warehouse.dws_subject_profile_risk_history_v1
SELECT
    'subject_profile_risk', 1, '24h', subject_type, subject_id,
    1794009600000, 1794096000000, 1794096000000,
    count(), countIf(verification_event_type = 'verification_failed'),
    countIf(verification_event_type = 'material_changed'),
    sum(mismatch_field_count), sum(changed_field_count),
    argMax(verification_status, occurred_at_millis), argMax(risk_level, occurred_at_millis),
    'risk-domain-demo-20261108-profile',
    lower(hex(SHA256(concat(subject_id, toString(count()), argMax(verification_status, occurred_at_millis))))),
    1794096002000, 1794096003000
FROM risk_warehouse.dwd_customer_verification_events_v1 FINAL
WHERE occurred_at_millis > 1794009600000 AND occurred_at_millis <= 1794096000000
GROUP BY subject_type, subject_id;

INSERT INTO risk_warehouse.dws_subject_profile_risk_current_v1
SELECT *
FROM risk_warehouse.dws_subject_profile_risk_history_v1 FINAL
WHERE source_run_id = 'risk-domain-demo-20261108-profile';

INSERT INTO risk_warehouse.dws_subject_relationship_risk_history_v1
SELECT
    'subject_relationship_risk', 1, '24h', subject_type, subject_id,
    1794009600000, 1794096000000, 1794096000000,
    countIf(active = 1),
    countIf(active = 1 AND relation_type = 'shared_device'),
    countIf(active = 1 AND relation_type = 'shared_ip'),
    countIf(active = 1 AND relation_type = 'shared_destination'),
    uniqExactIf(related_subject_id, active = 1 AND related_subject_risk_level = 'high'),
    'risk-domain-demo-20261108-relationship',
    lower(hex(SHA256(concat(subject_id, toString(countIf(active = 1)), toString(uniqExactIf(related_subject_id, active = 1)))))),
    1794096002000, 1794096003000
FROM risk_warehouse.dwd_subject_relationship_edges_v1 FINAL
WHERE occurred_at_millis > 1794009600000 AND occurred_at_millis <= 1794096000000
GROUP BY subject_type, subject_id;

INSERT INTO risk_warehouse.dws_subject_relationship_risk_current_v1
SELECT *
FROM risk_warehouse.dws_subject_relationship_risk_history_v1 FINAL
WHERE source_run_id = 'risk-domain-demo-20261108-relationship';

INSERT INTO risk_warehouse.dws_provider_channel_quality_history_v1
SELECT
    'provider_channel_quality', 1, '24h', provider_code, channel_domain,
    1794009600000, 1794096000000, 1794096000000,
    count(), countIf(result_status = 'succeeded'), countIf(result_status = 'failed'), countIf(result_status = 'timeout'),
    countIf(result_status = 'succeeded') / count(), avg(latency_ms), quantileExact(0.95)(latency_ms),
    countIf(duplicate_callback = 1), countIf(reconciliation_mismatch = 1),
    'risk-domain-demo-20261108-provider',
    lower(hex(SHA256(concat(provider_code, channel_domain, toString(count()), toString(sum(latency_ms)))))),
    1794096002000, 1794096003000
FROM risk_warehouse.dwd_provider_channel_events_v1 FINAL
WHERE occurred_at_millis > 1794009600000 AND occurred_at_millis <= 1794096000000
GROUP BY provider_code, channel_domain;

INSERT INTO risk_warehouse.dws_provider_channel_quality_current_v1
SELECT *
FROM risk_warehouse.dws_provider_channel_quality_history_v1 FINAL
WHERE source_run_id = 'risk-domain-demo-20261108-provider';

INSERT INTO risk_warehouse.feature_snapshot_history_v2
SELECT
    feature_set_key, feature_set_version, subject_type, subject_id, subject_id,
    1794096000000, 1794009600000, 1794096000000, 1794096000000,
    source_run_id, content_fingerprint, 'passed', values_json, source_event_count,
    1794096002000, 1794096003000, concat('risk-demo/features/', feature_set_key, '.json'),
    lower(hex(SHA256(concat('risk-demo/features/', feature_set_key, '.json'))))
FROM
(
    SELECT 'subject_fund_activity' AS feature_set_key, 1 AS feature_set_version,
           'organization' AS subject_type, subject_id, source_run_id,
           content_fingerprint, source_event_count,
           toJSONString(map('inflow_minor', toString(inflow_minor), 'outflow_minor', toString(outflow_minor), 'outflow_ratio', toString(outflow_ratio))) AS values_json
    FROM risk_warehouse.dws_subject_fund_activity_history_v2 FINAL
    WHERE source_run_id = 'risk-domain-demo-20261108-fund'
    UNION ALL
    SELECT 'merchant_acquiring_activity', 1, 'merchant', merchant_id, source_run_id, content_fingerprint, total_attempts,
           toJSONString(map('total_attempts', toString(total_attempts), 'success_rate', toString(success_rate), 'unique_payers', toString(unique_payers)))
    FROM risk_warehouse.dws_merchant_acquiring_activity_history_v1 FINAL
    WHERE source_run_id = 'risk-domain-demo-20261108-acquiring'
    UNION ALL
    SELECT 'subject_card_activity', 1, subject_type, subject_id, source_run_id, content_fingerprint, total_authorizations,
           toJSONString(map('total_authorizations', toString(total_authorizations), 'decline_rate', toString(decline_rate), 'cross_border_count', toString(cross_border_count)))
    FROM risk_warehouse.dws_subject_card_activity_history_v1 FINAL
    WHERE source_run_id = 'risk-domain-demo-20261108-card'
    UNION ALL
    SELECT 'subject_withdrawal_activity', 1, subject_type, subject_id, source_run_id, content_fingerprint, total_withdrawals,
           toJSONString(map('total_withdrawals', toString(total_withdrawals), 'failure_rate', toString(failure_rate), 'new_destination_count', toString(new_destination_count)))
    FROM risk_warehouse.dws_subject_withdrawal_activity_history_v1 FINAL
    WHERE source_run_id = 'risk-domain-demo-20261108-withdrawal'
    UNION ALL
    SELECT 'subject_security_activity', 1, subject_type, subject_id, source_run_id, content_fingerprint, total_events,
           toJSONString(map('failed_login_count', toString(failed_login_count), 'new_device_login_count', toString(new_device_login_count), 'high_risk_event_count', toString(high_risk_event_count)))
    FROM risk_warehouse.dws_subject_security_activity_history_v1 FINAL
    WHERE source_run_id = 'risk-domain-demo-20261108-security'
    UNION ALL
    SELECT 'subject_profile_risk', 1, subject_type, subject_id, source_run_id, content_fingerprint, total_events,
           toJSONString(map('verification_failed_count', toString(verification_failed_count), 'current_verification_status', current_verification_status, 'current_risk_level', current_risk_level))
    FROM risk_warehouse.dws_subject_profile_risk_history_v1 FINAL
    WHERE source_run_id = 'risk-domain-demo-20261108-profile'
    UNION ALL
    SELECT 'subject_relationship_risk', 1, subject_type, subject_id, source_run_id, content_fingerprint, active_relation_count,
           toJSONString(map('active_relation_count', toString(active_relation_count), 'high_risk_related_subject_count', toString(high_risk_related_subject_count)))
    FROM risk_warehouse.dws_subject_relationship_risk_history_v1 FINAL
    WHERE source_run_id = 'risk-domain-demo-20261108-relationship'
    UNION ALL
    SELECT 'provider_channel_quality', 1, 'provider', concat(provider_code, ':', channel_domain), source_run_id, content_fingerprint, total_operations,
           toJSONString(map('total_operations', toString(total_operations), 'success_rate', toString(success_rate), 'p95_latency_ms', toString(p95_latency_ms)))
    FROM risk_warehouse.dws_provider_channel_quality_history_v1 FINAL
    WHERE source_run_id = 'risk-domain-demo-20261108-provider'
);

OPTIMIZE TABLE risk_warehouse.dwd_fund_movements_v2 FINAL;
OPTIMIZE TABLE risk_warehouse.dwd_acquiring_payment_attempts_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dwd_card_authorizations_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dwd_withdrawal_orders_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dwd_account_security_events_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dwd_customer_verification_events_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dwd_subject_relationship_edges_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dwd_provider_channel_events_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.feature_snapshot_history_v2 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_subject_fund_activity_history_v2 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_subject_fund_activity_current_v2 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_merchant_acquiring_activity_history_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_merchant_acquiring_activity_current_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_subject_card_activity_history_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_subject_card_activity_current_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_subject_withdrawal_activity_history_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_subject_withdrawal_activity_current_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_subject_security_activity_history_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_subject_security_activity_current_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_subject_profile_risk_history_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_subject_profile_risk_current_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_subject_relationship_risk_history_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_subject_relationship_risk_current_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_provider_channel_quality_history_v1 FINAL;
OPTIMIZE TABLE risk_warehouse.dws_provider_channel_quality_current_v1 FINAL;
