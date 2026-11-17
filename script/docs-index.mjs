#!/usr/bin/env node
// Documentation index and link check.
//
// Two things had drifted and nobody noticed: two relative links in a dated
// audit note pointed one directory too high, and README.md's table covered 19
// of the 80 top-level documents in docs/ while calling itself the index. Both
// are invisible in review and cost a reader's time. This checks them.
//
// Usage:
//   node script/docs-index.mjs          check, exit 1 on a problem
//   node script/docs-index.mjs --write  regenerate the list, then check
//
// The generated block lists the documents README.md does not link anywhere
// else, labelled with each document's own first heading, so nothing here
// invents a description. Dated records under docs/audits/ are checked for dead
// links but are not listed: they are snapshots, not a map.

import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, readdirSync, renameSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const readmePath = join(root, "README.md");
const START = "<!-- docs-index:start -->";
const END = "<!-- docs-index:end -->";

const problems = [];
const linkedPaths = new Set();
// Links outside the generated block: a document that only the block links is
// still "not linked elsewhere" and must stay in the block.
const linkedOutsideBlock = new Set();

// Tracked markdown only: build output and node_modules are not documentation,
// and `git ls-files` is what reviewers and CI both see.
function trackedMarkdown() {
  const out = execFileSync("git", ["ls-files", "-z", "*.md"], { cwd: root, encoding: "utf8" });
  return out.split("\0").filter(Boolean).map((path) => join(root, path));
}

// Text that looks like a link but is not one: fenced blocks, HTML comments and
// inline code. Removing it keeps examples and notes from failing the check.
function stripNonProse(text) {
  return text
    .replace(/^ {0,3}(```|~~~)[^\n]*\n[\s\S]*?^ {0,3}\1[^\n]*$/gm, "")
    .replace(/<!--[\s\S]*?-->/g, "")
    .replace(/`[^`\n]*`/g, "");
}

const INLINE_LINK = /\[[^\]]*\]\(\s*(<[^>\n]*>|[^)\s]+)(?:\s+("[^"]*"|'[^']*'|\([^)]*\)))?\s*\)/g;
const REFERENCE_DEFINITION = /^ {0,3}\[[^\]]+\]:\s*(<[^>\n]*>|\S+)/gm;

function targetsIn(text) {
  const prose = stripNonProse(text);
  const targets = [];
  for (const match of prose.matchAll(INLINE_LINK)) targets.push(match[1]);
  for (const match of prose.matchAll(REFERENCE_DEFINITION)) targets.push(match[1]);
  return targets
    .map((target) => target.replace(/^<|>$/g, "").split("#")[0])
    .filter((target) => target && !/^(?:[a-z][a-z0-9+.-]*:|\/\/|\/)/i.test(target));
}

function checkFile(path, text, extraSet, report = true) {
  for (const target of targetsIn(text)) {
    let decoded = target;
    try {
      decoded = decodeURIComponent(target);
    } catch {
      if (report) problems.push(`${relative(root, path)} has a malformed link target: ${target}`);
      continue;
    }
    const resolved = resolve(dirname(path), decoded);
    if (!existsSync(resolved)) {
      if (report) problems.push(`${relative(root, path)} links to ${target}, which does not exist`);
    } else {
      linkedPaths.add(resolved);
      if (extraSet) extraSet.add(resolved);
    }
  }
}

function splitBlock(text) {
  const start = text.indexOf(START);
  const end = text.indexOf(END);
  if (start === -1 || end === -1 || end < start) return null;
  if (text.indexOf(START, start + START.length) !== -1) return null;
  if (text.indexOf(END, end + END.length) !== -1) return null;
  return { block: text.slice(start + START.length, end) };
}

function heading(path) {
  const text = stripNonProse(readFileSync(path, "utf8").replace(/^---\n[\s\S]*?\n---\n/, ""));
  const line = text.split("\n").find((candidate) => candidate.startsWith("# "));
  return (line ? line.replace(/^#\s+/, "") : relative(root, path)).trim();
}

const docs = readdirSync(join(root, "docs"))
  .filter((name) => name.endsWith(".md"))
  .sort()
  .map((name) => ({ name, path: join(root, "docs", name) }));

let readme = readFileSync(readmePath, "utf8");
let parts = splitBlock(readme);

// The generated block's own entries must not count as coverage, or every
// document it lists would look linked and the list could never be rebuilt.
for (const path of trackedMarkdown()) {
  const text = readFileSync(path, "utf8");
  if (path === readmePath && parts) {
    checkFile(path, text);
    checkFile(path, text.replace(parts.block, ""), linkedOutsideBlock, false);
  } else {
    checkFile(path, text);
  }
}

const unlinked = docs.filter((doc) => !linkedOutsideBlock.has(doc.path));
const unreachable = docs.filter((doc) => !linkedPaths.has(doc.path));
const render = (list) => list.map((doc) => `- [${heading(doc.path)}](./docs/${doc.name})`).join("\n");

if (process.argv.includes("--write")) {
  // Never rewrite a file that already has dead links: the writer would look as
  // if it had fixed something.
  if (problems.length) {
    console.error(`refusing to write: ${problems.length} link problem(s) first`);
    for (const problem of problems) console.error(`  - ${problem}`);
    process.exit(1);
  }
  if (!parts) {
    console.error(`README.md is missing ${START} / ${END}, or has them out of order.`);
    process.exit(1);
  }
  const temp = `${readmePath}.tmp`;
  writeFileSync(temp, readme.replace(parts.block, `\n${render(unlinked)}\n`));
  renameSync(temp, readmePath);
  console.log(`wrote ${unlinked.length} entries into README.md`);
  readme = readFileSync(readmePath, "utf8");
  parts = splitBlock(readme);
}

for (const doc of unreachable) {
  problems.push(`docs/${doc.name} is not linked from README.md (run: node script/docs-index.mjs --write)`);
}
if (!parts) {
  problems.push(`README.md is missing ${START} / ${END}, or has them out of order`);
} else if (parts.block.trim() !== render(unlinked).trim()) {
  problems.push("the generated list in README.md is stale (run: node script/docs-index.mjs --write)");
}

if (problems.length) {
  console.error(`documentation check failed (${problems.length}):`);
  for (const problem of problems) console.error(`  - ${problem}`);
  process.exit(1);
}
console.log(`documentation check passed: ${trackedMarkdown().length} markdown files, ${docs.length} documents in docs/`);
