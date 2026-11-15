import { describe, expect, it } from "vitest";
import type { SessionInfo } from "$lib/store.svelte";
import { BUCKETS, inboxItems, ItemCache, rowModel } from "./inbox.svelte";
import { inboxSearch, matchesModel, searchBuckets } from "./inboxsearch.svelte";

const s = (id: string, over: Partial<SessionInfo> = {}): SessionInfo =>
  ({ id, project: "nori-app", issue: id.toUpperCase(), title: `t ${id}`, branch: `lola/${id}`, agentState: "working", delivery: "none", age: "1m", prNumber: 0, ...over }) as SessionInfo;

const label = (p: string): string => ({ "nori-app": "Nori App", okane: "Okane" })[p] ?? p;

function buckets(list: SessionInfo[]) {
  const out = { needs: [], working: [], review: [], done: [] } as Record<(typeof BUCKETS)[number], ReturnType<typeof rowModel>[]>;
  for (const x of list) {
    const m = rowModel(x);
    out[m.bucket].push(m);
  }
  return out;
}

describe("matchesModel", () => {
  const m = rowModel(s("a1", { title: "Fix login redirect", issue: "NOR-12", branch: "lola/nor-12", project: "okane" }));
  it("matches title, issue key, branch and project name", () => {
    expect(matchesModel(m, "login", "Okane")).toBe(true);
    expect(matchesModel(m, "nor-12", "Okane")).toBe(true);
    expect(matchesModel(m, "lola/nor", "Okane")).toBe(true);
    expect(matchesModel(m, "okane", "Okane")).toBe(true);
  });
  it("needs every word to fit one field", () => {
    expect(matchesModel(m, "login nor-12", "Okane")).toBe(true);
    expect(matchesModel(m, "login zzz", "Okane")).toBe(false);
  });
});

describe("searchBuckets", () => {
  const list = [
    s("a", { title: "Fix login", issue: "NOR-1" }),
    s("b", { title: "Rework billing", issue: "OKA-2", project: "okane", agentState: "waiting_input" }),
    s("c", { title: "Docs", issue: "NOR-3", agentState: "idle", delivery: "merged" }),
  ];

  it("returns the same object for a blank query", () => {
    const b = buckets(list);
    expect(searchBuckets(b, "  ", label)).toBe(b);
  });

  it("narrows every bucket and keeps matching models by identity", () => {
    const b = buckets(list);
    const out = searchBuckets(b, "nori", label);
    expect(out.working[0]).toBe(b.working[0]);
    expect(out.done[0]).toBe(b.done[0]);
    expect(out.needs).toEqual([]);
  });

  it("composes with the status filter and with grouping", () => {
    const b = buckets(list);
    const narrowed = searchBuckets(b, "nor", label);
    const filtered = inboxItems(narrowed, "done", false, new ItemCache());
    expect(filtered.map((i) => i.key)).toEqual(["row:c"]);
    const grouped = inboxItems(narrowed, "", false, new ItemCache(), { order: ["nori-app", "okane"], label });
    expect(grouped.map((i) => i.key).filter((k) => k !== "clear")).toEqual(["group:nori-app", "row:a", "row:c"]);
  });
});

describe("inboxSearch", () => {
  it("is active only for a non-blank query and clears", () => {
    inboxSearch.query = "  ";
    expect(inboxSearch.active).toBe(false);
    inboxSearch.query = "x";
    expect(inboxSearch.active).toBe(true);
    inboxSearch.clear();
    expect(inboxSearch.query).toBe("");
  });
});
