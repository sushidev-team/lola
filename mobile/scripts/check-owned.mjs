// Type-checks only the paths a work item owns.
//
// `npm run check` fails on the whole of mobile/ until the superseded client
// modules are gone, so an item cannot use it as a gate. This runs the same
// svelte-check, reads its machine output, and fails only on an ERROR whose file
// lives under one of the given paths (relative to mobile/). Warnings and errors
// elsewhere never fail it.
//
//   npm run check:owned -- src/wire src/lib/foo.ts
import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

// parseMachine reads `svelte-check --output machine` lines:
//   <ms> ERROR "<file>" <line>:<col> "<message>"
// Anything that is not an ERROR record (START, WARNING, COMPLETED, noise) is skipped.
export function parseMachine(text) {
  const out = [];
  const re = /^\S+\s+ERROR\s+"((?:[^"\\]|\\.)*)"\s+(\d+):(\d+)\s+"((?:[^"\\]|\\.)*)"/;
  for (const line of String(text).split(/\r?\n/)) {
    const m = re.exec(line);
    if (!m) continue;
    out.push({ file: m[1], line: Number(m[2]), col: Number(m[3]), message: m[4], raw: line });
  }
  return out;
}

// under reports whether file (relative to root, as svelte-check prints it) is
// the owned path or inside it. Compared on resolved path segments, so "src/ui"
// does not claim "src/uikit/a.ts".
export function under(file, owned, root) {
  const f = path.resolve(root, file);
  const o = path.resolve(root, owned);
  const rel = path.relative(o, f);
  return rel === "" || (!rel.startsWith("..") && !path.isAbsolute(rel));
}

export function owned(errors, paths, root) {
  return errors.filter((e) => paths.some((p) => under(e.file, p, root)));
}

function main(argv) {
  const paths = argv.filter((a) => a !== "--");
  if (paths.length === 0) {
    console.error("usage: npm run check:owned -- <path under mobile/>...");
    return 2;
  }
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const r = spawnSync(
    "npx",
    ["svelte-check", "--tsconfig", "./tsconfig.json", "--output", "machine"],
    { cwd: root, encoding: "utf8", maxBuffer: 64 * 1024 * 1024 },
  );
  if (r.error) {
    console.error(`svelte-check did not run: ${r.error.message}`);
    return 2;
  }
  const text = `${r.stdout ?? ""}\n${r.stderr ?? ""}`;
  const hits = owned(parseMachine(text), paths, root);
  if (hits.length === 0) {
    // A crash prints no ERROR records; do not call it clean.
    if (r.status !== 0 && r.status !== 1 && !/COMPLETED/.test(text)) {
      console.error(`svelte-check failed (exit ${r.status}) without a report`);
      return 2;
    }
    return 0;
  }
  for (const h of hits) console.log(h.raw);
  console.error(`${hits.length} type error(s) in owned paths`);
  return 1;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exit(main(process.argv.slice(2)));
}
