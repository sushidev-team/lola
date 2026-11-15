// Bundle budget: xterm and the diff viewer stay out of the entry chunk's static
// import graph, and gzip sizes stay under their ceilings.
//
//   npm run build && npm run budget
//
// Reads dist/.vite/manifest.json (build.manifest in vite.config.ts). Every named
// chunk must be emitted and is held to its ceiling. The session screen counts as
// absent when something imports Session.svelte statically (the bundler then
// folds it into a shared chunk with no source); that is a failure, because the
// screen would no longer be measured.
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";

const KB = 1024;
export const LIMITS = { entry: 220 * KB, session: 260 * KB, terminal: 200 * KB, diff: 90 * KB };
const FORBIDDEN = /@xterm[\\/]|DiffView/;
const XTERM = /@xterm[\\/]/;
const CONVERSATION_SRC = /src[\\/]screens[\\/]session[\\/]ConversationSegment\.svelte$/;
const SESSION_SRC = /src[\\/]screens[\\/]session[\\/]Session\.svelte$/;

const isForbidden = (c) =>
  c.name === "terminal" || c.name === "diff" || FORBIDDEN.test(c.src ?? "") || FORBIDDEN.test(c.file ?? "");

// staticGraph returns the chunks reachable from key through `imports` only.
// Dynamic imports are lazy chunks and are not part of first paint.
export function staticGraph(manifest, key) {
  const seen = new Set();
  const walk = (k) => {
    if (seen.has(k) || !manifest[k]) return;
    seen.add(k);
    for (const i of manifest[k].imports ?? []) walk(i);
  };
  walk(key);
  return [...seen];
}

// check returns the list of violations for a manifest; sizeOf(file) is the gzip
// byte size of an emitted file.
export function check(manifest, sizeOf) {
  const problems = [];
  const report = [];
  const entries = Object.keys(manifest).filter((k) => manifest[k].isEntry);
  if (entries.length === 0) return { problems: ["manifest has no entry chunk"], report };

  for (const key of entries) {
    const graph = staticGraph(manifest, key);
    for (const k of graph) {
      if (isForbidden(manifest[k])) problems.push(`entry ${key} statically imports ${k} (${manifest[k].file})`);
    }
    const total = graph.reduce((n, k) => n + sizeOf(manifest[k].file), 0);
    report.push(["entry " + key, total, LIMITS.entry]);
    if (total > LIMITS.entry) problems.push(`entry ${key}: ${kb(total)} > ${kb(LIMITS.entry)}`);
  }

  const chunks = Object.entries(manifest).map(([k, c]) => ({ k, ...c }));

  // The default segment must not pull xterm in statically either: the Inbox
  // prefetches it about a second after launch.
  const conv = chunks.find((c) => CONVERSATION_SRC.test(c.src ?? c.k));
  if (!conv) problems.push("ConversationSegment chunk is not emitted on its own");
  else
    for (const k of staticGraph(manifest, conv.k)) {
      const c = manifest[k];
      if (c.name === "terminal" || XTERM.test(c.src ?? "") || XTERM.test(c.file ?? ""))
        problems.push(`ConversationSegment statically imports ${k} (${c.file})`);
    }
  const named = {
    session: chunks.find((c) => SESSION_SRC.test(c.src ?? c.k)),
    terminal: chunks.find((c) => c.name === "terminal"),
    diff: chunks.find((c) => c.name === "diff"),
  };
  for (const [name, c] of Object.entries(named)) {
    if (!c) {
      report.push([name + " (absent)", 0, LIMITS[name]]);
      problems.push(`${name} chunk is not emitted on its own (a static import folded it into a shared chunk?)`);
      continue;
    }
    const size = sizeOf(c.file);
    report.push([name, size, LIMITS[name]]);
    if (size > LIMITS[name]) problems.push(`${name} chunk: ${kb(size)} > ${kb(LIMITS[name])}`);
  }
  return { problems, report };
}

const kb = (n) => `${(n / KB).toFixed(1)} KB`;

function main() {
  const dist = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "dist");
  let manifest;
  try {
    manifest = JSON.parse(readFileSync(path.join(dist, ".vite", "manifest.json"), "utf8"));
  } catch (e) {
    console.error(`budget: cannot read dist/.vite/manifest.json (run npm run build): ${e.message}`);
    process.exit(1);
  }
  const sizeOf = (file) => gzipSync(readFileSync(path.join(dist, file))).length;
  const { problems, report } = check(manifest, sizeOf);
  for (const [name, size, limit] of report) console.log(`${name.padEnd(48)} ${kb(size).padStart(10)} / ${kb(limit)}`);
  if (problems.length) {
    for (const p of problems) console.error(`budget: ${p}`);
    process.exit(1);
  }
  console.log("budget: ok");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
