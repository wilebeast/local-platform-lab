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
