// 只读教学演算：无网络、数据库或文件写入，不是生产账本实现。
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const fixture = JSON.parse(readFileSync(new URL('./ledger-stage-5-journals.json', import.meta.url), 'utf8'));
const clone = value => structuredClone(value);
const accounts = new Map(fixture.accounts.map(account => [account.id, account]));
const checkedAdd = (left, right) => {
  const result = left + right;
  assert(Number.isSafeInteger(result), 'unsafe_integer');
  return result;
};
const balanceOf = (account, totals) => account.normal_side === 'debit'
  ? totals.debits - totals.credits
  : totals.credits - totals.debits;
const businessKey = journal => JSON.stringify([
  journal.book_id, journal.business_type, journal.business_id, journal.event_type,
]);
const fingerprint = journal => JSON.stringify({
  business_key: businessKey(journal), currency: journal.currency,
  effective_at: journal.effective_at,
  postings: journal.postings.map((posting, index) => ({
    sequence_no: index + 1,
    account_id: posting.account_id,
    side: posting.side,
    amount_minor: posting.amount_minor,
  })),
});

function createLedger() {
  const balances = new Map(fixture.accounts.map(account => [account.id, { debits: 0, credits: 0, version: 0 }]));
  const postedByBusinessKey = new Map();
  const postedByID = new Map();
  const posted = [];

  function post(command) {
    for (const key of ['id', 'book_id', 'business_type', 'business_id', 'event_type', 'currency', 'effective_at']) {
      assert.equal(typeof command[key], 'string', `invalid_${key}`);
      assert(command[key].length > 0, `empty_${key}`);
    }
    const key = businessKey(command);
    const prior = postedByBusinessKey.get(key);
    if (prior) {
      assert.equal(prior.fingerprint, fingerprint(command), 'idempotency_conflict');
      return { applied: false, journal_id: prior.journal_id };
    }
    assert(!postedByID.has(command.id), 'journal_id_conflict');
    assert.equal(command.book_id, fixture.book_id, 'book_mismatch');
    assert.equal(command.currency, fixture.currency, 'currency_mismatch');
    assert(Array.isArray(command.postings) && command.postings.length >= 2, 'too_few_postings');

    let debits = 0;
    let credits = 0;
    const next = clone(balances);
    const affected = new Set();
    for (const posting of command.postings) {
      const account = accounts.get(posting.account_id);
      assert(account, 'unknown_account');
      assert.notEqual(account.status, 'closed', 'closed_account');
      assert.equal(account.book_id, command.book_id, 'account_book_mismatch');
      assert.equal(account.currency, command.currency, 'account_currency_mismatch');
      assert(['debit', 'credit'].includes(posting.side), 'invalid_side');
      assert(Number.isSafeInteger(posting.amount_minor) && posting.amount_minor > 0, 'invalid_amount');
      const totals = next.get(posting.account_id);
      if (posting.side === 'debit') {
        debits = checkedAdd(debits, posting.amount_minor);
        totals.debits = checkedAdd(totals.debits, posting.amount_minor);
      } else {
        credits = checkedAdd(credits, posting.amount_minor);
        totals.credits = checkedAdd(totals.credits, posting.amount_minor);
      }
      affected.add(posting.account_id);
    }
    assert.equal(debits, credits, 'unbalanced_journal');
    for (const accountID of affected) {
      const account = accounts.get(accountID);
      const totals = next.get(accountID);
      if (account.nonnegative) assert(balanceOf(account, totals) >= 0, 'insufficient_available_or_held_funds');
      totals.version = checkedAdd(totals.version, 1);
    }

    // 全部检查通过后才一次性应用；失败时不改变原状态。
    for (const [accountID, totals] of next) balances.set(accountID, totals);
    const snapshot = clone(command);
    posted.push(snapshot);
    postedByID.set(command.id, snapshot);
    postedByBusinessKey.set(key, { fingerprint: fingerprint(command), journal_id: command.id });
    return { applied: true, journal_id: command.id, debits, credits };
  }

  return { balances, posted, post };
}

const ledger = createLedger();
for (const journal of fixture.journals) {
  const result = ledger.post(journal);
  console.log(`${journal.id}: debit=${result.debits}, credit=${result.credits}`);
}
for (const [accountID, expected] of Object.entries(fixture.expected_balances)) {
  assert.equal(balanceOf(accounts.get(accountID), ledger.balances.get(accountID)), expected);
}

const beforeReplay = clone(ledger.balances);
for (const journal of fixture.journals) assert.equal(ledger.post(clone(journal)).applied, false);
const reorderedReplay = clone(fixture.journals[1]);
reorderedReplay.id = 'J2-retry-local-id';
reorderedReplay.postings = reorderedReplay.postings.map(posting => ({
  amount_minor: posting.amount_minor, side: posting.side, account_id: posting.account_id,
}));
assert.deepEqual(ledger.post(reorderedReplay), { applied: false, journal_id: 'J2' });
assert.deepEqual(ledger.balances, beforeReplay);
assert.equal(ledger.posted.length, 8);

const conflict = clone(fixture.journals[0]);
conflict.postings[0].amount_minor += 1;
assert.throws(() => ledger.post(conflict), /idempotency_conflict/);
assert.deepEqual(ledger.balances, beforeReplay);

const unbalanced = clone(fixture.journals[0]);
unbalanced.id = 'J-bad';
unbalanced.business_id = 'P-bad';
unbalanced.postings[0].amount_minor += 1;
assert.throws(() => ledger.post(unbalanced), /unbalanced_journal/);
assert.deepEqual(ledger.balances, beforeReplay);

const reserved = createLedger();
for (const journal of fixture.journals.slice(0, 5)) reserved.post(journal);
const reservedSnapshot = clone(reserved.balances);
assert.equal(balanceOf(accounts.get('merchant_available'), reserved.balances.get('merchant_available')), 5800);
const payout = {
  id: 'J-too-large-payout', book_id: fixture.book_id,
  business_type: 'payout', business_id: 'PO-test', event_type: 'reserve',
  currency: fixture.currency, effective_at: '2026-11-22T11:01:01Z',
  postings: [
    { account_id: 'merchant_available', side: 'debit', amount_minor: 9800 },
    { account_id: 'merchant_payout_held', side: 'credit', amount_minor: 9800 },
  ],
};
assert.throws(() => reserved.post(payout), /insufficient_available_or_held_funds/);
assert.deepEqual(reserved.balances, reservedSnapshot);

const rebuilt = createLedger();
for (const journal of ledger.posted) rebuilt.post(journal);
assert.deepEqual(rebuilt.balances, ledger.balances);
const damagedProjection = clone(ledger.balances);
damagedProjection.get('merchant_available').credits += 1;
assert.notDeepEqual(damagedProjection, rebuilt.balances);

const totals = { asset: 0, liability: 0, revenue: 0, expense: 0, equity: 0 };
for (const account of fixture.accounts) {
  totals[account.account_type] = checkedAdd(totals[account.account_type], balanceOf(account, ledger.balances.get(account.id)));
}
assert.equal(totals.asset + totals.expense, totals.liability + totals.equity + totals.revenue);
assert.equal(totals.asset - totals.liability, 170);
console.table(fixture.accounts.map(account => ({
  account: account.name,
  normal_side: account.normal_side,
  balance_minor: balanceOf(account, ledger.balances.get(account.id)),
  version: ledger.balances.get(account.id).version,
})));
console.log('PASS: 8 journals balanced; replay unchanged; conflicts/unbalanced/overspend rejected; projection rebuilt.');
console.log(`asset=${totals.asset}, liability=${totals.liability}, revenue=${totals.revenue}, expense=${totals.expense}, equity_including_current_profit=170`);
