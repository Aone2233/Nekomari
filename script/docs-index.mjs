#!/usr/bin/env node
// Documentation index and link check.
//
// Two things had drifted and nobody noticed: two relative links in a dated
// audit note pointed at files that are one directory up, and README.md's table
// covered 19 of the 80 documents in docs/ while calling itself the index. Both
// are invisible in review and only cost a reader's time. This checks them.
//
// Usage:
//   node script/docs-index.mjs          check, exit 1 on a problem
//   node script/docs-index.mjs --write  rewrite the generated block first
//
// The generated block lists the documents README.md does not otherwise mention,
// with each document's own first heading as its label, so nothing here invents
// a description.

import { readdirSync, readFileSync, statSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join, relative, resolve, sep } from "node:path";

const root = resolve(import.meta.dirname, "..");
const readmePath = join(root, "README.md");
const START = "<!-- docs-index:start -->";
const END = "<!-- docs-index:end -->";

function walk(dir, out = []) {
  for (const name of readdirSync(dir).sort()) {
    const path = join(dir, name);
    const info = statSync(path);
    if (info.isDirectory()) {
      walk(path, out);
    } else if (name.endsWith(".md")) {
      out.push(path);
    }
  }
  return out;
}

// Links inside fenced code blocks are examples, not references.
function stripFences(text) {
  return text.replace(/^```[\s\S]*?^```/gm, "");
}

const LINK = /\[[^\]]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g;

function linksIn(text) {
  const links = [];
  for (const match of stripFences(text).matchAll(LINK)) {
    const target = match[1];
    if (/^(?:[a-z][a-z0-9+.-]*:|\/\/|\/|#)/i.test(target)) continue;
    links.push(target.split("#")[0]);
  }
  return links.filter(Boolean);
}

const problems = [];

// 1. Every relative link in the repository's markdown resolves.
const markdown = [
  ...walk(join(root, "docs")),
  ...["README.md", "CONTRIBUTING.md", "CHANGELOG.md", "FORK.md"]
    .map((name) => join(root, name))
    .filter((path) => existsSync(path)),
];
for (const path of markdown) {
  const text = readFileSync(path, "utf8");
  for (const target of linksIn(text)) {
    const resolved = resolve(dirname(path), decodeURIComponent(target));
    if (!existsSync(resolved)) {
      problems.push(`${relative(root, path)} links to ${target}, which does not exist`);
    }
  }
}

// 2. Every document in docs/ is reachable from README.md.
const docs = readdirSync(join(root, "docs"))
  .filter((name) => name.endsWith(".md"))
  .sort();
let readme = readFileSync(readmePath, "utf8");

function splitBlock(text) {
  const start = text.indexOf(START);
  const end = text.indexOf(END);
  if (start === -1 || end === -1 || end < start) return null;
  return { before: text.slice(0, start + START.length), block: text.slice(start + START.length, end), after: text.slice(end) };
}

function heading(path) {
  const text = readFileSync(path, "utf8");
  const line = text.split("\n").find((l) => l.startsWith("# "));
  return line ? line.replace(/^#\s+/, "").trim() : relative(root, path);
}

// Documents the generated block has to list: those README does not mention
// anywhere outside the block itself.
function unlistedDocs(text) {
  const parts = splitBlock(text);
  const body = parts ? parts.before.replace(START, "") + parts.after.replace(END, "") : text;
  return docs.filter((name) => !body.includes(`docs/${name}`));
}

// Documents a reader cannot reach from README at all, block included.
function unreadableDocs(text) {
  return docs.filter((name) => !text.includes(`docs/${name}`));
}

function renderBlock(missing) {
  return missing.map((name) => `- [${heading(join(root, "docs", name))}](./docs/${name})`).join("\n");
}

const write = process.argv.includes("--write");
const parts = splitBlock(readme);

if (write) {
  if (!parts) {
    console.error(`README.md is missing ${START} / ${END}; add them where the generated list belongs.`);
    process.exit(1);
  }
  const missing = unlistedDocs(readme);
  const block = `\n${renderBlock(missing)}\n`;
  readme = parts.before + block + parts.after;
  writeFileSync(readmePath, readme);
  console.log(`wrote ${missing.length} entries into README.md`);
} else {
  for (const name of unreadableDocs(readme)) {
    problems.push(`docs/${name} is not linked from README.md (run: node script/docs-index.mjs --write)`);
  }
  if (parts) {
    const expected = renderBlock(unlistedDocs(readme));
    if (parts.block.trim() !== expected.trim()) {
      problems.push("the generated list in README.md is stale (run: node script/docs-index.mjs --write)");
    }
  }
}

if (problems.length) {
  console.error(`documentation check failed (${problems.length}):`);
  for (const problem of problems) console.error(`  - ${problem}`);
  process.exit(1);
}
console.log(`documentation check passed: ${markdown.length} files, ${docs.length} documents`);
