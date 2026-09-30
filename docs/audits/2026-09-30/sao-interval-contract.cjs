"use strict";

const assert = require("node:assert/strict");
const { execFileSync } = require("node:child_process");
const vm = require("node:vm");

const remote = [
  "import pathlib,hashlib,json",
  "p=pathlib.Path('/opt/nekomari/data/theme/SAO/dist/assets/themeSettings-CYV_2lCd.js')",
  "b=p.read_bytes();s=b.decode()",
  "print(json.dumps({'sha256':hashlib.sha256(b).hexdigest(),'code':s[s.index('function Ee('):s.index('var j=`traffic.up`')]}))",
].join("\n") + "\n";
const asset = JSON.parse(execFileSync("ssh", [
  "-o", "BatchMode=yes", "-o", "ConnectTimeout=12", "OC424", "python3 -",
], { input: remote, encoding: "utf8", timeout: 30000, maxBuffer: 1024 * 1024 }));
assert.equal(asset.sha256, "6950e5729b960fd90465fca94e6fd27cb554e7dd34ed3ad7ec23c3f7ef8a3e66");
const base = [{
  metricKey: "traffic.up", client: "audit-node", intervalSeconds: 300,
  semantics: "interval_delta_v2", quality: "validated_intervals_only",
  points: [{ time: "2026-09-30T02:00:00Z", value: null, count: 0 }],
}];
const repair = [{
  metricKey: "traffic.up", client: "audit-node", intervalSeconds: 0,
  semantics: "interval_delta_v2", quality: "validated_intervals_only",
  points: [10, 10].map((value, index) => ({
    time: new Date(Date.UTC(2026, 8, 30, 2, 0, 15 + index * 10)).toISOString(),
    value, count: 1,
  })),
}];
const replay = (repair) => vm.runInNewContext(asset.code + '; Me(base,repair,{"traffic.up":"sum"})',
  { base, repair }, { timeout: 1000 });
const result = replay(repair);
assert.equal(result.series[0].points[0].value, 20);
assert.equal(result.series[0].points[0].count, 2);
const empty = replay([]);
assert.equal(empty.series[0].points[0].value, null);
assert.equal(empty.series[0].points[0].count, 0);
console.log(JSON.stringify({ sha256: asset.sha256, observed_delta: 20, repaired_delta: 20,
  count: 2, legacy_empty: null, scope: "Pinned deployed SAO pure-function contract; not live fixed-server acceptance" }, null, 2));
