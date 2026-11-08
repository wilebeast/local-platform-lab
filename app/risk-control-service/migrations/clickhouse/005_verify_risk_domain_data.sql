SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dwd_fund_movements_v2 FINAL) != 4,
    'expected 4 fund movement DWD rows'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dwd_acquiring_payment_attempts_v1 FINAL) != 5,
    'expected 5 acquiring DWD rows'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dwd_card_authorizations_v1 FINAL) != 5,
    'expected 5 card authorization DWD rows'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dwd_withdrawal_orders_v1 FINAL) != 4,
    'expected 4 withdrawal DWD rows'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dwd_account_security_events_v1 FINAL) != 6,
    'expected 6 account security DWD rows'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dwd_customer_verification_events_v1 FINAL) != 4,
    'expected 4 customer verification DWD rows'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dwd_subject_relationship_edges_v1 FINAL) != 4,
    'expected 4 relationship DWD rows'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dwd_provider_channel_events_v1 FINAL) != 7,
    'expected 7 provider channel DWD rows'
);

SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dws_subject_fund_activity_history_v2 FINAL WHERE source_run_id = 'risk-domain-demo-20261108-fund') != 1,
    'expected 1 fund DWS history row'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dws_merchant_acquiring_activity_history_v1 FINAL WHERE source_run_id = 'risk-domain-demo-20261108-acquiring') != 1,
    'expected 1 acquiring DWS history row'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dws_subject_card_activity_history_v1 FINAL WHERE source_run_id = 'risk-domain-demo-20261108-card') != 1,
    'expected 1 card DWS history row'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dws_subject_withdrawal_activity_history_v1 FINAL WHERE source_run_id = 'risk-domain-demo-20261108-withdrawal') != 1,
    'expected 1 withdrawal DWS history row'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dws_subject_security_activity_history_v1 FINAL WHERE source_run_id = 'risk-domain-demo-20261108-security') != 1,
    'expected 1 security DWS history row'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dws_subject_profile_risk_history_v1 FINAL WHERE source_run_id = 'risk-domain-demo-20261108-profile') != 1,
    'expected 1 profile DWS history row'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dws_subject_relationship_risk_history_v1 FINAL WHERE source_run_id = 'risk-domain-demo-20261108-relationship') != 1,
    'expected 1 relationship DWS history row'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.dws_provider_channel_quality_history_v1 FINAL WHERE source_run_id = 'risk-domain-demo-20261108-provider') != 2,
    'expected 2 provider DWS history rows'
);
SELECT throwIf(
    (SELECT count() FROM risk_warehouse.feature_snapshot_history_v2 FINAL WHERE source_run_id LIKE 'risk-domain-demo-20261108-%') != 9,
    'expected 9 generic feature manifest rows'
);

SELECT throwIf(
    (SELECT inflow_minor FROM risk_warehouse.dws_subject_fund_activity_current_v2 FINAL WHERE subject_id = 'organization-risk-demo-001') != 200000,
    'expected fund inflow 200000'
);
SELECT throwIf(
    (SELECT outflow_minor FROM risk_warehouse.dws_subject_fund_activity_current_v2 FINAL WHERE subject_id = 'organization-risk-demo-001') != 175000,
    'expected fund outflow 175000'
);
SELECT throwIf(
    abs((SELECT outflow_ratio FROM risk_warehouse.dws_subject_fund_activity_current_v2 FINAL WHERE subject_id = 'organization-risk-demo-001') - 0.875) > 0.000001,
    'expected fund outflow ratio 0.875'
);
SELECT throwIf(
    abs((SELECT success_rate FROM risk_warehouse.dws_merchant_acquiring_activity_current_v1 FINAL WHERE merchant_id = 'merchant-risk-demo-001') - 0.6) > 0.000001,
    'expected acquiring success rate 0.6'
);
SELECT throwIf(
    (SELECT high_risk_event_count FROM risk_warehouse.dws_subject_security_activity_current_v1 FINAL WHERE subject_id = 'organization-risk-demo-001') != 3,
    'expected 3 high-risk security events'
);
SELECT throwIf(
    (SELECT current_verification_status FROM risk_warehouse.dws_subject_profile_risk_current_v1 FINAL WHERE subject_id = 'organization-risk-demo-001') != 'verified',
    'expected current verification status verified'
);
SELECT throwIf(
    (SELECT high_risk_related_subject_count FROM risk_warehouse.dws_subject_relationship_risk_current_v1 FINAL WHERE subject_id = 'organization-risk-demo-001') != 2,
    'expected 2 high-risk related subjects'
);
SELECT throwIf(
    (SELECT count() FROM system.columns WHERE database = 'risk_warehouse' AND name = 'tenant_id' AND (table LIKE 'dwd_%' OR table LIKE 'dws_%')) != 0,
    'tenant_id must not exist in the single-platform DWD/DWS model'
);

SELECT *
FROM
(
    SELECT
        'fund' AS risk_theme,
        toString(inflow_minor) AS metric_1,
        toString(outflow_minor) AS metric_2,
        toString(round(outflow_ratio, 4)) AS metric_3
    FROM risk_warehouse.dws_subject_fund_activity_current_v2 FINAL
    UNION ALL
    SELECT 'acquiring', toString(total_attempts), toString(succeeded_attempts), toString(round(success_rate, 4))
    FROM risk_warehouse.dws_merchant_acquiring_activity_current_v1 FINAL
    UNION ALL
    SELECT 'card', toString(total_authorizations), toString(declined_authorizations), toString(round(decline_rate, 4))
    FROM risk_warehouse.dws_subject_card_activity_current_v1 FINAL
    UNION ALL
    SELECT 'withdrawal', toString(total_withdrawals), toString(new_destination_count), toString(round(failure_rate, 4))
    FROM risk_warehouse.dws_subject_withdrawal_activity_current_v1 FINAL
    UNION ALL
    SELECT 'security', toString(total_events), toString(failed_login_count), toString(high_risk_event_count)
    FROM risk_warehouse.dws_subject_security_activity_current_v1 FINAL
    UNION ALL
    SELECT 'profile', toString(total_events), current_verification_status, current_risk_level
    FROM risk_warehouse.dws_subject_profile_risk_current_v1 FINAL
    UNION ALL
    SELECT 'relationship', toString(active_relation_count), toString(high_risk_related_subject_count), toString(shared_device_count)
    FROM risk_warehouse.dws_subject_relationship_risk_current_v1 FINAL
    UNION ALL
    SELECT concat('provider:', provider_code, ':', channel_domain), toString(total_operations), toString(succeeded_operations), toString(round(success_rate, 4))
    FROM risk_warehouse.dws_provider_channel_quality_current_v1 FINAL
)
ORDER BY risk_theme;
