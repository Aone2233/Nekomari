import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import ts from 'typescript';

// Exercise the real module. It imports types only, so the transpiled CommonJS has
// no runtime dependencies and can run in a bare vm context.
const source = readFileSync(new URL('../src/utils/metricSeries.ts', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

const exports = {};
vm.runInNewContext(compiled, { module: { exports }, exports, console });
const {
  pingMetricStatKey,
  pingSeriesFamily,
  metricSeriesDataKey,
  metricSeriesKey,
  metricTagsKey,
} = exports;

test('a statistic key keeps its legacy shape when no family is reported', () => {
  // Old agents do not report a family and historical data has no `family` tag.
  // Those keys must not change, or every existing deployment's statistics would
  // be recomputed under a new dimension.
  assert.equal(pingMetricStatKey('node-a', '7'), 'node-a:7');
  assert.equal(pingMetricStatKey('node-a', '7', ''), 'node-a:7');
  assert.equal(pingMetricStatKey('node-a', '7', '   '), 'node-a:7');
});

test('a statistic key separates the two address families', () => {
  const v4 = pingMetricStatKey('node-a', '7', 'ipv4');
  const v6 = pingMetricStatKey('node-a', '7', 'ipv6');
  assert.notEqual(v4, v6);
  assert.equal(v4, 'node-a:7:ipv4');
  assert.equal(v6, 'node-a:7:ipv6');
});

test('the family is read from a series tag, and absent tags yield undefined', () => {
  assert.equal(pingSeriesFamily({ task_id: '7', family: 'ipv6' }), 'ipv6');
  assert.equal(pingSeriesFamily({ task_id: '7' }), undefined);
  assert.equal(pingSeriesFamily({ task_id: '7', family: '' }), undefined);
  assert.equal(pingSeriesFamily(undefined), undefined);
});

test('two families of one task become two chart series instead of one', () => {
  // This is the whole point of the change. The chart identifies a series by its
  // tags; if the family were not part of that identity the second family's points
  // would land on the first family's data key and silently replace them — the
  // chart would show one line and one mixed loss number.
  const v4 = { task_id: '7', family: 'ipv4' };
  const v6 = { task_id: '7', family: 'ipv6' };

  assert.notEqual(metricTagsKey(v4), metricTagsKey(v6));
  assert.notEqual(
    metricSeriesDataKey('ping.latency_ms', v4),
    metricSeriesDataKey('ping.latency_ms', v6),
  );
  assert.notEqual(
    metricSeriesKey('ping.latency_ms', v4),
    metricSeriesKey('ping.latency_ms', v6),
  );

  // The same family is still the same series, so points accumulate rather than
  // fragmenting.
  assert.equal(
    metricSeriesDataKey('ping.latency_ms', { task_id: '7', family: 'ipv4' }),
    metricSeriesDataKey('ping.latency_ms', v4),
  );
});

test('the other ping dimensions keep working alongside the family', () => {
  // protocol (icmp/tcp) and role (reference) already split series; the family must
  // be an additional dimension, not a replacement.
  const base = { task_id: '7', protocol: 'icmp' };
  assert.notEqual(
    metricSeriesDataKey('ping.latency_ms', { ...base, family: 'ipv4' }),
    metricSeriesDataKey('ping.latency_ms', { ...base, family: 'ipv6' }),
  );
  assert.notEqual(
    metricSeriesDataKey('ping.latency_ms', { ...base, family: 'ipv4' }),
    metricSeriesDataKey('ping.latency_ms', { ...base, role: 'reference', family: 'ipv4' }),
  );
  // Tag order must not matter.
  assert.equal(
    metricSeriesDataKey('ping.latency_ms', { task_id: '7', family: 'ipv4', protocol: 'icmp' }),
    metricSeriesDataKey('ping.latency_ms', base.task_id ? { protocol: 'icmp', family: 'ipv4', task_id: '7' } : {}),
  );
});
