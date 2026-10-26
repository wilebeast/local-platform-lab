# Acquiring System Lab Design

## Scope

This lab implements one USD merchant, two simulated provider routes, asynchronous provider confirmation, a PostgreSQL Inbox, fixed fees, balanced operational-ledger journals, full refund before merchant payout, and deterministic idempotency.

It deliberately excludes real card data, PCI scope, partial capture/refund, chargebacks, FX, split settlement, and refunds after merchant payout.

## Facts and owners

| Fact | Authority | Completion point |
| --- | --- | --- |
| Merchant payment intent | `payment_orders` | order transaction commits |
| Concrete route attempt | `payment_attempts` | attempt is created |
| Provider execution | `payment_channel_operations` | provider outcome is confirmed |
| Raw provider notification | `payment_provider_inbox` | verified raw body commits |
| Successful collection | payment worker | order success and capture journal commit together |
| Money movement | operational ledger | balanced journal is posted |
| Refund | `payment_refunds` | refund fact and reversal journal commit together |
| Channel net settlement | `acquiring_channel_settlements` | settlement fact and balanced netting journal commit together |
| Channel cash settlement | `acquiring_channel_cash_settlements` | confirmed remittance fact and cash journal commit together |
| Merchant payout | `acquiring_merchant_payouts` | confirmed transfer fact, payout journal, and `paid_out` order state commit together |
| Provider reconciliation | `acquiring_reconciliation_runs` and `acquiring_reconciliation_issues` | immutable statement comparison commits without changing financial facts |
| Reconciliation resolution | `acquiring_reconciliation_resolutions` | evidence reference and issue transition to `resolved` commit together without changing financial facts |
| Missing-payout correction | `acquiring_reconciliation_resolutions`, payout state, and operational ledger | evidence, reversal journal, payout reversal, payment recovery, and issue closure commit atomically |

## State boundaries

```text
PaymentOrder: created -> processing -> succeeded -> refunded
PaymentAttempt: created -> provider_pending -> succeeded | failed
ChannelOperation: pending -> executing -> unknown | succeeded | failed
ProviderInbox: pending -> processing -> processed | dead_letter
```

Provider acceptance is not payment success. Payment success is not channel settlement, merchant settlement, or merchant payout.

## Accounting matrix

For a USD 100.00 payment with a USD 2.00 platform fee:

### Provider confirms successful collection

```text
Debit   channel_receivable   100.00
Credit  merchant_payable      98.00
Credit  fee_revenue            2.00
```

### Provider remits cash

```text
Debit   platform_cash        100.00
Credit  channel_receivable   100.00
```

Cash settlement and net settlement are mutually exclusive ways to discharge the same provider receivable. Once cash remittance has been recorded, a later refund creates a provider refund payable that must be paid or reconciled separately; it must not reuse the already-consumed receivable for netting.

### Platform pays the merchant

```text
Debit   merchant_payable      98.00
Credit  platform_cash         98.00
```

### Provider proves the recorded payout was never sent

The original payout journal remains posted and immutable. A new journal reverses its economic effect:

```text
Debit   platform_cash         98.00
Credit  merchant_payable      98.00
```

The payout becomes `reversed`, the payment returns from `paid_out` to `succeeded`, and a later payout must use a new idempotency key. The correction is not a resend: a provider timeout or unknown result must first be queried and reconciled.

In the lab, the payout API simulates an already-confirmed external transfer. Production must persist an outbound operation before sending, retain one provider request ID while the outcome is unknown, query before retrying, and post the payout journal only after final provider confirmation.

### Full refund before merchant payout

```text
Debit   merchant_payable      98.00
Debit   fee_revenue            2.00
Credit  provider_refund_payable 100.00
```

### Provider confirms net settlement after refund

```text
Debit   provider_refund_payable 100.00
Credit  channel_receivable      100.00
```

This is a netted provider fact: the platform does not record fictitious incoming and outgoing cash. A non-netted provider remittance must instead use explicit platform-cash journals.

Every journal is single-currency, immutable, and balanced. A business event maps to at most one posted journal.

## Public test seams

1. Create-payment API: exact replay returns the original order; changed reuse conflicts.
2. Provider-event API: forged input leaves no Inbox row; duplicate verified input creates one row.
3. Worker: confirmed success atomically changes business state and posts one balanced capture journal.
4. Refund API: exact replay produces one refund and one balanced journal; invalid state and changed reuse fail without financial effect.
5. Channel-net-settlement API: only refunded payments qualify; exact replay returns the original result; conflicting reuse fails; provider receivable and refund payable both become zero.
6. Channel-cash-settlement API: only successful, unrefunded payments qualify; exact replay returns the original result; provider receivable becomes platform cash exactly once; the payment cannot later enter the net-settlement path.
7. Merchant-payout API: only cash-settled payments qualify; exact replay returns the original result; conflicting reuse and premature payout fail; payout and later full refund are mutually exclusive in this phase.
8. Payout-reconciliation API: compares one provider statement and bounded period with local payouts; exact replay is stable, changed replay conflicts, missing provider facts create open issues, and reconciliation never mutates journals or balances.
9. Reconciliation-resolution API: only an open issue can be closed; exact replay returns the original resolution, changed replay conflicts, evidence is retained, and payment, payout, journals, and balances remain unchanged.
10. Missing-payout-correction API: only an open `missing_provider_payout` issue for a paid payout qualifies; it creates exactly one reversal journal linked through `reversal_of`, never edits the original journal, restores the pre-payout balances, and permits a later payout with a new business identity.

## Reconciliation boundary

```text
Provider statement lines ─┐
                          ├─ compare → matched facts
Local payout facts ───────┘          → reconciliation issues

reconciliation issue ≠ permission to edit ledger
```

Resolution requires external evidence. A genuine local accounting error is corrected through a new adjustment or reversal journal; a late provider statement may resolve the issue without any financial posting.

The lab's `provider_confirmed_late` resolution represents evidence that the provider eventually reported the already-completed payout. The API stores the evidence reference and closes the operational issue in one transaction. It does not treat the caller's assertion as a money-moving authority. In production, the reference must point to authenticated provider data or an approved evidence record, and the action must be protected by RBAC, audit, and—where policy requires—four-eyes approval.

The `local_payout_not_sent` path has a financial effect and therefore has a stricter completion point. The resolution row, reversal journal, payout state, payment state, and issue state commit together. The caller cannot supply account IDs or posting directions; the service derives the reversal from the posted payout journal. Production must additionally bind the evidence reference to an authenticated provider statement or query result and require privileged approval before execution.

## Recovery rules

- Persist a channel operation before calling a provider.
- Keep the same provider request ID while an outcome is unknown; query before resend.
- Claim Inbox and operation work using row locks with `SKIP LOCKED`.
- Commit order success, attempt success, Inbox completion, and the capture journal in one transaction.
- Reject stale or duplicate success after one attempt has already completed the order.
- Reconcile business facts against journals and ledger balances; projections are not the source of truth.
