import { readFileSync } from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { gzipSync } from 'node:zlib';
import ts from 'typescript';

function localAsset(root, name) {
  if (typeof name !== 'string' || !name || /[?#:%\\]/.test(name) ||
      name.split('/').some(part => ['', '.', '..'].includes(part))) {
    throw new Error(`Expected a relative build asset: ${name}`);
  }
  return readFileSync(path.join(root, name));
}

function sizes(buffer) {
  return { rawBytes: buffer.length, gzipBytes: gzipSync(buffer, { level: 9 }).length };
}

function entry(root, document) {
  const manifest = JSON.parse(localAsset(root, '.vite/manifest.json'));
  const asset = manifest[document];
  if (asset?.isEntry !== true || !asset.file?.endsWith('.js')) throw new Error(`Missing JS entry: ${document}`);
  return { file: asset.file, ...sizes(localAsset(root, asset.file)) };
}

function precache(root) {
  const worker = ts.createSourceFile('sw.js', localAsset(root, 'sw.js').toString(), ts.ScriptTarget.Latest, false, ts.ScriptKind.JS);
  const calls = [];
  function visit(node) {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) &&
        node.expression.name.text === 'precacheAndRoute') calls.push(node);
    ts.forEachChild(node, visit);
  }
  visit(worker);
  const entries = calls[0]?.arguments[0];
  if (worker.parseDiagnostics.length || calls.length !== 1 || !entries || !ts.isArrayLiteralExpression(entries) || !entries.elements.length) {
    throw new Error('Expected one precacheAndRoute call with a nonempty literal array');
  }
  const urls = new Map();
  for (const item of entries.elements) {
    if (!ts.isObjectLiteralExpression(item)) throw new Error('Expected a literal precache entry');
    const fields = new Map();
    for (const field of item.properties) {
      if (!ts.isPropertyAssignment(field) || !(ts.isIdentifier(field.name) || ts.isStringLiteral(field.name)) || fields.has(field.name.text)) {
        throw new Error('Expected unambiguous literal precache properties');
      }
      fields.set(field.name.text, field.initializer);
    }
    const url = fields.get('url'), revision = fields.get('revision');
    if (!url || !ts.isStringLiteral(url)) throw new Error('Expected a literal precache URL');
    if (revision && revision.kind !== ts.SyntaxKind.NullKeyword && !ts.isStringLiteral(revision)) throw new Error('Expected a literal precache revision');
    const name = url.text.replace(/^(?:\.\/)+/, '');
    const value = revision && ts.isStringLiteral(revision) ? revision.text : null;
    if (urls.has(name) && urls.get(name) !== value) throw new Error(`Conflicting revisions: ${name}`);
    urls.set(name, value);
  }
  let rawBytes = 0, gzipBytes = 0;
  for (const url of urls.keys()) {
    const size = sizes(localAsset(root, url));
    rawBytes += size.rawBytes;
    gzipBytes += size.gzipBytes;
  }
  return { distinctUrls: urls.size, rawBytes, gzipBytes };
}

export function assetBudget(root) {
  return {
    publicEntry: entry(root, 'index.html'),
    adminEntry: entry(path.join(root, 'standalone/admin'), 'admin.html'),
    publicPrecache: precache(root),
  };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  console.log(JSON.stringify(assetBudget(process.argv[2] ?? 'dist'), null, 2));
}
