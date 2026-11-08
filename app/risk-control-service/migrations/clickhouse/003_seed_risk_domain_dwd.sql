INSERT INTO risk_warehouse.dwd_fund_movements_v2
SELECT
    event_id,
    'funds.movement.recorded',
    1,
    'funds.movement.recorded',
    1,
    business_object_type,
    business_object_id,
    1,
    source_domain,
    'organization',
    'organization-risk-demo-001',
    direction,
    'USD',
    amount_minor,
    occurred_at_millis,
    occurred_at_millis + 1000,
    occurred_at_millis + 2000,
    1794096001000,
    concat('platform.', source_domain, '.events.v1'),
    0,
    source_offset,
    concat('risk-demo/raw/', event_id, '.json'),
    lower(hex(SHA256(event_id))),
    'confidential',
    concat('trace-', event_id)
FROM values(
    'event_id String, source_domain String, business_object_type String, business_object_id String, direction String, amount_minor Int64, occurred_at_millis Int64, source_offset Int64',
    ('evt-fund-acquiring-settlement-001', 'acquiring', 'merchant_settlement', 'merchant-settlement-demo-001', 'inflow', 120000, 1794024000000, 1),
    ('evt-fund-deposit-credit-001', 'inbound_funds', 'deposit_order', 'deposit-demo-001', 'inflow', 80000, 1794038400000, 2),
    ('evt-fund-card-settlement-001', 'card', 'card_settlement', 'card-settlement-demo-001', 'outflow', 25000, 1794052800000, 3),
    ('evt-fund-withdrawal-completed-001', 'withdrawal', 'withdrawal_order', 'withdrawal-demo-001', 'outflow', 150000, 1794067200000, 4)
);
INSERT INTO risk_warehouse.dwd_acquiring_payment_attempts_v1
SELECT
    event_id, 1, 'acquiring.payment.attempt', 1,
    'payment_attempt', attempt_id, 1, 'acquiring',
    'merchant-risk-demo-001', payment_order_id, attempt_id, payer_subject_id,
    provider_code, payment_method, 'USD', amount_minor, result_status, failure_code,
    occurred_at_millis, occurred_at_millis + 1000, 1794096001000
FROM values(
    'event_id String, payment_order_id String, attempt_id String, payer_subject_id String, provider_code String, payment_method String, amount_minor Int64, result_status String, failure_code String, occurred_at_millis Int64',
    ('evt-acquiring-attempt-001', 'payment-order-001', 'payment-attempt-001', 'payer-001', 'provider-demo-01', 'card', 10000, 'succeeded', '', 1794024000000),
    ('evt-acquiring-attempt-002', 'payment-order-002', 'payment-attempt-002', 'payer-002', 'provider-demo-01', 'bank_transfer', 20000, 'succeeded', '', 1794031200000),
    ('evt-acquiring-attempt-003', 'payment-order-003', 'payment-attempt-003', 'payer-003', 'provider-demo-01', 'card', 15000, 'failed', 'issuer_declined', 1794038400000),
    ('evt-acquiring-attempt-004', 'payment-order-004', 'payment-attempt-004', 'payer-004', 'provider-demo-01', 'card', 5000, 'failed', 'timeout', 1794052800000),
    ('evt-acquiring-attempt-005', 'payment-order-005', 'payment-attempt-005', 'payer-005', 'provider-demo-01', 'wallet', 50000, 'succeeded', '', 1794081600000)
);

INSERT INTO risk_warehouse.dwd_card_authorizations_v1
SELECT
    event_id, 1, 'card.authorization.decision', 1,
    'card_authorization', authorization_id, 1, 'card',
    'organization', 'organization-risk-demo-001', card_id, authorization_id,
    merchant_id, merchant_category, merchant_country, 'SG', 'USD', amount_minor,
    decision, decline_reason, occurred_at_millis, occurred_at_millis + 1000, 1794096001000
FROM values(
    'event_id String, card_id String, authorization_id String, merchant_id String, merchant_category String, merchant_country String, amount_minor Int64, decision String, decline_reason String, occurred_at_millis Int64',
    ('evt-card-authorization-001', 'card-demo-001', 'authorization-demo-001', 'merchant-card-001', 'software', 'SG', 5000, 'approved', '', 1794024000000),
    ('evt-card-authorization-002', 'card-demo-001', 'authorization-demo-002', 'merchant-card-002', 'travel', 'US', 8000, 'approved', '', 1794038400000),
    ('evt-card-authorization-003', 'card-demo-001', 'authorization-demo-003', 'merchant-card-003', 'cash_equivalent', 'GB', 12000, 'declined', 'restricted_mcc', 1794052800000),
    ('evt-card-authorization-004', 'card-demo-001', 'authorization-demo-004', 'merchant-card-004', 'office', 'SG', 5000, 'approved', '', 1794067200000),
    ('evt-card-authorization-005', 'card-demo-001', 'authorization-demo-005', 'merchant-card-002', 'travel', 'US', 5000, 'declined', 'velocity_limit', 1794081600000)
);

INSERT INTO risk_warehouse.dwd_withdrawal_orders_v1
SELECT
    event_id, 1, 'withdrawal.order.result', 1,
    'withdrawal_order', withdrawal_id, 1, 'withdrawal',
    'organization', 'organization-risk-demo-001', withdrawal_id,
    lower(hex(SHA256(destination_key))), destination_first_seen,
    'USD', amount_minor, status, failure_code,
    occurred_at_millis, occurred_at_millis + 1000, 1794096001000
FROM values(
    'event_id String, withdrawal_id String, destination_key String, destination_first_seen UInt8, amount_minor Int64, status String, failure_code String, occurred_at_millis Int64',
    ('evt-withdrawal-001', 'withdrawal-demo-001', 'destination-demo-001', 1, 80000, 'completed', '', 1794024000000),
    ('evt-withdrawal-002', 'withdrawal-demo-002', 'destination-demo-002', 0, 70000, 'completed', '', 1794038400000),
    ('evt-withdrawal-003', 'withdrawal-demo-003', 'destination-demo-003', 1, 30000, 'failed', 'beneficiary_rejected', 1794052800000),
    ('evt-withdrawal-004', 'withdrawal-demo-004', 'destination-demo-004', 0, 20000, 'pending_review', '', 1794081600000)
);

INSERT INTO risk_warehouse.dwd_account_security_events_v1
SELECT
    event_id, 1, 'account.security.event', 1,
    'account_security', concat('security-demo-', toString(row_number)), row_number, 'account_security',
    'organization', 'organization-risk-demo-001', security_event_type,
    lower(hex(SHA256(device_key))), ip_country, success, risk_signal,
    occurred_at_millis, occurred_at_millis + 1000, 1794096001000
FROM values(
    'event_id String, row_number UInt64, security_event_type String, device_key String, ip_country String, success UInt8, risk_signal String, occurred_at_millis Int64',
    ('evt-security-001', 1, 'login_failed', 'device-demo-001', 'SG', 0, 'medium', 1794024000000),
    ('evt-security-002', 2, 'login_failed', 'device-demo-001', 'SG', 0, 'medium', 1794027600000),
    ('evt-security-003', 3, 'login_failed', 'device-demo-002', 'RU', 0, 'high', 1794031200000),
    ('evt-security-004', 4, 'new_device_login', 'device-demo-002', 'RU', 1, 'high', 1794038400000),
    ('evt-security-005', 5, 'password_reset', 'device-demo-002', 'RU', 1, 'medium', 1794052800000),
    ('evt-security-006', 6, 'administrator_added', 'device-demo-002', 'RU', 1, 'high', 1794067200000)
);

INSERT INTO risk_warehouse.dwd_customer_verification_events_v1
SELECT
    event_id, 1, 'customer.verification.event', 1,
    'customer_verification', 'verification-demo-001', aggregate_version, 'customer_verification',
    'organization', 'organization-risk-demo-001', 'verification-demo-001',
    verification_event_type, verification_status, 'verification-provider-demo', 'SG',
    mismatch_field_count, changed_field_count, risk_level,
    occurred_at_millis, occurred_at_millis + 1000, 1794096001000
FROM values(
    'event_id String, aggregate_version UInt64, verification_event_type String, verification_status String, mismatch_field_count UInt32, changed_field_count UInt32, risk_level String, occurred_at_millis Int64',
    ('evt-verification-001', 1, 'verification_started', 'pending', 0, 0, 'low', 1794024000000),
    ('evt-verification-002', 2, 'verification_failed', 'rejected', 2, 0, 'high', 1794038400000),
    ('evt-verification-003', 3, 'material_changed', 'pending', 1, 3, 'medium', 1794052800000),
    ('evt-verification-004', 4, 'verification_completed', 'verified', 0, 0, 'low', 1794081600000)
);

INSERT INTO risk_warehouse.dwd_subject_relationship_edges_v1
SELECT
    event_id, 1, 'subject.relationship.edge', 1,
    'subject_relationship', concat('relationship-demo-', toString(aggregate_version)), aggregate_version, 'relationship',
    'organization', 'organization-risk-demo-001', 'organization', related_subject_id,
    relation_type, lower(hex(SHA256(relation_key))), related_subject_risk_level, active,
    occurred_at_millis, occurred_at_millis + 1000, 1794096001000
FROM values(
    'event_id String, aggregate_version UInt64, related_subject_id String, relation_type String, relation_key String, related_subject_risk_level String, active UInt8, occurred_at_millis Int64',
    ('evt-relationship-001', 1, 'organization-related-001', 'shared_device', 'device-demo-002', 'high', 1, 1794024000000),
    ('evt-relationship-002', 2, 'organization-related-002', 'shared_ip', 'ip-demo-001', 'medium', 1, 1794038400000),
    ('evt-relationship-003', 3, 'organization-related-003', 'shared_destination', 'destination-demo-001', 'high', 1, 1794052800000),
    ('evt-relationship-004', 4, 'organization-related-004', 'shared_device', 'device-retired-001', 'low', 0, 1794067200000)
);

INSERT INTO risk_warehouse.dwd_provider_channel_events_v1
SELECT
    event_id, 1, 'provider.channel.operation', 1,
    'channel_operation', operation_id, 1, channel_domain,
    provider_code, channel_domain, operation_id, result_status, latency_ms,
    duplicate_callback, reconciliation_mismatch,
    occurred_at_millis, occurred_at_millis + 1000, 1794096001000
FROM values(
    'event_id String, provider_code String, channel_domain String, operation_id String, result_status String, latency_ms UInt64, duplicate_callback UInt8, reconciliation_mismatch UInt8, occurred_at_millis Int64',
    ('evt-provider-001', 'provider-demo-01', 'acquiring', 'operation-demo-001', 'succeeded', 320, 0, 0, 1794024000000),
    ('evt-provider-002', 'provider-demo-01', 'acquiring', 'operation-demo-002', 'succeeded', 450, 0, 0, 1794031200000),
    ('evt-provider-003', 'provider-demo-01', 'acquiring', 'operation-demo-003', 'failed', 800, 0, 1, 1794038400000),
    ('evt-provider-004', 'provider-demo-01', 'acquiring', 'operation-demo-004', 'timeout', 3000, 1, 0, 1794052800000),
    ('evt-provider-005', 'provider-demo-02', 'card', 'operation-demo-005', 'succeeded', 210, 0, 0, 1794067200000),
    ('evt-provider-006', 'provider-demo-02', 'card', 'operation-demo-006', 'succeeded', 260, 0, 0, 1794074400000),
    ('evt-provider-007', 'provider-demo-02', 'card', 'operation-demo-007', 'failed', 900, 1, 1, 1794081600000)
);
