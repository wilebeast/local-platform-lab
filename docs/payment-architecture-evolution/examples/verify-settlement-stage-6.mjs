// Read-only teaching model: no database, network, durable worker or real payment.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const fixture = JSON.parse(readFileSync(new URL('./settlement-stage-6-scenario.json', import.meta.url), 'utf8'));
const canonical = (value) => JSON.stringify(normalize(value));
function normalize(value) {
  if (Array.isArray(value)) return value.map(normalize);
  if (value && typeof value === 'object') {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, normalize(value[key])]));
  }
  return value;
}
function amount(value, allowZero = false) {
  assert(Number.isSafeInteger(value) && value >= (allowZero ? 0 : 1), 'INVALID_AMOUNT');
  return value;
}
function add(left, right) {
  const result = left + right;
  assert(Number.isSafeInteger(result), 'AMOUNT_OVERFLOW');
  return result;
}
const sum = (values) => values.reduce(add, 0);
function feeFor(basis, rateBPS) {
  amount(basis); amount(rateBPS, true);
  const numerator = BigInt(basis) * BigInt(rateBPS) + 5000n;
  const result = numerator / 10000n;
  assert(result <= BigInt(Number.MAX_SAFE_INTEGER), 'AMOUNT_OVERFLOW');
  return Number(result);
}
function utc(text) {
  const value = Date.parse(text);
  assert(Number.isFinite(value), 'INVALID_TIME');
  return value;
}
const definitions = {
  channel_receivable: ['debit', false], unallocated: ['credit', true],
  merchant_pending: ['credit', true], merchant_available: ['credit', true],
  merchant_settlement_held: ['credit', true], merchant_refund_held: ['credit', true],
  partner_payable: ['credit', true], service_revenue: ['credit', false], cash: ['debit', true],
};
let state = {
  bookID: fixture.book_id, currency: fixture.currency, cashReceived: false,
  accounts: Object.fromEntries(Object.entries(definitions).map(([id, [side, restricted]]) =>
    [id, { side, restricted, debits: 0, credits: 0, version: 0 }])),
  journals: {}, payments: {}, assessments: {}, allocations: {}, entitlements: {},
  settlements: {}, settlementRequests: {}, payouts: {}, inbox: {}, providerTransactions: {},
};
// Copy-and-commit models all-or-nothing mutation only. It is NOT a DB isolation test.
function atomic(work) {
  const next = structuredClone(state);
  const result = work(next);
  state = next;
  return structuredClone(result);
}
function balance(s, id) {
  const a = s.accounts[id];
  assert(a, 'UNKNOWN_ACCOUNT');
  return a.side === 'debit' ? a.debits - a.credits : a.credits - a.debits;
}
function post(s, key, lines) {
  const fingerprint = canonical({ book: s.bookID, currency: s.currency, lines });
  if (s.journals[key]) {
    assert.equal(s.journals[key].fingerprint, fingerprint, 'JOURNAL_CONFLICT');
    return;
  }
  assert(lines.length >= 2, 'INCOMPLETE_JOURNAL');
  let debit = 0, credit = 0;
  for (const [id, side, value] of lines) {
    assert(s.accounts[id], 'UNKNOWN_ACCOUNT'); amount(value);
    assert(side === 'debit' || side === 'credit', 'INVALID_SIDE');
    if (side === 'debit') debit = add(debit, value); else credit = add(credit, value);
  }
  assert.equal(debit, credit, 'UNBALANCED');
  const changed = new Set();
  for (const [id, side, value] of lines) {
    const field = side === 'debit' ? 'debits' : 'credits';
    s.accounts[id][field] = add(s.accounts[id][field], value); changed.add(id);
  }
  for (const id of changed) {
    assert(!s.accounts[id].restricted || balance(s, id) >= 0, 'INSUFFICIENT_FUNDS');
    s.accounts[id].version += 1;
  }
  s.journals[key] = { fingerprint, lines: structuredClone(lines) };
}
function seedPayments() {
  return atomic((s) => {
    fixture.payments.forEach((p, index) => {
      assert(!s.payments[p.id], 'DUPLICATE_PAYMENT');
      amount(p.amount_minor); amount(p.partner_amount_minor, true);
      const fee = feeFor(p.amount_minor, fixture.fee_rule.rate_bps);
      const merchant = p.amount_minor - fee - p.partner_amount_minor; amount(merchant);
      s.payments[p.id] = { status: 'succeeded', gross: p.amount_minor };
      s.assessments[p.id] = { basis: p.amount_minor, fee, rule: fixture.fee_rule.version,
        rounding: fixture.fee_rule.rounding };
      s.allocations[p.id] = { merchant, partner: p.partner_amount_minor, fee,
        version: fixture.clearing_version };
      assert.equal(add(add(merchant, fee), p.partner_amount_minor), p.amount_minor);
      post(s, `payment/${p.id}/receipt`, [['channel_receivable', 'debit', p.amount_minor],
        ['unallocated', 'credit', p.amount_minor]]);
      const lines = [['unallocated', 'debit', p.amount_minor],
        ['merchant_pending', 'credit', merchant], ['service_revenue', 'credit', fee]];
      if (p.partner_amount_minor) lines.push(['partner_payable', 'credit', p.partner_amount_minor]);
      post(s, `payment/${p.id}/allocation`, lines);
      s.entitlements[`E${index + 1}`] = { paymentID: p.id, amount: merchant,
        merchantID: fixture.merchant_id, accountGroupID: fixture.account_group_id,
        currency: s.currency, availableOn: fixture.settlement_rule.available_on,
        status: 'pending', currentClaimID: null, consumed: 0 };
    });
  });
}
function fundAndRelease() {
  return atomic((s) => {
    const gross = sum(Object.values(s.payments).map((p) => p.gross));
    post(s, 'provider/ST1/cash_received', [['cash', 'debit', gross], ['channel_receivable', 'credit', gross]]);
    s.cashReceived = true;
    // Explicitly apply the fixture's eligibility events, at/after availableOn.
    for (const [id, e] of Object.entries(s.entitlements)) {
      post(s, `entitlement/${id}/eligible`, [['merchant_pending', 'debit', e.amount],
        ['merchant_available', 'credit', e.amount]]);
      e.status = 'eligible';
    }
  });
}
function prepareSettlement(request) {
  return atomic((s) => {
    const { proposed_id: proposedID, ...meaningfulInput } = request;
    const input = { ...meaningfulInput, source_ids: [...request.source_ids].sort() };
    const fingerprint = canonical(input);
    const old = s.settlementRequests[request.request_key];
    if (old) {
      assert.equal(old.fingerprint, fingerprint, 'SETTLEMENT_REQUEST_CONFLICT');
      return s.settlements[old.id];
    }
    assert.equal(request.rule_version, fixture.settlement_rule.version, 'UNKNOWN_SETTLEMENT_RULE');
    assert.equal(request.merchant_id, fixture.merchant_id, 'WRONG_MERCHANT');
    assert.equal(request.account_group_id, fixture.account_group_id, 'WRONG_ACCOUNT_GROUP');
    assert.equal(request.currency, s.currency, 'WRONG_CURRENCY');
    assert.equal(request.destination_version, fixture.settlement_request.destination_version, 'UNVERIFIED_DESTINATION');
    assert(s.cashReceived, 'FUNDING_NOT_CONFIRMED');
    assert(request.source_ids.length > 0 && new Set(request.source_ids).size === request.source_ids.length,
      'INVALID_SOURCE_LIST');
    assert(!s.settlements[proposedID], 'SETTLEMENT_ID_CONFLICT');
    const sources = input.source_ids.map((id) => {
      const e = s.entitlements[id]; assert(e, 'UNKNOWN_SOURCE');
      assert(utc(e.availableOn) <= utc(request.cutoff), 'NOT_DUE');
      assert(e.status === 'eligible' && e.currentClaimID === null, 'SOURCE_ALREADY_CLAIMED');
      assert.equal(e.merchantID, request.merchant_id, 'SOURCE_OWNER_MISMATCH');
      assert.equal(e.accountGroupID, request.account_group_id, 'SOURCE_GROUP_MISMATCH');
      assert.equal(e.currency, request.currency, 'SOURCE_CURRENCY_MISMATCH');
      return { id, amount: e.amount };
    });
    const total = sum(sources.map((e) => e.amount)); amount(total);
    post(s, `settlement/${proposedID}/reserve`, [['merchant_available', 'debit', total],
      ['merchant_settlement_held', 'credit', total]]);
    const order = { id: proposedID, amount: total, paid: 0, status: 'reserved', sources,
      payoutIDs: [], currency: request.currency, destination: request.destination_version };
    for (const e of sources) {
      s.entitlements[e.id].currentClaimID = proposedID; s.entitlements[e.id].status = 'claimed';
    }
    let remaining = total, sequence = 1, sourceIndex = 0, sourceUsed = 0;
    const limit = amount(fixture.settlement_rule.payout_max_minor);
    while (remaining > 0) {
      const value = Math.min(limit, remaining), id = `PO${sequence}`;
      assert(!s.payouts[id], 'PAYOUT_ID_CONFLICT');
      let toMap = value; const mappings = [];
      while (toMap > 0) {
        const e = sources[sourceIndex];
        const part = Math.min(toMap, e.amount - sourceUsed);
        mappings.push({ sourceID: e.id, amount: part });
        sourceUsed += part; toMap -= part;
        if (sourceUsed === e.amount) { sourceIndex += 1; sourceUsed = 0; }
      }
      assert.equal(sum(mappings.map((m) => m.amount)), value);
      s.payouts[id] = { id, settlementID: proposedID, amount: value, mappings,
        status: 'queued', attemptID: `PA${sequence}-1`, operationID: `OP${sequence}-1`,
        operationStatus: 'prepared', finalProof: null };
      order.payoutIDs.push(id); remaining -= value; sequence += 1;
    }
    assert.equal(sum(order.payoutIDs.map((id) => s.payouts[id].amount)), total);
    s.settlements[proposedID] = order;
    s.settlementRequests[request.request_key] = { id: proposedID, fingerprint };
    return order;
  });
}
function dispatch(id) {
  return atomic((s) => {
    const p = s.payouts[id]; assert(p && p.status === 'queued', 'PAYOUT_NOT_READY');
    assert(balance(s, 'merchant_settlement_held') >= p.amount, 'MISSING_RESERVATION');
    // Real execution additionally requires current authorization/risk/liquidity permits.
    p.status = 'processing'; p.operationStatus = 'sent';
  });
}
function markUnknown(id) {
  return atomic((s) => {
    const p = s.payouts[id]; assert(p.status === 'processing' && p.operationStatus === 'sent');
    p.operationStatus = 'unknown';
  });
}
function recoveryCommand(id) {
  const p = state.payouts[id]; assert.equal(p.operationStatus, 'unknown');
  return { action: 'query', operationID: p.operationID, attemptID: p.attemptID };
}
function observeSuccess(id, proof) {
  return atomic((s) => {
    const p = s.payouts[id]; assert(p, 'UNKNOWN_PAYOUT');
    assert.equal(proof.operationID, p.operationID, 'WRONG_OPERATION');
    assert.equal(proof.amount, p.amount, 'PROVIDER_AMOUNT_MISMATCH');
    assert.equal(proof.currency, s.currency, 'PROVIDER_CURRENCY_MISMATCH');
    assert.equal(proof.status, 'succeeded', 'NOT_FINAL');
    const evidence = { operationID: proof.operationID, amount: proof.amount,
      currency: proof.currency, transactionID: proof.transactionID, status: proof.status };
    const signature = canonical(evidence), eventKey = `provider-demo/${proof.eventID}`;
    if (s.inbox[eventKey]) assert.equal(s.inbox[eventKey], signature, 'EVENT_CONTENT_CONFLICT');
    if (p.finalProof) assert.equal(canonical(p.finalProof), signature, 'PAYOUT_FINAL_PROOF_CONFLICT');
    const transactionKey = `provider-demo/${proof.transactionID}`;
    assert(!s.providerTransactions[transactionKey] || s.providerTransactions[transactionKey] === id,
      'PROVIDER_TRANSACTION_REUSED');
    assert(p.status === 'processing' || p.status === 'accounting_pending' || p.status === 'succeeded',
      'PAYOUT_NOT_EXECUTING');
    s.inbox[eventKey] = signature; s.providerTransactions[transactionKey] = id;
    p.finalProof = evidence; p.operationStatus = 'succeeded';
    if (p.status !== 'succeeded') p.status = 'accounting_pending';
  });
}
function finalize(id) {
  return atomic((s) => {
    const p = s.payouts[id];
    if (p.status === 'succeeded') return;
    assert(p.status === 'accounting_pending' && p.finalProof, 'MISSING_FINAL_PROOF');
    post(s, `payout/${id}/paid`, [['merchant_settlement_held', 'debit', p.amount], ['cash', 'credit', p.amount]]);
    for (const mapping of p.mappings) {
      const e = s.entitlements[mapping.sourceID];
      assert.equal(e.currentClaimID, p.settlementID, 'CLAIM_MISMATCH');
      e.consumed = add(e.consumed, mapping.amount); assert(e.consumed <= e.amount, 'SOURCE_OVERPAID');
      if (e.consumed === e.amount) e.status = 'consumed';
    }
    p.status = 'succeeded';
    const order = s.settlements[p.settlementID];
    order.paid = sum(order.payoutIDs.map((pid) => s.payouts[pid].status === 'succeeded' ? s.payouts[pid].amount : 0));
    order.status = order.paid === order.amount ? 'completed' : 'partially_paid';
  });
}
function rejectsWithoutMutation(work, pattern) {
  const before = canonical(state);
  assert.throws(work, pattern);
  assert.equal(canonical(state), before, 'REJECTION_PARTIALLY_MUTATED_STATE');
}
function printStage(label) {
  const s = state.settlements.S1;
  console.log(label, { settlement: s?.status ?? 'none', paid: s?.paid ?? 0,
    held: balance(state, 'merchant_settlement_held'), cash: balance(state, 'cash') });
}

// Walk the scenario and verify boundaries, not just the final sum.
assert.equal(fixture.fee_rule.rounding, 'half_up_per_payment');
assert.equal(add(feeFor(26, 200), feeFor(26, 200)), 2);
assert.equal(feeFor(52, 200), 1);
seedPayments(); fundAndRelease();
assert.equal(sum(Object.values(state.assessments).map((a) => a.fee)), fixture.expected.fee_minor);
assert.equal(balance(state, 'merchant_available'), fixture.expected.merchant_minor);
assert.equal(balance(state, 'partner_payable'), fixture.expected.partner_minor);
assert.equal(feeFor(10000, 300), 300);
assert.equal(state.assessments.P1.fee, 200, 'NEW_RATE_REWROTE_HISTORY');
rejectsWithoutMutation(() => prepareSettlement({ ...fixture.settlement_request,
  cutoff: '2026-11-20T12:00:00Z' }), /NOT_DUE/);
const original = prepareSettlement(fixture.settlement_request);
const beforeReplay = canonical(state);
assert.equal(prepareSettlement({ ...fixture.settlement_request, proposed_id: 'S-retry' }).id, original.id);
assert.equal(canonical(state), beforeReplay);
rejectsWithoutMutation(() => prepareSettlement({ ...fixture.settlement_request,
  rule_version: 'SETTLEMENT-V2' }), /SETTLEMENT_REQUEST_CONFLICT/);
rejectsWithoutMutation(() => prepareSettlement({ ...fixture.settlement_request,
  proposed_id: 'S2', request_key: 'another-settlement-request' }), /SOURCE_ALREADY_CLAIMED/);
assert.deepEqual(Object.values(state.payouts).map((p) => p.amount), fixture.expected.payouts_minor);
printStage('RESERVED');

dispatch('PO1');
const proof1 = { eventID: 'EV1', operationID: 'OP1-1', transactionID: 'provider-txn-1',
  amount: 8000, currency: 'USD', status: 'succeeded' };
observeSuccess('PO1', proof1);
assert.equal(state.settlements.S1.paid, 0, 'SUCCESS_WITHOUT_ACCOUNTING');
finalize('PO1');
assert.equal(balance(state, 'cash'), fixture.expected.cash_after_po1_minor);
assert.equal(state.settlements.S1.paid, 8000);
dispatch('PO2'); markUnknown('PO2');
const unknownState = canonical(state);
assert.deepEqual(recoveryCommand('PO2'), { action: 'query', operationID: 'OP2-1', attemptID: 'PA2-1' });
assert.equal(canonical(state), unknownState);
assert.equal(Object.keys(state.payouts).length, 2);
assert.equal(balance(state, 'merchant_settlement_held'), 6600);
assert.equal(balance(state, 'merchant_available'), 0);
rejectsWithoutMutation(() => atomic((s) => post(s, 'refund/RF1/reserve',
  [['merchant_available', 'debit', 1000], ['merchant_refund_held', 'credit', 1000]])), /INSUFFICIENT_FUNDS/);
printStage('PO2_UNKNOWN');

const proof2 = { eventID: 'EV2', operationID: 'OP2-1', transactionID: 'provider-txn-2',
  amount: 6600, currency: 'USD', status: 'succeeded' };
observeSuccess('PO2', proof2);
assert.equal(state.payouts.PO2.status, 'accounting_pending');
assert.equal(state.settlements.S1.paid, 8000);
assert.equal(balance(state, 'merchant_settlement_held'), 6600);
printStage('PROVIDER_SUCCESS_ACCOUNTING_PENDING');
finalize('PO2');
const completed = canonical(state);
observeSuccess('PO2', proof2); finalize('PO2');
assert.equal(canonical(state), completed);
const journalCount = Object.keys(state.journals).length;
observeSuccess('PO2', { ...proof2, eventID: 'EV2-another-delivery' }); finalize('PO2');
assert.equal(Object.keys(state.journals).length, journalCount);
assert.equal(balance(state, 'cash'), fixture.expected.cash_final_minor);
rejectsWithoutMutation(() => observeSuccess('PO2', { ...proof2, amount: 6601 }), /PROVIDER_AMOUNT_MISMATCH/);
assert.equal(state.settlements.S1.status, 'completed');
assert.equal(state.settlements.S1.paid, fixture.expected.merchant_minor);
assert.equal(balance(state, 'merchant_settlement_held'), 0);
assert.equal(balance(state, 'cash'), add(balance(state, 'partner_payable'), balance(state, 'service_revenue')));
for (const p of Object.values(state.payments)) assert.equal(p.status, 'succeeded');
for (const e of Object.values(state.entitlements)) assert.equal(e.consumed, e.amount);
for (const j of Object.values(state.journals)) {
  assert.equal(sum(j.lines.filter((l) => l[1] === 'debit').map((l) => l[2])),
    sum(j.lines.filter((l) => l[1] === 'credit').map((l) => l[2])));
}
assert.equal(journalCount, 10);
printStage('COMPLETED');
console.table(Object.entries(state.allocations).map(([payment, a]) => ({ payment, ...a })));
console.log('PASS: 10 balanced journals; versioned fees; due-time guard; request replay/conflict; source claims; split payouts; unknown recovery; shared funds guard; accounting confirmation; duplicate success.');
