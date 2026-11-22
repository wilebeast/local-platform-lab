// Read-only virtual-clock exercise. No real sleep, network, database or writes.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const fixture = JSON.parse(readFileSync(new URL('./architecture-stage-8-capacity.json', import.meta.url), 'utf8'));
function integer(value, allowZero = false) {
  assert(Number.isSafeInteger(value) && value >= (allowZero ? 0 : 1), 'INVALID_PLANNING_INPUT');
  return value;
}
function makeTasks(input) {
  const tasks = [];
  for (let i = 0; i < integer(input.notification.burst_count); i += 1) {
    tasks.push({ id: `notification-${i}`, kind: 'notification', arrival: 0,
      duration: integer(input.notification.service_ms), sequence: tasks.length });
  }
  for (let i = 0; i < integer(input.transaction.count); i += 1) {
    tasks.push({ id: `transaction-${i}`, kind: 'transaction',
      arrival: i * integer(input.transaction.arrival_interval_ms),
      duration: integer(input.transaction.service_ms), sequence: tasks.length });
  }
  return tasks.sort((a, b) => a.arrival - b.arrival || a.sequence - b.sequence);
}
function simulate(tasks, limits, isolated) {
  const lanes = isolated ? ['transaction', 'notification'] : ['shared'];
  const caps = Object.fromEntries(lanes.map((lane) => [lane, integer(limits[lane])]));
  const pending = Object.fromEntries(lanes.map((lane) => [lane, []]));
  let active = [], index = 0, now = 0, maxInFlight = 0;
  const completed = [];
  while (index < tasks.length || active.length || lanes.some((lane) => pending[lane].length)) {
    const nextArrival = index < tasks.length ? tasks[index].arrival : Infinity;
    const nextFinish = active.length ? Math.min(...active.map((task) => task.finish)) : Infinity;
    now = Math.min(nextArrival, nextFinish);
    assert(Number.isFinite(now), 'SCHEDULER_STALLED');
    const finishing = active.filter((task) => task.finish === now);
    completed.push(...finishing);
    active = active.filter((task) => task.finish !== now);
    while (index < tasks.length && tasks[index].arrival === now) {
      const task = tasks[index++];
      pending[isolated ? task.kind : 'shared'].push(task);
    }
    for (const lane of lanes) {
      let inFlight = active.filter((task) => task.lane === lane).length;
      while (pending[lane].length && inFlight < caps[lane]) {
        const task = pending[lane].shift();
        active.push({ ...task, lane, start: now, finish: now + task.duration });
        inFlight += 1;
      }
      assert(inFlight <= caps[lane], 'LANE_CAPACITY_EXCEEDED');
    }
    maxInFlight = Math.max(maxInFlight, active.length);
  }
  assert.equal(completed.length, tasks.length, 'TASK_LOST');
  assert.equal(new Set(completed.map((task) => task.id)).size, tasks.length, 'TASK_DUPLICATED');
  for (const task of completed) {
    assert(task.start >= task.arrival && task.finish - task.start === task.duration);
  }
  return { completed, maxInFlight };
}
function percentile(values, fraction) {
  assert(values.length > 0 && fraction > 0 && fraction <= 1);
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.ceil(fraction * sorted.length) - 1];
}
function metrics(label, simulation) {
  const ofKind = (kind) => simulation.completed.filter((task) => task.kind === kind);
  const tx = ofKind('transaction'), notifications = ofKind('notification');
  return {
    model: label,
    transaction_p95_ms: percentile(tx.map((task) => task.finish - task.arrival), 0.95),
    transaction_max_ms: Math.max(...tx.map((task) => task.finish - task.arrival)),
    notification_last_finish_ms: Math.max(...notifications.map((task) => task.finish)),
    max_in_flight: simulation.maxInFlight,
  };
}
function connectionBudget(database) {
  integer(database.max_connections); integer(database.system_reserved, true);
  const applicationMax = database.pools.reduce((sum, pool) =>
    sum + integer(pool.replicas, true) * integer(pool.pool_max), 0);
  assert(Number.isSafeInteger(applicationMax), 'POOL_BUDGET_OVERFLOW');
  const total = applicationMax + database.system_reserved;
  return { application_max: applicationMax, total_with_reserved: total,
    headroom: database.max_connections - total, accepted: total <= database.max_connections };
}
function idealDrainSeconds(pending, arrivalRate, completionRate) {
  integer(pending, true);
  assert(Number.isFinite(arrivalRate) && arrivalRate >= 0);
  assert(Number.isFinite(completionRate) && completionRate >= 0);
  if (pending === 0) return 0;
  if (completionRate <= arrivalRate) return null;
  return Math.ceil(pending / (completionRate - arrivalRate));
}

const tasks = makeTasks(fixture);
const shared = simulate(tasks, { shared: fixture.worker_limits.shared }, false);
const isolated = simulate(tasks, { transaction: fixture.worker_limits.isolated_transaction,
  notification: fixture.worker_limits.isolated_notification }, true);
const sharedMetrics = metrics('shared FIFO: 20', shared);
const isolatedMetrics = metrics('isolated: transaction 4 + notification 16', isolated);
const target = integer(fixture.transaction.exercise_p95_target_ms);
assert.equal(fixture.worker_limits.shared,
  fixture.worker_limits.isolated_transaction + fixture.worker_limits.isolated_notification,
  'COMPARE_WITH_SAME_TOTAL_SLOT_CEILING');
assert(sharedMetrics.transaction_p95_ms > target, 'SHARED_QUEUE_DID_NOT_EXPOSE_INTERFERENCE');
assert(isolatedMetrics.transaction_p95_ms <= target, 'TRANSACTION_RESERVATION_DID_NOT_PROTECT');
assert(shared.maxInFlight <= fixture.worker_limits.shared);
assert(isolated.maxInFlight <= fixture.worker_limits.shared);
assert(isolatedMetrics.notification_last_finish_ms > sharedMetrics.notification_last_finish_ms,
  'ISOLATION_DOES_NOT_CREATE_FREE_CAPACITY');
console.table([sharedMetrics, isolatedMetrics]);

const baseBudget = connectionBudget(fixture.database);
assert.deepEqual(baseBudget, { application_max: 90, total_with_reserved: 110, headroom: 10, accepted: true });
const scaled = structuredClone(fixture.database);
scaled.pools.find((pool) => pool.name === 'transaction-api').replicas = scaled.proposed_transaction_replicas;
const scaledBudget = connectionBudget(scaled);
assert.deepEqual(scaledBudget, { application_max: 110, total_with_reserved: 130, headroom: -10, accepted: false });
assert.equal(fixture.database.pools[0].replicas, 4, 'PLANNING_MUTATED_ORIGINAL_CONFIGURATION');
console.log('DATABASE_BUDGET', { before: baseBudget, proposed_scale: scaledBudget });

const r = fixture.recovery;
const seconds = idealDrainSeconds(r.pending_count, r.new_items_per_second, r.successful_items_per_second);
assert.equal(seconds, 213);
assert.equal(idealDrainSeconds(10000, 3, 3), null, 'ZERO_NET_DRAIN_INCORRECTLY_CONVERGED');
const notificationCapacityPerSecond = fixture.worker_limits.isolated_notification /
  (fixture.notification.service_ms / 1000);
assert.equal(notificationCapacityPerSecond, 3.2);
assert(5 > notificationCapacityPerSecond, 'SUSTAINED_OVERLOAD_EXAMPLE_INVALID');
console.log('IDEAL_RECOVERY_SECONDS', seconds);
console.log('NOTIFICATION_CAPACITY_PER_SECOND', notificationCapacityPerSecond);
console.log('PASS: same slot ceiling; transaction latency isolated; all tasks preserved; notification tradeoff exposed; DB expansion gated; net-drain recovery checked.');
console.log('These results are deterministic teaching calculations, not measured TPS, production latency, durability, or fault-recovery evidence.');
