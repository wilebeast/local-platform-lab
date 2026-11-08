# Local Platform Lab

This glossary defines the business language used by the local platform exercises. It keeps card, funding, and ledger concepts distinct as the lab evolves.

## Funds and ledger

**Operational Ledger**:
The authoritative, real-time record of available, held, settled, and refunded value used by product decisions. It is distinct from a statutory general ledger used for financial reporting.
_Avoid_: General Ledger, Card Balance Table

**Ledger Book**:
An isolated accounting boundary in which every journal is balanced and every account belongs to the same legal entity context.
_Avoid_: Tenant Database, Organization Wallet

**Ledger Account**:
A single-currency bucket of value identified independently from cards and business documents. It has an owner, purpose, account type, and normal balance side.
_Avoid_: Card Account, Bank Account, Balance Row

**Funding Source**:
The ledger account from which a card may reserve and spend value, together with the binding policy and optional spending limit.
_Avoid_: Card Balance

**Journal**:
The immutable container that records one business event as a balanced set of postings in one currency.
_Avoid_: Transaction, Request, Ledger Entry

**Posting**:
One debit or credit line belonging to a journal and a ledger account.
_Avoid_: Payment, Event

**Balance Projection**:
A rebuildable query model derived from postings and maintained for real-time balance checks.
_Avoid_: Source of Truth

**Available Funds**:
Value that may be reserved by a new authorization under the funding source policy.
_Avoid_: Posted Balance

**Held Funds**:
Value reserved by an approved authorization and unavailable to other authorizations until settlement, reversal, or expiry.
_Avoid_: Settled Funds, Locked Row

**Reversal Journal**:
A new journal whose postings economically negate an earlier posted journal without modifying the earlier journal.
_Avoid_: Delete, Rollback, Edit

## Card lifecycle

**Card**:
A payment instrument that references a funding source; it does not itself define or own a ledger balance.
_Avoid_: Ledger Account, Wallet

**Authorization**:
A provider-originated decision request that may reserve funds against a card's effective funding source.
_Avoid_: Settlement, Charge

**Authorization Reversal**:
The release of funds previously held by an authorization before settlement.
_Avoid_: Refund

**Settlement**:
The conversion of an authorized hold into a final provider payable and a completed spend fact.
_Avoid_: Authorization, Provider Payment

**Refund**:
A return of value after settlement, represented by a new business fact and new journal.
_Avoid_: Reversal, Journal Rollback

## Acquiring

**Merchant**:
The business that accepts a customer payment and is owed the net amount after platform fees.
_Avoid_: Customer, Provider

**Payment Order**:
The merchant-facing commercial instruction to collect one amount in one currency. It owns the merchant order reference and idempotency boundary, but not a provider request.
_Avoid_: Payment Attempt, Provider Order

**Payment Attempt**:
One concrete effort to complete a payment order with a selected payment method and provider route. A payment order may have more than one attempt, but only one may produce the successful payment effect in this lab phase.
_Avoid_: Payment Order, Channel Operation

**Channel Operation**:
The durable execution record for a provider request, including request identity, attempts, outcome uncertainty, retry timing, and final provider result.
_Avoid_: Payment Attempt, HTTP Request

**Payment Success**:
The confirmed provider fact that one payment attempt collected the customer payment. It does not mean the provider has remitted cash to the platform or that the platform has paid the merchant.
_Avoid_: Channel Settlement, Merchant Payout

**Channel Settlement**:
The provider's remittance that converts a channel receivable into platform cash.
_Avoid_: Payment Success, Merchant Payout

**Channel Net Settlement**:
The provider-confirmed offset of an amount receivable from the provider against an amount refundable to the same provider, without pretending that cash moved twice.
_Avoid_: Cash Settlement, Refund, Payment Success

**Merchant Settlement**:
The calculation and crystallization of the amount owed to a merchant. It remains distinct from the later transfer of cash to the merchant.
_Avoid_: Merchant Payout, Channel Settlement

**Merchant Payout**:
The externally confirmed transfer of settled funds from platform cash to the merchant, which discharges the merchant-payable liability.
_Avoid_: Merchant Settlement, Payment Success, API Acceptance

**Reconciliation Run**:
An immutable comparison of provider statement facts with local business and ledger facts for one provider and time window.
_Avoid_: Ledger Repair, Balance Update

**Reconciliation Issue**:
A durable discrepancy discovered by reconciliation that requires evidence-based resolution; its existence does not authorize changing a journal.
_Avoid_: Automatic Compensation, Failed Payment

**Reconciliation Resolution**:
An immutable, evidence-referenced fact that explains why a reconciliation issue may be closed. Closing an issue does not itself change a payment, payout, journal, or balance.
_Avoid_: Ledger Repair, Journal Edit, Manual Balance Update

**Payout Correction**:
An evidence-backed correction used when a locally recorded merchant payout is proven not to have occurred externally. It posts a new reversal journal, marks the payout reversed, and preserves the original payout and journal as immutable history.
_Avoid_: Delete Payout, Edit Journal, Retry Unknown Transfer

## Risk control

**Evaluation**:
An immutable record of one policy execution against a frozen set of factor values and point-in-time context.
_Avoid_: Risk Request, Mutable Decision Row

**Decision**:
The synchronous answer that states whether the current business operation may continue: `pass` or `block`.
_Avoid_: Risk Level, Action, Business Status

**Disposition**:
The path that explains how a blocked operation may proceed, such as rejection, step-up verification, manual review, or deferral.
_Avoid_: Decision, Action

**Business Disposition**:
A typed business-state outcome that the authoritative business module must apply synchronously, such as keeping inbound funds in `pending_credit`.
_Avoid_: Asynchronous Action, Risk Level

**Action Intent**:
An immutable request for a separately executed side effect produced by an Evaluation.
_Avoid_: Decision, Business Disposition, Completed Action

**Finding**:
A durable risk observation that may require investigation; it is evidence, not permission to modify a business fact.
_Avoid_: Decision, Restriction, Case

**Restriction**:
A versioned, continuing limitation on one subject's capability, activated or released through audited actions.
_Avoid_: Block Decision, Finding

**Case**:
The operational container for human investigation, evidence, assignment, review, and resolution.
_Avoid_: Evaluation, Finding

**Feature Snapshot**:
An immutable, point-in-time set of warehouse-derived feature values identified by feature-set version, subject, `as_of`, and source run.
_Avoid_: Factor Value, Current Projection

**Feature Window Type**:
A stable, named definition of how a Feature Snapshot selects time, such as a rolling five-minute window, rolling twenty-four-hour window, calendar day, or lifetime window.
_Avoid_: Window Start, Table Name, Feature Set

**Feature Window Instance**:
The concrete interval selected by one Feature Window Type at one `as_of`, recorded with its actual window start and window end for validation and replay.
_Avoid_: Feature Window Type, Computation Time

**Factor Value**:
A typed input value frozen for one Evaluation, regardless of whether it originated from request context, an RPC, state, a Feature Snapshot, an expression, or a model.
_Avoid_: Feature Definition, Raw Fact

**Source Domain**:
The business domain that owns the fact from which an event originated, such as acquiring, card, withdrawal, security, or customer verification.
_Avoid_: Tenant, Service Name

**DWD Fact**:
A normalized, event-grained business fact whose identity, time meaning, and lifecycle are stable across its source systems.
_Avoid_: Raw Event, Subject Summary, Universal Event Row

**DWS Summary**:
A point-in-time analytical summary grouped by subject, risk theme, window, and row grain, from which one or more Features may be produced.
_Avoid_: Raw Fact, Factor Value, One Table Per Factor
