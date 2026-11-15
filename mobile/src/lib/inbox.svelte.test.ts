import { describe, expect, it, vi } from "vitest";
import type { SessionInfo } from "$lib/store.svelte";
import {
  approveChoice,
  DONE_VISIBLE,
  InboxModel,
  inboxItems,
  ItemCache,
  MAX_INBOX_TALK_WATCH,
  metaLine,
  metaParts,
  rowModel,
  type RowModel,
} from "./inbox.svelte";

function s(id: string, over: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id,
    project: "p",
    issue: id.toUpperCase(),
    title: `t ${id}`,
    branch: "b",
    agentState: "working",
    delivery: "none",
    age: "1m",
    prNumber: 0,
    ...over,
  } as SessionInfo;
}

const prompt = (labels: string[], kind = "permission") => ({
  kind,
  hash: "h",
  lines: [],
  choices: labels.map((label, i) => ({ key: String(i + 1), label })),
});

describe("approveChoice", () => {
  it("accepts one exact Yes on a permission prompt", () => {
    expect(approveChoice(prompt(["Yes", "No"]))?.key).toBe("1");
    expect(approveChoice(prompt([" Yes ", "No"]))?.key).toBe("1");
  });
  it("refuses everything else", () => {
    expect(approveChoice(prompt(["Yes, and don't ask again", "No"]))).toBeNull();
    expect(approveChoice(prompt(["Yes", "Yes"]))).toBeNull();
    expect(approveChoice(prompt(["Yes", "No"], "question"))).toBeNull();
    expect(approveChoice(prompt(["yes"]))).toBeNull();
    expect(approveChoice(null)).toBeNull();
  });
});

describe("rowModel buckets and actions", () => {
  const cases: [Partial<SessionInfo>, string, string | null][] = [
    [{ agentState: "waiting_input" }, "needs", "answer"],
    [{ agentState: "waiting_input", delivery: "ci_failed" }, "needs", "answer"],
    [{ delivery: "merge_conflict" }, "needs", "resolve"],
    [{ delivery: "ci_failed" }, "needs", "checks"],
    [{ delivery: "changes_requested" }, "needs", "address"],
    [{ agentState: "idle", delivery: "approved" }, "needs", "merge"],
    [{ agentState: "idle", delivery: "review_pending" }, "review", null],
    [{ agentState: "working" }, "working", null],
    [{ agentState: "dead" }, "done", null],
    [{ delivery: "merged" }, "done", null],
  ];
  it.each(cases)("%j -> %s/%s", (over, bucket, action) => {
    const m = rowModel(s("a", over));
    expect(m.bucket).toBe(bucket);
    expect(m.action).toBe(action);
  });
});

describe("rowModel state word", () => {
  const cases: [Partial<SessionInfo>, string][] = [
    [{ agentState: "waiting_input", inputReason: "permission_prompt" }, "Allow this?"],
    [{ agentState: "waiting_input", inputReason: "question" }, "Question"],
    [{ agentState: "waiting_input", inputReason: "dialog" }, "Needs a choice"],
    [{ agentState: "waiting_input", inputReason: "quota_limited" }, "Needs you"],
    [{ agentState: "waiting_input" }, "Needs you"],
    [{ delivery: "ci_failed" }, "CI failed"],
    [{ delivery: "merge_conflict" }, "Conflict"],
    [{ delivery: "changes_requested" }, "Changes requested"],
    [{ agentState: "idle", delivery: "approved" }, "Approved"],
    [{ agentState: "idle", delivery: "review_pending" }, "In review"],
    [{ agentState: "working" }, "Working"],
    [{ agentState: "working", delivery: "ci_pending" }, "CI running"],
    [{ delivery: "merged" }, "Merged"],
  ];
  it.each(cases)("%j -> %s, never prefixed or uppercase", (over, word) => {
    expect(rowModel(s("a", over)).state).toBe(word);
  });
});

describe("keyed deltas", () => {
  it("computes one model for one changed session and keeps the rest identical", () => {
    const calls = vi.fn(rowModel);
    const model = new InboxModel(calls);
    const list = Array.from({ length: 200 }, (_, i) => s(`s${i}`));
    model.ingest(list);
    expect(calls).toHaveBeenCalledTimes(200);
    const before = new Map(model.models);
    calls.mockClear();

    const next = list.map((x) => (x.id === "s7" ? s("s7", { agentState: "waiting_input" }) : x));
    model.ingest(next);
    expect(calls).toHaveBeenCalledTimes(1);
    for (const [id, m] of model.models) {
      if (id === "s7") expect(m).not.toBe(before.get(id));
      else expect(m).toBe(before.get(id));
    }
    expect(model.buckets.needs.map((m) => m.id)).toEqual(["s7"]);
  });

  it("removes sessions that left the list", () => {
    const model = new InboxModel();
    model.ingest([s("a"), s("b")]);
    model.ingest([s("a")]);
    expect([...model.models.keys()]).toEqual(["a"]);
  });

  it("watches only the first blocked cards", () => {
    const model = new InboxModel();
    model.ingest(Array.from({ length: 8 }, (_, i) => s(`w${i}`, { agentState: "waiting_input" })));
    expect(model.watched.size).toBe(MAX_INBOX_TALK_WATCH);
    expect(MAX_INBOX_TALK_WATCH).toBe(3);
  });
});

describe("inboxItems", () => {
  const mk = (n: number, over: Partial<SessionInfo>): RowModel[] =>
    Array.from({ length: n }, (_, i) => rowModel(s(`x${i}`, over)));
  const buckets = {
    needs: mk(1, { agentState: "waiting_input" }),
    working: mk(1, {}),
    review: [],
    done: mk(DONE_VISIBLE + 5, { agentState: "dead" }),
  };

  it("drops empty sections and cuts Done", () => {
    const items = inboxItems(buckets, "", false);
    expect(items.filter((i) => i.kind === "section").map((i) => (i as { bucket: string }).bucket)).toEqual([
      "needs",
      "working",
      "done",
    ]);
    expect(items.filter((i) => i.kind === "row" && i.model.bucket === "done")).toHaveLength(DONE_VISIBLE);
    expect(items.at(-1)).toMatchObject({ kind: "more", hidden: 5 });
    expect(inboxItems(buckets, "", true).some((i) => i.kind === "more")).toBe(false);
  });

  it("leads with the all-clear block only when nothing needs you and something else is listed", () => {
    expect(inboxItems(buckets, "", false)[0].kind).toBe("section");
    const calm = { ...buckets, needs: [] };
    expect(inboxItems(calm, "", false)[0]).toMatchObject({ kind: "clear" });
    expect(inboxItems(calm, "working", false).some((i) => i.kind === "clear")).toBe(false);
    expect(inboxItems({ needs: [], working: [], review: [], done: [] }, "", false)).toEqual([]);
  });

  it("narrows to one bucket", () => {
    expect(inboxItems(buckets, "working", false).map((i) => i.kind)).toEqual(["row"]);
  });

  it("drops the section header when a filter is on (the chip carries it)", () => {
    expect(inboxItems(buckets, "working", false).some((i) => i.kind === "section")).toBe(false);
  });

  it("hands back the same item object for an unchanged row, section or footer", () => {
    const cache = new ItemCache();
    const list = { ...buckets, working: [rowModel(s("w1", {}))] };
    const a = inboxItems(list, "", false, cache);
    const byKey = new Map(a.map((i) => [i.key, i]));
    expect(byKey.size).toBe(a.length);
    const b = inboxItems({ ...list, working: [rowModel(s("w1", { title: "renamed" }))] }, "", false, cache);
    expect(b.map((i) => i.key)).toEqual(a.map((i) => i.key));
    for (const it of b) {
      if (it.key === "row:w1") expect(it).not.toBe(byKey.get(it.key));
      else expect(it).toBe(byKey.get(it.key));
    }
    // A count that moved is a new section item.
    const c = inboxItems({ ...list, working: [...list.working, rowModel(s("w2", {}))] }, "", false, cache);
    expect(c.find((i) => i.key === "section:working")).not.toBe(byKey.get("section:working"));
  });
});

describe("metaLine", () => {
  it("joins project, ref and age with a spaced middle dot", () => {
    const m = rowModel(s("a", { issue: "NOR-411" }));
    expect(metaLine(m, "Nori App", "22m")).toBe("Nori App \u00B7 NOR-411 \u00B7 22m");
    expect(metaLine(m, "Nori App")).toBe("Nori App \u00B7 NOR-411");
  });

  it("drops empty parts and falls back to the branch", () => {
    expect(metaParts(rowModel(s("a", { issue: "NOR-411" })), "", "22m")).toEqual(["NOR-411", "22m"]);
    expect(metaLine(rowModel(s("a", { issue: "", branch: "feat/x" })), "P")).toBe("P \u00B7 feat/x");
  });
});
