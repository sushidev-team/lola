import test from "node:test";
import assert from "node:assert/strict";
import { parseMachine, owned, under } from "./check-owned.mjs";

const root = "/repo/mobile";
const sample = [
  '1794675892793 START "/repo/mobile"',
  '1794675892794 ERROR "../desktop/frontend/src/lib/store.svelte.ts" 486:58 "Expected 2 arguments, but got 3."',
  '1794675892794 ERROR "src/wire/codec.ts" 3:1 "Cannot find \\"x\\""',
  '1794675892794 WARNING "src/wire/codec.ts" 9:1 "unused"',
  '1794675892794 ERROR "src/uikit/a.ts" 1:1 "bad"',
  '1794675892794 ERROR "src/views/Old.svelte" 7:2 "gone"',
  "1794675892795 COMPLETED 4 FILES 3 ERRORS 1 WARNINGS 2 FILES_WITH_PROBLEMS",
].join("\n");

test("parses only ERROR records", () => {
  const e = parseMachine(sample);
  assert.equal(e.length, 4);
  assert.deepEqual([e[1].file, e[1].line, e[1].col], ["src/wire/codec.ts", 3, 1]);
  assert.equal(e[1].message, 'Cannot find \\"x\\"');
});

test("owned matches a directory and a file, by segment", () => {
  const e = parseMachine(sample);
  assert.deepEqual(owned(e, ["src/wire"], root).map((x) => x.file), ["src/wire/codec.ts"]);
  assert.deepEqual(owned(e, ["src/ui"], root), []);
  assert.deepEqual(owned(e, ["src/views/Old.svelte"], root).length, 1);
  assert.deepEqual(owned(e, ["src/lib"], root), []);
});

test("paths outside mobile/ resolve and match", () => {
  assert.ok(under("../desktop/frontend/src/lib/store.svelte.ts", "../desktop/frontend/src/lib", root));
  assert.ok(!under("src/a.ts", "src/b.ts", root));
});

test("empty and garbage input yield nothing", () => {
  assert.deepEqual(parseMachine(""), []);
  assert.deepEqual(parseMachine("npm warn something\nERROR nope"), []);
});
