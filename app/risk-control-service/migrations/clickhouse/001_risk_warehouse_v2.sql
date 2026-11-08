CREATE DATABASE IF NOT EXISTS risk_warehouse;

CREATE TABLE IF NOT EXISTS risk_warehouse.dwd_fund_movements_v2
(
    event_id String,
    event_type LowCardinality(String),
    event_version UInt32,
    schema_id String,
    schema_version UInt32,
    aggregate_type LowCardinality(String),
    aggregate_id String,
    aggregate_version UInt64,
    source_domain LowCardinality(String),
    subject_type LowCardinality(String),
    subject_id String,
    direction Enum8('inflow' = 1, 'outflow' = 2),
    currency FixedString(3),
    amount_minor Int64,
    occurred_at_millis Int64,
    observed_at_millis Int64,
    produced_at_millis Int64,
    ingested_at_millis Int64,
    source_topic String,
    source_partition Int32,
    source_offset Int64,
    source_object_key String,
    source_sha256 FixedString(64),
    data_classification LowCardinality(String),
    trace_id String
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(occurred_at_millis / 1000, 'UTC'))
ORDER BY event_id;

CREATE TABLE IF NOT EXISTS risk_warehouse.feature_snapshot_history_v2
(
    feature_set_key LowCardinality(String),
    feature_set_version UInt32,
    subject_type LowCardinality(String),
    subject_id String,
    entity_key String,
    as_of_millis Int64,
    window_start_millis Int64,
    window_end_millis Int64,
    input_cutoff_millis Int64,
    source_run_id String,
    content_fingerprint FixedString(64),
    quality_status Enum8('unknown' = 0, 'passed' = 1, 'failed' = 2),
    values_json String,
    source_event_count UInt64,
    computed_at_millis Int64,
    ingested_at_millis Int64,
    source_object_key String,
    source_sha256 FixedString(64)
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (
    feature_set_key,
    feature_set_version,
    subject_type,
    subject_id,
    entity_key,
    as_of_millis,
    source_run_id
);
CREATE TABLE IF NOT EXISTS risk_warehouse.dws_subject_fund_activity_history_v2
(
    feature_set_key LowCardinality(String),
    feature_set_version UInt32,
    window_type LowCardinality(String),
    subject_type LowCardinality(String),
    subject_id String,
    currency FixedString(3),
    window_start_millis Int64,
    window_end_millis Int64,
    as_of_millis Int64,
    input_cutoff_millis Int64,
    inflow_minor Int64,
    outflow_minor Int64,
    outflow_ratio Float64,
    outflow_ratio_defined UInt8,
    source_event_count UInt64,
    source_run_id String,
    content_fingerprint FixedString(64),
    quality_status Enum8('unknown' = 0, 'passed' = 1, 'failed' = 2),
    computed_at_millis Int64,
    ingested_at_millis Int64,
    source_object_key String,
    source_sha256 FixedString(64)
)
ENGINE = ReplacingMergeTree(ingested_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (
    feature_set_key,
    feature_set_version,
    window_type,
    subject_type,
    subject_id,
    currency,
    as_of_millis,
    source_run_id
);

CREATE TABLE IF NOT EXISTS risk_warehouse.dws_subject_fund_activity_current_v2
(
    feature_set_key LowCardinality(String),
    feature_set_version UInt32,
    window_type LowCardinality(String),
    subject_type LowCardinality(String),
    subject_id String,
    currency FixedString(3),
    window_start_millis Int64,
    window_end_millis Int64,
    as_of_millis Int64,
    input_cutoff_millis Int64,
    inflow_minor Int64,
    outflow_minor Int64,
    outflow_ratio Float64,
    outflow_ratio_defined UInt8,
    source_event_count UInt64,
    source_run_id String,
    content_fingerprint FixedString(64),
    quality_status Enum8('unknown' = 0, 'passed' = 1, 'failed' = 2),
    computed_at_millis Int64,
    ingested_at_millis Int64
)
ENGINE = ReplacingMergeTree(computed_at_millis)
PARTITION BY toYYYYMM(toDateTime(as_of_millis / 1000, 'UTC'))
ORDER BY (
    feature_set_key,
    feature_set_version,
    window_type,
    subject_type,
    subject_id,
    currency,
    as_of_millis
);
