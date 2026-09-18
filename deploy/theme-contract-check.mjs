#!/usr/bin/env node
// Validate the live ip-info API against the *installed theme's own* zod schemas.
//
// Why this exists
// ---------------
// The IP 信息 tab in LuminaPlus did not render across four rounds of debugging while
// every server-side check passed. The theme parses each response with a strict zod
// schema, and one required field (`classification.source`) was missing. The HTTP status
// was 200, the JSON looked complete, and React Query swallowed the parse error -- so
// nothing anywhere said "invalid". Reasoning about the minified bundle predicted the
// gate should pass; it did not.
//
// This script removes the reasoning step. It reads the schemas straight out of whatever
// theme build is installed, runs them with the theme's own bundled zod, and prints every
// rejected field path. Re-run after any theme upgrade: the contract is re-derived each
// time, so it cannot drift out of date.
//
// Usage
// -----
//   node theme-contract-check.mjs --assets <theme>/dist/assets --base http://127.0.0.1:25774
//   node theme-contract-check.mjs --assets <theme>/dist/assets --base <url> --cookie "session_token=..."
//   node theme-contract-check.mjs --assets <theme>/dist/assets --base <url> --uuid U --ip 1.2.3.4
//   ... --refresh        also POST /api/admin/ip-info/v1/refresh (needs --cookie)
//
// --cookie is only needed to auto-discover node addresses: they are admin-only, because
// the public node list blanks ipv4/ipv6 on purpose. Without it, pass --uuid/--ip.
//
// Exit code is 1 if any payload is rejected, so it can gate a deploy.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

// The schemas are a contiguous run: the zod definitions, then two small consumers. The
// anchors are the first declaration (`J`, the nullable-string primitive every later
// schema reuses) and the function that follows the last one. A future theme build may
// rename them -- that is what to update, and the script says so when it cannot find them.
const BLOCK_START = 'var J=d().nullable().default(null)';
const BLOCK_END = 'async function an(';

// Names the rest of the theme imports from this block, and the endpoint each validates.
const SCHEMAS = {
  Zt: 'GET /api/public/ip-info/v1/status',
  en: 'GET /api/public/ip-info/v1/lookup',
  tn: 'GET /api/public/ip-info/v1/latency',
  rn: 'POST /api/admin/ip-info/v1/refresh',
};

function parseArgs(argv) {
  const out = { base: 'http://127.0.0.1:25774' };
  for (let i = 2; i < argv.length; i++) {
    const key = argv[i];
    if (!key.startsWith('--')) continue;
    const name = key.slice(2);
    out[name] = argv[i + 1] && !argv[i + 1].startsWith('--') ? argv[++i] : 'true';
  }
  if (!out.assets) {
    console.error('missing --assets <theme>/dist/assets');
    process.exit(2);
  }
  return out;
}

function fail(message) {
  console.error(`\n  ✗ ${message}`);
  process.exit(1);
}

// Find the chunk holding the schemas and pull the block out verbatim.
function extractSchemaBlock(assetsDir) {
  for (const chunk of fs.readdirSync(assetsDir).filter((f) => f.endsWith('.js'))) {
    const text = fs.readFileSync(path.join(assetsDir, chunk), 'utf8');
    const start = text.indexOf(BLOCK_START);
    if (start === -1) continue;
    const end = text.indexOf(BLOCK_END, start);
    if (end === -1) fail(`found ${BLOCK_START} in ${chunk} but not the ${BLOCK_END} anchor`);
    return { chunk, block: text.slice(start, end) };
  }
  return fail(`no chunk under ${assetsDir} contains the schema anchor ${BLOCK_START}`);
}

// Build an importable module: the theme's zod chunk plus the extracted schemas. Node
// decides ESM vs CJS from the nearest package.json and a theme dist has none, so the
// chunks are copied next to a type:module marker rather than imported in place.
//
// The schemas are collected behind `typeof` guards: a name that a future build renames
// reports as absent instead of throwing at import time, and the rest still get checked.
function buildProbe(assetsDir, block) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'theme-contract-'));
  for (const file of fs.readdirSync(assetsDir)) {
    if (file.endsWith('.js')) fs.copyFileSync(path.join(assetsDir, file), path.join(dir, file));
  }
  fs.writeFileSync(path.join(dir, 'package.json'), '{"type":"module"}');

  const header = "import{a as u,c as d,i as f,l as p,n as m,o as h,r as g,s as _,t as v}" +
    " from './validation-FdTlrw-l.js';\n";
  const collector = Object.keys(SCHEMAS)
    .map((name) => `  ${name}: typeof ${name} === 'undefined' ? null : ${name},`)
    .join('\n');
  fs.writeFileSync(
    path.join(dir, 'probe.mjs'),
    `${header}${block}\nconst __schemas = {\n${collector}\n};\nexport { __schemas };\n`,
  );
  return dir;
}

async function get(base, route, cookie) {
  const response = await fetch(base + route, {
    headers: { Accept: 'application/json', ...(cookie ? { Cookie: cookie } : {}) },
  });
  const text = await response.text();
  let body;
  try {
    body = JSON.parse(text);
  } catch {
    fail(`${route} did not return JSON (HTTP ${response.status}): ${text.slice(0, 200)}`);
  }
  return { status: response.status, body };
}

async function rpc(base, cookie, method, params = {}) {
  const response = await fetch(`${base}/api/rpc2`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json', Cookie: cookie },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }),
  });
  const body = await response.json();
  if (body.error) fail(`${method} failed: ${body.error.message}`);
  return body.result;
}

// The theme calls gn(uuid, node.ipv4, node.ipv6, node.region, loggedIn) and hides the tab
// unless at least one address survives its own schema. Mirror that: one target per address.
async function resolveTargets(args) {
  if (args.uuid && args.ip) return [{ uuid: args.uuid, ip: args.ip }];
  if (!args.cookie) {
    fail('node addresses are admin-only: pass --cookie "session_token=..." or explicit --uuid/--ip');
  }
  const result = await rpc(args.base, args.cookie, 'common:getNodes');
  const nodes = Array.isArray(result) ? result : Object.values(result || {});
  const targets = [];
  for (const node of nodes) {
    for (const ip of [node.ipv4, node.ipv6]) {
      if (ip) targets.push({ uuid: node.uuid, ip, name: node.name, region: node.region });
    }
  }
  if (!targets.length) fail('common:getNodes returned no node with an address');
  return targets;
}

async function main() {
  const args = parseArgs(process.argv);
  const { chunk, block } = extractSchemaBlock(args.assets);
  const dir = buildProbe(args.assets, block);
  const { __schemas: schemas } = await import(pathToFileURL(path.join(dir, 'probe.mjs')).href);

  const found = Object.keys(SCHEMAS).filter((name) => schemas[name]);
  console.log(`schema source : ${chunk}`);
  console.log(`schemas found : ${found.join(', ') || '(none)'}`);
  if (!found.length) fail('none of the expected schemas were found -- the theme build changed');
  console.log(`probe dir     : ${dir}`);

  const targets = await resolveTargets(args);
  console.log(`targets       : ${targets.length}`);

  // Each entry: label, route, and the schema that must accept the response.
  const checks = [{ label: 'status', route: '/api/public/ip-info/v1/status', schema: 'Zt' }];
  for (const target of targets) {
    const who = `${target.name || target.uuid} ${target.ip}`;
    const query = new URLSearchParams({ uuid: target.uuid, ip: target.ip }).toString();
    checks.push({ label: `lookup  ${who}`, route: `/api/public/ip-info/v1/lookup?${query}`, schema: 'en' });
    checks.push({ label: `latency ${who}`, route: `/api/public/ip-info/v1/latency?${query}`, schema: 'tn' });
  }
  if (args.refresh === 'true' && targets[0]) {
    if (!args.cookie) fail('--refresh needs --cookie');
    const response = await fetch(`${args.base}/api/admin/ip-info/v1/refresh`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Accept: 'application/json',
        Cookie: args.cookie,
      },
      body: JSON.stringify({ uuid: targets[0].uuid, ip: targets[0].ip, force: true, include_latency: false }),
    });
    checks.push({ label: `refresh ${targets[0].ip}`, schema: 'rn', preset: { status: response.status, body: await response.json() } });
  }

  let rejected = 0;
  const problems = new Map();
  for (const check of checks) {
    const schema = schemas[check.schema];
    if (!schema) {
      console.log(`  ? ${check.label}: theme has no ${check.schema} schema (${SCHEMAS[check.schema]}), skipped`);
      continue;
    }
    const { status, body } = check.preset || (await get(args.base, check.route, args.cookie));
    if (status !== 200) {
      rejected++;
      console.log(`\n  ✗ ${check.label}: HTTP ${status} ${JSON.stringify(body).slice(0, 160)}`);
      continue;
    }
    const parsed = schema.safeParse(body);
    if (parsed.success) {
      console.log(`  ✓ ${check.label}`);
      continue;
    }
    rejected++;
    console.log(`\n  ✗ ${check.label}: rejected by the theme's schema (${SCHEMAS[check.schema]})`);
    for (const issue of parsed.error.issues) {
      const field = issue.path.join('.');
      console.log(`      ${field || '(root)'} — ${issue.message}`);
      problems.set(field, (problems.get(field) || 0) + 1);
    }
  }

  console.log('');
  if (rejected) {
    console.log(`${rejected} of ${checks.length} payloads rejected by the installed theme.`);
    console.log('Distinct offending fields (fix these, not the HTTP status):');
    for (const [field, count] of problems) console.log(`  ${field}  (${count} payloads)`);
    process.exit(1);
  }
  console.log(`all ${checks.length} payloads accepted by the installed theme.`);
}

main().catch((error) => fail(error.stack || String(error)));
