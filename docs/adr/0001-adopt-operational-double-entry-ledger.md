---
status: accepted
---

# Adopt an independent operational double-entry ledger

The enterprise-card lab will stop treating a card row as a funds account and will introduce an independent operational ledger. Cards will bind to shared or dedicated funding sources, authorizations will move value between available and held liability accounts, posted journals will be immutable and single-currency, and existing balances will enter the new ledger through balanced opening-balance journals. This preserves real-time product controls while leaving statutory accounting and cross-currency accounting outside the first phase.

## Considered options

We rejected one-account-per-card because it fragments organizational funds, shared-account-only because it cannot isolate special budgets, authorization-only holds because it splits the authoritative audit trail, and replaying the old single-sided history because it does not contain enough facts to reconstruct all balanced postings.

## Consequences

The implementation must introduce ledger books, accounts, journals, postings, balance projections, and card funding-source bindings. New funds mutations must go through a controlled posting transaction; old card balance and single-sided entry tables remain read-only migration evidence until removal is separately approved.
