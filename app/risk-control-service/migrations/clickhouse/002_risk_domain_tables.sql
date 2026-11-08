CREATE DATABASE IF NOT EXISTS risk_warehouse;

CREATE TABLE IF NOT EXISTS risk_warehouse.dwd_acquiring_payment_attempts_v1
(
    event_id String,
    event_version UInt32,
    schema_id String,
    schema_version UInt32,
    aggregate_type LowCardinality(String),
    aggregate_id String,
    aggregate_version UInt64,
    source_domain LowCardinality(String),
    merchant_id String,
    payment_order_id String,
    attempt_id String,
    payer_subject_id String,
    provider_code LowCardinality(String),
    payment_method LowCardinality(String),
    currency FixedString(3),
    amount_minor Int64,
    result_status LowCardinality(String),
    failure_code LowCardinality(String),
    occurred_at_millis Int64,
    observed_at_millis Int64,
    ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(occurred_at_millis / 1000, 'UTC'))
ORDER BY event_id;

CREATE TABLE IF NOT EXISTS risk_warehouse.dwd_card_authorizations_v1
(
    event_id String,
    event_version UInt32,
    schema_id String,
    schema_version UInt32,
    aggregate_type LowCardinality(String),
    aggregate_id String,
    aggregate_version UInt64,
    source_domain LowCardinality(String),
    subject_type LowCardinality(String),
    subject_id String,
    card_id String,
    authorization_id String,
    merchant_id String,
    merchant_category LowCardinality(String),
    merchant_country FixedString(2),
    home_country FixedString(2),
    currency FixedString(3),
    amount_minor Int64,
    decision LowCardinality(String),
    decline_reason LowCardinality(String),
    occurred_at_millis Int64,
    observed_at_millis Int64,
    ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(occurred_at_millis / 1000, 'UTC'))
ORDER BY event_id;

CREATE TABLE IF NOT EXISTS risk_warehouse.dwd_withdrawal_orders_v1
(
    event_id String,
    event_version UInt32,
    schema_id String,
    schema_version UInt32,
    aggregate_type LowCardinality(String),
    aggregate_id String,
    aggregate_version UInt64,
    source_domain LowCardinality(String),
    subject_type LowCardinality(String),
    subject_id String,
    withdrawal_id String,
    destination_fingerprint FixedString(64),
    destination_first_seen UInt8,
    currency FixedString(3),
    amount_minor Int64,
    status LowCardinality(String),
    failure_code LowCardinality(String),
    occurred_at_millis Int64,
    observed_at_millis Int64,
    ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(occurred_at_millis / 1000, 'UTC'))
ORDER BY event_id;

CREATE TABLE IF NOT EXISTS risk_warehouse.dwd_account_security_events_v1
(
    event_id String,
    event_version UInt32,
    schema_id String,
    schema_version UInt32,
    aggregate_type LowCardinality(String),
    aggregate_id String,
    aggregate_version UInt64,
    source_domain LowCardinality(String),
    subject_type LowCardinality(String),
    subject_id String,
    security_event_type LowCardinality(String),
    device_fingerprint FixedString(64),
    ip_country FixedString(2),
    success UInt8,
    risk_signal LowCardinality(String),
    occurred_at_millis Int64,
    observed_at_millis Int64,
    ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(occurred_at_millis / 1000, 'UTC'))
ORDER BY event_id;

CREATE TABLE IF NOT EXISTS risk_warehouse.dwd_customer_verification_events_v1
(
    event_id String,
    event_version UInt32,
    schema_id String,
    schema_version UInt32,
    aggregate_type LowCardinality(String),
    aggregate_id String,
    aggregate_version UInt64,
    source_domain LowCardinality(String),
    subject_type LowCardinality(String),
    subject_id String,
    verification_id String,
    verification_event_type LowCardinality(String),
    verification_status LowCardinality(String),
    provider_code LowCardinality(String),
    country FixedString(2),
    mismatch_field_count UInt32,
    changed_field_count UInt32,
    risk_level LowCardinality(String),
    occurred_at_millis Int64,
    observed_at_millis Int64,
    ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(occurred_at_millis / 1000, 'UTC'))
ORDER BY event_id;

CREATE TABLE IF NOT EXISTS risk_warehouse.dwd_subject_relationship_edges_v1
(
    event_id String,
    event_version UInt32,
    schema_id String,
    schema_version UInt32,
    aggregate_type LowCardinality(String),
    aggregate_id String,
    aggregate_version UInt64,
    source_domain LowCardinality(String),
    subject_type LowCardinality(String),
    subject_id String,
    related_subject_type LowCardinality(String),
    related_subject_id String,
    relation_type LowCardinality(String),
    relation_key_fingerprint FixedString(64),
    related_subject_risk_level LowCardinality(String),
    active UInt8,
    occurred_at_millis Int64,
    observed_at_millis Int64,
    ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(occurred_at_millis / 1000, 'UTC'))
ORDER BY event_id;

CREATE TABLE IF NOT EXISTS risk_warehouse.dwd_provider_channel_events_v1
(
    event_id String,
    event_version UInt32,
    schema_id String,
    schema_version UInt32,
    aggregate_type LowCardinality(String),
    aggregate_id String,
    aggregate_version UInt64,
    source_domain LowCardinality(String),
    provider_code LowCardinality(String),
    channel_domain LowCardinality(String),
    operation_id String,
    result_status LowCardinality(String),
    latency_ms UInt64,
    duplicate_callback UInt8,
    reconciliation_mismatch UInt8,
    occurred_at_millis Int64,
    observed_at_millis Int64,
    ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(occurred_at_millis / 1000, 'UTC'))
ORDER BY event_id;

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_merchant_acquiring_activity_history_v1
(
    feature_set_key LowCardinality(String), feature_set_version UInt32, window_type LowCardinality(String),
    merchant_id String, currency FixedString(3), window_start_millis Int64, window_end_millis Int64, as_of_millis Int64,
    total_attempts UInt64, succeeded_attempts UInt64, failed_attempts UInt64,
    attempted_amount_minor Int64, succeeded_amount_minor Int64, success_rate Float64, unique_payers UInt64,
    source_run_id String, content_fingerprint FixedString(64), computed_at_millis Int64, ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, window_type, merchant_id, currency, as_of_millis, source_run_id);

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_merchant_acquiring_activity_current_v1
AS risk_warehouse.dws_merchant_acquiring_activity_history_v1
ENGINE = ReplacingMergeTree(computed_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, window_type, merchant_id, currency, as_of_millis);

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_subject_card_activity_history_v1
(
    feature_set_key LowCardinality(String), feature_set_version UInt32, window_type LowCardinality(String),
    subject_type LowCardinality(String), subject_id String, currency FixedString(3),
    window_start_millis Int64, window_end_millis Int64, as_of_millis Int64,
    total_authorizations UInt64, approved_authorizations UInt64, declined_authorizations UInt64,
    requested_amount_minor Int64, approved_amount_minor Int64, decline_rate Float64,
    cross_border_count UInt64, unique_merchants UInt64,
    source_run_id String, content_fingerprint FixedString(64), computed_at_millis Int64, ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, window_type, subject_type, subject_id, currency, as_of_millis, source_run_id);

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_subject_card_activity_current_v1
AS risk_warehouse.dws_subject_card_activity_history_v1
ENGINE = ReplacingMergeTree(computed_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, window_type, subject_type, subject_id, currency, as_of_millis);

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_subject_withdrawal_activity_history_v1
(
    feature_set_key LowCardinality(String), feature_set_version UInt32, window_type LowCardinality(String),
    subject_type LowCardinality(String), subject_id String, currency FixedString(3),
    window_start_millis Int64, window_end_millis Int64, as_of_millis Int64,
    total_withdrawals UInt64, completed_withdrawals UInt64, failed_withdrawals UInt64, pending_withdrawals UInt64,
    requested_amount_minor Int64, completed_amount_minor Int64, new_destination_count UInt64, failure_rate Float64,
    source_run_id String, content_fingerprint FixedString(64), computed_at_millis Int64, ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, window_type, subject_type, subject_id, currency, as_of_millis, source_run_id);

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_subject_withdrawal_activity_current_v1
AS risk_warehouse.dws_subject_withdrawal_activity_history_v1
ENGINE = ReplacingMergeTree(computed_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, window_type, subject_type, subject_id, currency, as_of_millis);

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_subject_security_activity_history_v1
(
    feature_set_key LowCardinality(String), feature_set_version UInt32, window_type LowCardinality(String),
    subject_type LowCardinality(String), subject_id String,
    window_start_millis Int64, window_end_millis Int64, as_of_millis Int64,
    total_events UInt64, failed_login_count UInt64, new_device_login_count UInt64,
    password_reset_count UInt64, two_factor_change_count UInt64, administrator_add_count UInt64, high_risk_event_count UInt64,
    source_run_id String, content_fingerprint FixedString(64), computed_at_millis Int64, ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, window_type, subject_type, subject_id, as_of_millis, source_run_id);

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_subject_security_activity_current_v1
AS risk_warehouse.dws_subject_security_activity_history_v1
ENGINE = ReplacingMergeTree(computed_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, window_type, subject_type, subject_id, as_of_millis);

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_subject_profile_risk_history_v1
(
    feature_set_key LowCardinality(String), feature_set_version UInt32, window_type LowCardinality(String),
    subject_type LowCardinality(String), subject_id String,
    window_start_millis Int64, window_end_millis Int64, as_of_millis Int64,
    total_events UInt64, verification_failed_count UInt64, material_change_count UInt64,
    mismatch_field_count UInt64, changed_field_count UInt64,
    current_verification_status LowCardinality(String), current_risk_level LowCardinality(String),
    source_run_id String, content_fingerprint FixedString(64), computed_at_millis Int64, ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, window_type, subject_type, subject_id, as_of_millis, source_run_id);

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_subject_profile_risk_current_v1
AS risk_warehouse.dws_subject_profile_risk_history_v1
ENGINE = ReplacingMergeTree(computed_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, window_type, subject_type, subject_id, as_of_millis);

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_subject_relationship_risk_history_v1
(
    feature_set_key LowCardinality(String), feature_set_version UInt32, window_type LowCardinality(String),
    subject_type LowCardinality(String), subject_id String,
    window_start_millis Int64, window_end_millis Int64, as_of_millis Int64,
    active_relation_count UInt64, shared_device_count UInt64, shared_ip_count UInt64,
    shared_destination_count UInt64, high_risk_related_subject_count UInt64,
    source_run_id String, content_fingerprint FixedString(64), computed_at_millis Int64, ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, window_type, subject_type, subject_id, as_of_millis, source_run_id);

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_subject_relationship_risk_current_v1
AS risk_warehouse.dws_subject_relationship_risk_history_v1
ENGINE = ReplacingMergeTree(computed_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, window_type, subject_type, subject_id, as_of_millis);

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_provider_channel_quality_history_v1
(
    feature_set_key LowCardinality(String), feature_set_version UInt32, window_type LowCardinality(String),
    provider_code LowCardinality(String), channel_domain LowCardinality(String),
    window_start_millis Int64, window_end_millis Int64, as_of_millis Int64,
    total_operations UInt64, succeeded_operations UInt64, failed_operations UInt64, timeout_operations UInt64,
    success_rate Float64, average_latency_ms Float64, p95_latency_ms Float64,
    duplicate_callback_count UInt64, reconciliation_mismatch_count UInt64,
    source_run_id String, content_fingerprint FixedString(64), computed_at_millis Int64, ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, window_type, provider_code, channel_domain, as_of_millis, source_run_id);

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_provider_channel_quality_current_v1
AS risk_warehouse.dws_provider_channel_quality_history_v1
ENGINE = ReplacingMergeTree(computed_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (feature_set_key, feature_set_version, window_type, provider_code, channel_domain, as_of_millis);
