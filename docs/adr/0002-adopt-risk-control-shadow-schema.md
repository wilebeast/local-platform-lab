---
status: accepted
date: 2026-11-08
---

# Introduce the V2 risk data plane in an isolated PostgreSQL schema

The risk-control lab will introduce the new transactional write model under the PostgreSQL schema `risk_control` while preserving the existing `public.risk_*` tables as read-only migration evidence. ClickHouse tables will use explicit V2 names during the same shadow period. New writes will be introduced behind a shadow writer, reconciled against existing behavior, and cut over use case by use case.

## Considered options

We rejected rewriting the existing tables in place because they were created by several experiments and contain incompatible decision enums, overlapping Evaluation tables, nullable lifecycle timestamps, and Action states that do not represent unknown outcomes. We rejected adding only more columns to the old tables because that would keep ambiguous ownership and make rollback dependent on partially migrated rows. We also rejected creating a separate database for the first stage because it adds credentials, backup, connection-pool, and cross-database operational complexity before workload isolation is needed.

## Consequences

The first migration is additive and does not change current runtime behavior. The platform currently has one business isolation boundary, so V2 tables do not introduce `tenant_id`; business provenance is represented by typed facts and `source_domain`, while transactional records retain explicit environment, point-in-time, idempotency, lifecycle, lease, lineage, and audit fields. Existing code continues to use the old tables until a use-case-specific shadow writer, reconciliation report, and cutover gate pass. Rollback disables the V2 writer; it does not delete V2 evidence or attempt to reverse already executed business effects.
