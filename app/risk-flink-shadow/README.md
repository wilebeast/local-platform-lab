# Risk Flink Shadow

This module is the first streaming shadow implementation for
`subject_fund_activity_24h`.

It consumes contract-valid `funds.movement.recorded` events from Kafka, assigns
event time, advances a bounded-out-of-order watermark, maintains a keyed sliding
window, and publishes feature snapshots to a shadow topic. Events for which all
assigned windows have already expired are written to a separate late-event
topic.

The output is deliberately not connected to Strategy, Decision, or Action. It
must first pass stream-versus-batch reconciliation against the ClickHouse and
Airflow snapshot for the same subject, `as_of`, and feature-set version.

## Runtime defaults

- Input: `platform.raw.events.v1`
- Shadow output: `risk.shadow.feature.subject_fund_activity_24h.v1`
- Late output: `risk.shadow.late-events.v1`
- Window: 24 hours, sliding every minute
- Watermark: 120 seconds
- Allowed lateness: 300 seconds
- Checkpoint interval: 10 seconds

All values are configurable through the `RISK_SHADOW_*` environment variables
used in `RiskFlinkShadowJob`.

## Local verification

Run `mvn test package` with Java 17 and Maven 3.9, or build the multi-stage
`Dockerfile`. `Dockerfile.runtime` is only for a local loop that reuses an
already-built `target/risk-flink-shadow-1.0.0.jar`.
