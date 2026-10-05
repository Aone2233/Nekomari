import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import ts from 'typescript';

// Exercise the real module. It imports types only, so the transpiled CommonJS has
// no runtime dependencies and can run in a bare vm context.
const source = readFileSync(new URL('../src/utils/trafficSummary.ts', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

const exports = {};
vm.runInNewContext(compiled, { module: { exports }, exports, console });
const {
  NET_METRIC_KEYS,
  TRAFFIC_AGGREGATION_BY_METRIC,
  TRAFFIC_INTERVAL_SEMANTICS,
  TRAFFIC_TOTAL_METRIC_KEYS,
  computeTrafficSummary,
} = exports;

const GIB = 1024 ** 3;
const MIB = 1024 ** 2;
const BASE = Date.UTC(2026, 9, 5, 0, 0, 0);

const point = (minute, value) => ({
  time: new Date(BASE + minute * 60_000).toISOString(),
  value,
});

// Values built inside the vm carry the vm's prototypes, which `deepStrictEqual`
// compares; a JSON round-trip puts them back in this realm so equality means value
// equality.
const plain = (value) => JSON.parse(JSON.stringify(value));

const series = (metricKey, entityId, points, semantics) => {
  const item = {
    metric_key: metricKey,
    entity_id: entityId,
    count: points.length,
    points,
  };
  if (semantics !== undefined) item.semantics = semantics;
  return item;
};

// What the panel returns for `traffic.*` asked for with `sum`: validated interval amounts.
const intervals = (metricKey, entityId, points) =>
  series(metricKey, entityId, points, TRAFFIC_INTERVAL_SEMANTICS);

const response = (seriesList) => ({
  start: new Date(BASE).toISOString(),
  end: new Date(BASE + 24 * 3600_000).toISOString(),
  series: seriesList,
  count: seriesList.length,
});

test('traffic.* is requested as the additive interval amount, never as `last`', () => {
  // `last` returns the billing-cycle cumulative itself. computeTrafficSummary adds
  // one value per chart bucket, so `last` here multiplies one running total by the
  // bucket count — which is how a ~25 GB cycle total rendered as several TB on the
  // 24 h card. The panel remaps `sum` on these keys to the validated interval series
  // (`traffic.interval.up` / `.down`), whose points are additive.
  assert.deepEqual(plain(TRAFFIC_AGGREGATION_BY_METRIC), {
    'traffic.up': 'sum',
    'traffic.down': 'sum',
  });
  assert.deepEqual(plain(TRAFFIC_TOTAL_METRIC_KEYS), ['traffic.up', 'traffic.down']);
  assert.deepEqual(plain(NET_METRIC_KEYS), ['net.in.rate', 'net.out.rate']);
  assert.equal(TRAFFIC_INTERVAL_SEMANTICS, 'interval_delta_v2');
});

test('interval amounts add up to the traffic observed in the window', () => {
  const up = [1, 2, 3, 4, 5].map((i, index) => point(index, i * GIB));
  const down = [2, 4, 6, 8, 10].map((i, index) => point(index, i * GIB));
  const summary = computeTrafficSummary(
    response([
      intervals('traffic.up', 'node-a', up),
      intervals('traffic.down', 'node-a', down),
    ]),
  );

  assert.equal(summary.totalUp, 15 * GIB);
  assert.equal(summary.totalDown, 30 * GIB);
  assert.equal(summary.points.length, 5);
  // The dashed line is the window's running total, so it ends at the same number.
  assert.equal(summary.points.at(-1).upCum, 15 * GIB);
  assert.equal(summary.points.at(-1).downCum, 30 * GIB);
  assert.deepEqual(
    plain(summary.nodeTotals.map((node) => [node.uuid, node.up, node.total, node.unknown])),
    [['node-a', 15 * GIB, 45 * GIB, false]],
  );
  assert.deepEqual(plain(summary.unknownTrafficNodes), []);
});

test('the same buckets read as cycle-cumulative snapshots inflate by the bucket count', () => {
  // This is the shape the card received while it asked for `last`, and the shape the
  // panel still returns for a node with no interval data (next test). The real growth
  // is the last value minus the first (~287 MiB); the sum of snapshots is ~7 TiB. The
  // assertion exists so a misplaced aggregation shows up as a number, not a surprise.
  const buckets = 288;
  const cycleStart = 25 * GIB;
  const perBucket = MIB;
  const snapshots = [];
  for (let i = 0; i < buckets; i += 1) {
    snapshots.push(point(i * 5, cycleStart + i * perBucket));
  }
  const summary = computeTrafficSummary(
    response([series('traffic.up', 'node-a', snapshots, 'billing_cycle_cumulative')]),
  );

  const realGrowth = (buckets - 1) * perBucket;
  const naiveSum = buckets * cycleStart + ((buckets - 1) * buckets * perBucket) / 2;
  assert.ok(
    naiveSum > realGrowth * 1000,
    `snapshots read as traffic: ${naiveSum} vs real growth ${realGrowth}`,
  );
  // The module refuses that reading instead: nothing is added, and the node is named.
  assert.equal(summary.totalUp, 0);
  assert.deepEqual(plain(summary.unknownTrafficNodes), ['node-a']);
});

test('a series the panel could not measure is unknown, never summed', () => {
  // The panel answers an entity with no validated interval data by reading the
  // billing-cycle counter with `last` and saying so. Those points are the running
  // total, so adding them reproduces the inflation the interval series exists to
  // prevent; calling them 0 would dress a real cycle total up as an answer.
  const snapshots = [];
  for (let i = 0; i < 288; i += 1) snapshots.push(point(i * 5, 25 * GIB + i * MIB));
  const summary = computeTrafficSummary(
    response([
      series('traffic.up', 'legacy-a', snapshots, 'billing_cycle_cumulative'),
      intervals('traffic.up', 'modern-a', [point(0, GIB), point(1, 2 * GIB)]),
    ]),
  );

  assert.equal(summary.totalUp, 3 * GIB);
  assert.deepEqual(plain(summary.unknownTrafficNodes), ['legacy-a']);
  const legacy = summary.nodeTotals.find((node) => node.uuid === 'legacy-a');
  assert.equal(legacy.unknown, true);
  assert.equal(legacy.total, 0);
  const modern = summary.nodeTotals.find((node) => node.uuid === 'modern-a');
  assert.equal(modern.unknown, false);
  assert.equal(modern.total, 3 * GIB);
});

test('a traffic series without the interval marker is not summed on trust', () => {
  // The marker is what says the values are additive. Without it the safe reading is
  // "unknown", because every other meaning of this metric is a running total.
  const summary = computeTrafficSummary(
    response([series('traffic.up', 'node-a', [point(0, GIB), point(1, GIB)])]),
  );

  assert.equal(summary.totalUp, 0);
  assert.equal(summary.points.length, 0);
  assert.deepEqual(plain(summary.unknownTrafficNodes), ['node-a']);
  assert.deepEqual(
    plain(summary.nodeTotals.map((node) => [node.uuid, node.unknown])),
    [['node-a', true]],
  );
});

test('an unmeasured interval contributes nothing rather than a value', () => {
  // `fill_empty` turns a missing interval into a null, and an interval the panel
  // could not validate is simply absent. Either way it is not traffic.
  const points = [
    point(0, 1 * GIB),
    { time: new Date(BASE + 60_000).toISOString(), value: null },
    point(2, 2 * GIB),
    point(3, 4 * GIB),
  ];
  const summary = computeTrafficSummary(response([intervals('traffic.up', 'node-a', points)]));

  assert.equal(summary.totalUp, 7 * GIB);
  assert.equal(summary.points.length, 3);
  assert.deepEqual(plain(summary.unknownTrafficNodes), []);
});

test('rates are split by direction and never added to the byte totals', () => {
  const summary = computeTrafficSummary(
    response([
      series('net.out.rate', 'node-a', [point(0, 5), point(1, 9), point(2, 1)]),
      series('net.in.rate', 'node-a', [point(0, 3), point(1, 4), point(2, 6)]),
    ]),
  );

  assert.equal(summary.totalUp, 0);
  assert.equal(summary.totalDown, 0);
  assert.equal(summary.nodeTotals.length, 0);
  assert.deepEqual(
    plain(summary.points.map((p) => [p.upRate, p.downRate])),
    [[5, 3], [9, 4], [1, 6]],
  );
  // A rate bucket does not accumulate the way a byte total does.
  assert.deepEqual(
    plain(summary.points.map((p) => p.upCum)),
    [0, 0, 0],
  );
});

test('peak rate is the highest combined in+out rate per node', () => {
  const summary = computeTrafficSummary(
    response([
      intervals('traffic.up', 'node-a', [point(0, GIB)]),
      intervals('traffic.down', 'node-a', [point(0, GIB)]),
      series('net.out.rate', 'node-a', [point(0, 5), point(1, 20)]),
      series('net.in.rate', 'node-a', [point(0, 3), point(1, 4)]),
    ]),
  );

  const [node] = summary.nodeTotals;
  assert.equal(node.peakRate, 24);
  assert.equal(node.peakTime, BASE + 60_000);
});

test('the totals are ordered by traffic with unmeasurable nodes last', () => {
  const summary = computeTrafficSummary(
    response([
      intervals('traffic.up', 'small', [point(0, 1 * GIB)]),
      intervals('traffic.up', 'large', [point(0, 9 * GIB)]),
      series('traffic.up', 'unknown', [point(0, 25 * GIB)], 'billing_cycle_cumulative'),
    ]),
  );

  assert.deepEqual(
    plain(summary.nodeTotals.map((node) => node.uuid)),
    ['large', 'small', 'unknown'],
  );
});

test('a null response stays null so the card can show its loading state', () => {
  assert.equal(computeTrafficSummary(null), null);
});
