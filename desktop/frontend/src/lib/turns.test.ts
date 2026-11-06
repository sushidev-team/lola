import { describe, it, expect } from "vitest";
import { activitySummary, buildStory, catchUp, clampsRow, collapseWork, diffStat, foldLabel, liveVerb, findInStory, fitNames, flatRows, isFolded, isLolaCall, lastLifeIndex, locate, OPEN_TURNS, resolveStatus, shortName, toolStatusWords, type RunRowData, type ToolRowData, turnParts, turnAsk, turnFiles, workSegments, answerRowId, railKind } from "./turns";
import { answerShaped } from "./answerblocks";

const T0 = 1_700_000_000_000;
const iso = (s: number) => new Date(T0 + s * 1000).toISOString();
let n = 0;
const tool = (category: string, summary: string, more: Record<string, unknown> = {}, name = ""): any => {
  n++;
  return { id: `t${n}`, kind: "tool", order: n, seq: n, at: iso(n), tool: { name: name || (category === "read" ? "Read" : category === "edit" ? "Edit" : "Bash"), category, summary, status: "ok", durationMs: 100, ...more } };
};
const text = (t: string, s = ++n): any => ({ id: `x${s}`, kind: "text", order: s, seq: s, at: iso(s), text: t });

describe("grouping tool calls", () => {
  it("groups consecutive reads and names them", () => {
    const rows = collapseWork([tool("read", "internal/auth.go"), tool("read", "internal/middleware.go"), tool("read", "internal/session.go")], true);
    expect(rows).toHaveLength(1);
    const run = rows[0] as RunRowData;
    expect(run.kind).toBe("run");
    expect(run.verb).toBe("Read");
    expect(run.names).toEqual(["auth.go", "middleware.go", "session.go"]);
  });

  it("never shows a bare count", () => {
    // However narrow the row, a name shows, and what did not fit is +N beside it.
    const names = ["a-very-long-file-name-that-will-not-fit-anywhere-at-all.go", "b.go", "c.go"];
    const narrow = fitNames(names, 12);
    expect(narrow.shown).toHaveLength(1);
    expect(narrow.shown[0].length).toBeGreaterThan(0);
    expect(narrow.rest).toBe(2);
    const roomy = fitNames(["a.go", "b.go", "c.go"], 200);
    expect(roomy).toEqual({ shown: ["a.go", "b.go", "c.go"], rest: 0 });
    // A call with no summary at all still has a name to show.
    expect(shortName({ name: "mcp__linear__get_issue", category: "other", summary: "" })).toBe("get_issue");
    const rows = collapseWork([tool("run", ""), tool("run", ""), tool("run", "")], true) as RunRowData[];
    expect(rows[0].names.length).toBeGreaterThan(0);
  });

  it("a failure splits a run and stays visible", () => {
    const rows = collapseWork([tool("read", "a.go"), tool("read", "b.go"), tool("run", "make test", { status: "error", preview: "FAIL" }), tool("read", "c.go"), tool("read", "d.go")], true);
    expect(rows.map((r) => r.kind)).toEqual(["run", "tool", "run"]);
    const failed = rows[1] as ToolRowData;
    expect(failed.status).toBe("error");
    expect(failed.previewOpen).toBe(true);
  });

  it("running never groups", () => {
    const rows = collapseWork([tool("run", "a"), tool("run", "b"), tool("run", "c", { status: "running" }), tool("run", "d"), tool("run", "e")], true);
    expect(rows.map((r) => r.kind)).toEqual(["tool", "tool", "tool", "tool", "tool"]);
    expect((rows[2] as ToolRowData).status).toBe("running");
  });

  it("consecutive edits share one stable file explorer", () => {
    const three = collapseWork([tool("edit", "a.go"), tool("edit", "b.go"), tool("edit", "a.go"), tool("edit", "c.go")], true);
    expect(three).toHaveLength(1);
    const four = collapseWork([tool("edit", "a.go", { added: 2, removed: 1 }), tool("edit", "b.go", { added: 3 }), tool("edit", "c.go"), tool("edit", "d.go")], true);
    expect(four).toHaveLength(1);
    expect((four[0] as RunRowData).added).toBe(5);
    expect((four[0] as RunRowData).removed).toBe(1);
  });

  it("three clean commands in a row group, and two do not", () => {
    expect(collapseWork([tool("run", "a"), tool("run", "b")], true)).toHaveLength(2);
    expect(collapseWork([tool("run", "a"), tool("run", "b"), tool("run", "c")], true)).toHaveLength(1);
  });

  it("a subagent is always its own row", () => {
    const rows = collapseWork([tool("agent", "Find the bug", { subagent: "x" }, "Task"), tool("read", "a.go"), tool("read", "b.go")], true);
    expect(rows.map((r) => r.kind)).toEqual(["tool", "run"]);
  });

  it("keeps separated edit bursts in chronological order even in long stretches", () => {
    const mixed = [];
    for (let i = 0; i < 5; i++) mixed.push(tool("read", `r${i}.go`), tool("edit", `e${i}.go`), tool("run", `cmd${i}`));
    const rows = collapseWork(mixed, true);
    expect(rows).toHaveLength(15);
    expect(rows.flatMap(row => row.ids)).toEqual(mixed.map(item => item.id));
    expect(rows.filter(row => row.kind === 'run' && row.category === 'edit')).toHaveLength(5);
  });

  it("keeps an edit group's identity as files arrive and splits after a pause", () => {
    const first = tool('edit', 'a.ts');
    const second = { ...tool('edit', 'b.ts', { status: 'running' }), at: new Date(Date.parse(first.at) + 2000).toISOString() };
    const late = { ...tool('edit', 'c.ts'), at: new Date(Date.parse(second.at) + 31000).toISOString() };
    const one = collapseWork([first], true, true, first.id);
    const two = collapseWork([first, second], true, true, second.id);
    expect(two).toHaveLength(1);
    expect(two[0].id).toBe(one[0].id);
    expect(collapseWork([first, second, late], true)).toHaveLength(2);
  });

  it("a long stretch names its failures first and counts them, and an unresolved call keeps its row", () => {
    const mixed = [];
    for (let i = 0; i < 6; i++) mixed.push(tool("run", `cmd${i}`));
    mixed.push(tool("run", "broken", { status: "error" }));
    for (let i = 0; i < 6; i++) mixed.push(tool("run", `later${i}`));
    mixed.push(tool("run", "make test", { status: "running" }));
    const rows = collapseWork(mixed, true);
    const line = rows[0] as RunRowData;
    expect(line.names[0]).toBe("broken");
    expect(line.failed).toBe(1);
    expect(rows.at(-1)?.kind).toBe("tool");
    expect((rows.at(-1) as ToolRowData).status).toBe("running");
  });

  it("the stretch still in progress keeps its last failure's row and preview", () => {
    const mixed = [];
    for (let i = 0; i < 10; i++) mixed.push(tool("read", `r${i}.go`));
    mixed.push(tool("run", "clean"));
    mixed.push(tool("run", "first", { status: "error", preview: "one" }));
    mixed.push(tool("run", "second", { status: "error", preview: "two" }));
    const rows = collapseWork(mixed, true, true);
    const own = rows.filter((r) => r.kind === "tool") as ToolRowData[];
    expect(own.map((r) => r.target)).toEqual(["second"]);
    expect(own[0].previewOpen).toBe(true);
    const ran = rows.find((r) => r.kind === "run" && r.category === "run") as RunRowData;
    expect(ran.names[0]).toBe("first");
    expect(ran.failed).toBe(1);
  });

  it("drops lola ssh calls", () => {
    expect(isLolaCall({ category: "run", summary: "lola ssh run prod-api -- uptime" })).toBe(true);
    expect(isLolaCall({ category: "run", summary: "lola report phase testing" })).toBe(true);
    expect(isLolaCall({ category: "run", summary: "lola post narration hi" })).toBe(true);
    // A chain did real work, so it stays.
    expect(isLolaCall({ category: "run", summary: "lola report x && go test ./..." })).toBe(false);
    expect(isLolaCall({ category: "read", summary: "lola ssh" })).toBe(false);
    expect(collapseWork([tool("run", "lola ssh poll"), tool("run", "lola report note hi")], true)).toEqual([]);
  });

  it("unresolved call on a dead agent reads no result", () => {
    expect(resolveStatus("running", false)).toBe("no_result");
    expect(resolveStatus("running", true)).toBe("running");
    expect(resolveStatus("ok", false)).toBe("ok");
    const [row] = collapseWork([tool("run", "make test", { status: "running" })], false) as ToolRowData[];
    expect(row.status).toBe("no_result");
    expect(toolStatusWords(row.tool, row.status, "3:00:00", "").word).toBe("no result");
  });

  it("a tool row's status never says exit", () => {
    for (const status of ["ok", "error", "running", "interrupted", "no_result"]) {
      const w = toolStatusWords({ category: "run", durationMs: 100 }, status, "0:41", "0.1s");
      expect(`${w.word} ${w.tail}`).not.toMatch(/exit/);
    }
    expect(toolStatusWords({ category: "run" }, "error", "", "1s").bad).toBe(true);
    expect(toolStatusWords({ category: "run" }, "ok", "", "1s").bad).toBe(false);
  });
});

describe("the merge", () => {
  it("sorts a delivered message by deliveredAt", () => {
    const you: any = { id: "h1", kind: "human", order: 1, seq: 1, at: iso(0), human: { text: "first typed", delivered: true, deliveredAt: iso(50) } };
    const before = text("before", 10);
    const after = text("after", 60);
    const rows = flatRows(buildStory({ feed: [before, after], items: [you] }));
    expect(rows.map((r) => r.id)).toEqual(["x10", "h1", "x60"]);
  });

  it("hides an echo", () => {
    const echo: any = { id: "u1", kind: "user", order: 1, seq: 1, at: iso(1), text: "hello", echo: true };
    const you: any = { id: "h1", kind: "human", order: 1, seq: 1, at: iso(0), human: { text: "hello", delivered: true, deliveredAt: iso(1) } };
    const rows = flatRows(buildStory({ feed: [echo], items: [you] }));
    expect(rows.map((r) => r.kind)).toEqual(["you"]);
  });

  it("unmatched user record reads In terminal", () => {
    const typed: any = { id: "u1", kind: "user", order: 1, seq: 1, at: iso(1), text: "typed by hand", origin: "terminal" };
    const rows = flatRows(buildStory({ feed: [typed], items: [] }));
    expect(rows[0].kind).toBe("terminal");
  });

  it("a background task and a system turn start read as origin lines", () => {
    const task: any = { id: "u2", kind: "user", order: 2, seq: 2, at: iso(2), text: "", origin: "task" };
    const rows = flatRows(buildStory({ feed: [task], items: [] }));
    expect(rows[0].kind).toBe("origin");
    expect((rows[0] as { text: string }).text).toBe("Background task finished");
  });

  it("an undelivered message stays out of the story, as a queue row's business", () => {
    const queued: any = { id: "h1", kind: "human", order: 1, seq: 1, at: iso(0), human: { text: "later", delivered: false } };
    expect(flatRows(buildStory({ feed: [], items: [queued] }))).toEqual([]);
  });

  it("a block waiting on a person is a card, and joins the story once decided", () => {
    const waiting: any = { id: "b1", kind: "block", order: 1, seq: 1, at: iso(0), block: { id: "b1", status: "pending_approval", actor: "agent", command: "x" } };
    expect(flatRows(buildStory({ feed: [], items: [waiting] }))).toEqual([]);
    const ran = { ...waiting, block: { ...waiting.block, status: "failed", exitCode: 1 } };
    expect(flatRows(buildStory({ feed: [], items: [ran] }))[0].kind).toBe("plate");
  });

  it("a host in scope filters lola's record, and never the log", () => {
    const here: any = { id: "e1", kind: "element", order: 1, seq: 1, at: iso(1), element: { id: "e1", kind: "narration", conn: "c1", title: "here" } };
    const there: any = { id: "e2", kind: "element", order: 2, seq: 2, at: iso(2), element: { id: "e2", kind: "narration", conn: "c2", title: "there" } };
    const said = text("log", 3);
    const ids = flatRows(buildStory({ feed: [said], items: [here, there], scope: "c1" })).map((r) => r.id);
    expect(ids).toEqual(["e1", "x3"]);
  });

  it("Facts is the audit: lola's record only", () => {
    const b: any = { id: "b1", kind: "block", order: 1, seq: 1, at: iso(1), block: { id: "b1", status: "succeeded", actor: "agent", command: "x" } };
    const e: any = { id: "e1", kind: "element", order: 2, seq: 2, at: iso(2), element: { id: "e1", kind: "finding", title: "t" } };
    const rows = flatRows(buildStory({ feed: [text("log", 3)], items: [b, e], filter: "facts" }));
    expect(rows.map((r) => r.id)).toEqual(["b1"]);
  });

  it("three clean commands fold into a plate that names them, unless evidence points at one", () => {
    const mk = (id: string, order: number): any => ({ id, kind: "block", order, seq: order, at: iso(order), block: { id, status: "succeeded", actor: "agent", command: `cmd-${id}`, durationMs: 10 } });
    const rows = flatRows(buildStory({ feed: [], items: [mk("a", 1), mk("b", 2), mk("c", 3)] }));
    expect(rows[0].kind).toBe("platefold");
    expect((rows[0] as { names: string[] }).names).toEqual(["cmd-a", "cmd-b", "cmd-c"]);
    const cited: any = { id: "e", kind: "element", order: 4, seq: 4, at: iso(4), element: { id: "e", kind: "finding", title: "t", evidence: [{ block: "b", verified: true }] } };
    const kinds = flatRows(buildStory({ feed: [], items: [mk("a", 1), mk("b", 2), mk("c", 3), cited] })).map((r) => r.kind);
    expect(kinds).toEqual(["plate", "plate", "plate", "element"]);
  });
});

describe("what a row says about itself", () => {
  it("a diff drops the sides that did not move", () => {
    expect(diffStat(18, 0)).toBe("+18");
    expect(diffStat(0, 23)).toBe("−23");
    expect(diffStat(12, 3)).toBe("+12 −3");
    expect(diffStat(0, 0)).toBe("");
  });
  it("a live verb says so, and the word goes when it does", () => {
    expect(["Ran", "Read", "Edit", "Write", "Search"].map(liveVerb)).toEqual(["Running", "Reading", "Editing", "Writing", "Search"]);
    expect(toolStatusWords({ category: "run" }, "running", "0:41", "", true)).toMatchObject({ word: "", tail: "0:41" });
    expect(toolStatusWords({ category: "other" }, "running", "0:41", "", false).word).toBe("running");
  });
  it("a failure the agent spoke after is settled, and one at the tail is not", () => {
    const failed = tool("run", "make test", { status: "error" });
    const story = buildStory({ feed: [failed, text("that broke, looking"), tool("run", "make vet", { status: "error" })], items: [] });
    const tools = story.turns[0].rows.filter((r) => r.kind === "tool") as ToolRowData[];
    expect(tools.map((t) => !!t.settled)).toEqual([true, false]);
  });
  it("a folded turn with no prose names what it did, never a row's kind", () => {
    const story = buildStory({ feed: [tool("run", "go test ./...")], items: [] });
    expect(foldLabel(story.turns[0])).toBe("Ran go test ./...");
    expect(foldLabel({ firstAnswer: "", rows: [{ kind: "divider", label: "Compacted", life: false } as never] })).toBe("Compacted");
  });
  it("only a long row folds", () => {
    expect(clampsRow("one line")).toBe(false);
    expect(clampsRow(Array(13).fill("x").join("\n"))).toBe(true);
    expect(clampsRow("x".repeat(2000))).toBe(true);
  });
});

describe("turns", () => {
  const you = (id: string, s: number): any => ({ id, kind: "human", order: s, seq: s, at: iso(s), human: { text: id, delivered: true } });
  const done = (id: string, s: number, more = {}): any => ({ id, kind: "system", order: s, seq: s, at: iso(s), system: { kind: "checkpoint", text: "Done", durationMs: 151000, ...more } });

  it("a human message starts a turn and a stop ends it", () => {
    const story = buildStory({ feed: [text("a", 2), text("b", 12)], items: [you("h1", 1), done("s1", 3), you("h2", 11)] });
    expect(story.turns.map((t) => t.rows.map((r) => r.id))).toEqual([["h1", "x2", "s1"], ["h2", "x12"]]);
    expect(story.lastYou).toBe("h2");
  });

  it("the gaps follow the rail's table: digs touch, prose by its size, and a receipt sits 10 under", () => {
    const a = tool("read", "a.go");
    const b = tool("edit", "b.go");
    const say = text("answer");
    const story = buildStory({ feed: [a, b, say, text("more ".repeat(70))], items: [you("h1", 0), done("s1", 1e6), you("h2", 2e6)] });
    const rows = story.turns[0].rows;
    const gap = (kind: string, nth = 0) => rows.filter((r) => r.kind === kind)[nth].gap;
    expect(rows[0].kind).toBe("you");
    expect(gap("tool", 0)).toBe(12);
    expect(gap("run", 0)).toBe(0);
    expect(gap("prose", 0)).toBe(6);
    expect(gap("prose", 1)).toBe(28);
    expect(gap("receipt")).toBe(10);
    expect(story.turns[1].gap).toBe(32);
    expect(story.turns[0].gap).toBe(0);
  });

  it("a running call stands off the line above it, and a dig after an answer gets 12", () => {
    const rows = buildStory({ feed: [text("go", 1), tool("read", "a.go"), tool("run", "make", { status: "running" })], items: [], alive: true }).turns[0].rows;
    expect(rows.map((r) => r.gap)).toEqual([0, 12, 10]);
  });

  it("a folded turn's meta says what changed and when, and never a bare number", () => {
    const story = buildStory({ feed: [], items: [done("s1", 1, { added: 84, removed: 12, files: 6, seq: 14 })] });
    expect(story.turns[0].meta).toMatch(/^\+84 −12 in 6 files · \S+/);
    expect(buildStory({ feed: [], items: [done("s2", 1, { seq: 14 })] }).turns[0].meta).not.toMatch(/14/);
  });

  it("a folded turn says the answer, not the narration before it", () => {
    const story = buildStory({ feed: [text("looking", 2), text("## The answer\nmore", 3)], items: [you("h1", 1)] });
    expect(story.turns[0].firstAnswer).toBe("The answer");
  });

  it("the ask of a turn is its first line, with an image's path read back as a number", () => {
    const story = buildStory({ feed: [], items: [{ ...you("h1", 1), human: { text: "see /w/.lola/images/a.png\nand more", delivered: true } }] });
    expect(turnAsk(story.turns[0])).toBe("see (1)");
    expect(turnAsk({ rows: [] })).toBe("");
    expect(turnAsk({ rows: [{ kind: "origin", text: "Background task finished\nmore" } as never] })).toBe("Background task finished");
  });

  it("a receipt counts what the checkpoint says, and has no number without one", () => {
    const rows = flatRows(buildStory({ feed: [], items: [done("s1", 1, { added: 84, removed: 12, files: 6, seq: 14 })] }));
    expect((rows[0] as { words: string }).words).toBe("Done · 2m 31s · +84 −12 in 6 files · #14");
    const bare = flatRows(buildStory({ feed: [], items: [done("s2", 2)] }));
    expect((bare[0] as { words: string }).words).toBe("Done · 2m 31s");
    const stop: any = { id: "s3", kind: "system", order: 3, seq: 3, at: iso(3), system: { kind: "stopped", text: "You stopped this turn.", durationMs: 41000 } };
    const stopped = flatRows(buildStory({ feed: [], items: [stop] }));
    expect((stopped.find((r) => r.kind === "receipt") as { words: string }).words).toBe("Stopped · 41s");
  });

  it("a stop says what it did not end, then gives its receipt", () => {
    const stop: any = { id: "s1", kind: "system", order: 1, seq: 1, at: iso(1), system: { kind: "stopped", text: "You stopped this turn. Background shells keep running.", durationMs: 41000 } };
    const rows = flatRows(buildStory({ feed: [], items: [stop] }));
    expect(rows.map((r) => r.kind)).toEqual(["moment", "receipt"]);
    expect((rows[0] as { text: string }).text).toBe("You stopped this turn. Background shells keep running.");
    expect((rows[1] as { words: string }).words).toBe("Stopped · 41s");
    // Both rows are keyed in one each block, so they must not share an id.
    expect(rows[0].id).not.toBe(rows[1].id);
    const bare: any = { ...stop, system: { kind: "stopped", text: "", durationMs: 41000 } };
    expect(flatRows(buildStory({ feed: [], items: [bare] })).map((r) => r.kind)).toEqual(["receipt"]);
  });

  it("older turns fold, and a person can open one", () => {
    expect(isFolded(0, OPEN_TURNS + 1, new Set(), "a")).toBe(true);
    expect(isFolded(1, OPEN_TURNS + 1, new Set(), "a")).toBe(false);
    expect(isFolded(0, OPEN_TURNS + 1, new Set(["turn-a"]), "a")).toBe(false);
  });

  it("a folded turn keeps its first answer's first line", () => {
    const story = buildStory({ feed: [text("line one\nline two", 2)], items: [you("h1", 1)] });
    expect(story.turns[0].firstAnswer).toBe("line one");
  });

  it("a new session divides a previous life, and the story's own first start is not a row", () => {
    const start = (id: string, s: number): any => ({ id, kind: "system", order: s, seq: s, at: iso(s), system: { kind: "session_started", text: "Claude started" } });
    expect(flatRows(buildStory({ feed: [], items: [start("s0", 1)] }))).toEqual([]);
    const story = buildStory({ feed: [], items: [start("s0", 1), you("h1", 2), start("s1", 3), you("h2", 4)] });
    expect(story.turns.map((t) => t.rows[0].kind)).toEqual(["you", "divider", "you"]);
    expect(lastLifeIndex(story.turns)).toBe(1);
  });
});

describe("finding", () => {
  it("reaches a row, and a call folded inside a run line", () => {
    const story = buildStory({ feed: [tool("read", "internal/needle.go"), tool("read", "b.go"), text("no match here")], items: [] });
    const found = findInStory(story, "needle");
    expect(found).toHaveLength(1);
    expect(found[0].kind).toBe("run");
    expect(locate(story, found[0].ids[0])?.row.kind).toBe("run");
    expect(findInStory(story, "   ")).toEqual([]);
  });
});

describe("since you looked", () => {
  const you = (id: string, s: number): any => ({ id, kind: "human", order: s, seq: s, at: iso(s), human: { text: id, delivered: true } });

  it("counts turns, files and CI from what came after the stamp", () => {
    const edit = (s: number, f: string) => ({ ...tool("edit", f), at: iso(s) });
    const ci: any = { id: "c1", kind: "system", order: 30, seq: 30, at: iso(30), system: { kind: "ci", text: "CI passed on #412" } };
    const story = buildStory({ feed: [edit(21, "a.go"), edit(22, "b.go")], items: [you("h1", 5), you("h2", 20), ci] });
    const got = catchUp(story, T0 + 10_000);
    expect(got?.rowId).toBe("h2");
    expect(got?.words).toBe("Since you looked: 1 turn · 2 files · CI passed");
  });

  it("says nothing on a first open, or when nothing is new", () => {
    const story = buildStory({ feed: [], items: [you("h1", 5)] });
    expect(catchUp(story, 0)).toBeNull();
    expect(catchUp(story, T0 + 100_000)).toBeNull();
  });
});

describe("turnParts", () => {
  const prose = (id: string) => ({ id, gap: 0, kind: "prose", text: id, truncated: false }) as never;
  const tool = (id: string, verb = "Ran", category = "run", status = "ok") => ({ id, gap: 0, kind: "tool", verb, target: `/a/${id}`, status, tool: { category }, item: {}, previewOpen: false }) as never;
  const you = (id: string) => ({ id, gap: 0, kind: "you", item: {} }) as never;

  it("folds work before the last word, leaves narration standing between the lines, and what follows open", () => {
    const parts = turnParts([you("y"), prose("p1"), tool("t1"), tool("t2"), prose("p2"), tool("t3")]);
    expect(parts.map((p) => p.kind)).toEqual(["row", "row", "work", "row", "row"]);
    const work = parts[2] as Extract<ReturnType<typeof turnParts>[number], { kind: "work" }>;
    expect(work.rows.map((r) => r.id)).toEqual(["t1", "t2"]);
  });
  it("keeps the last beats before the answer, and older ones fold with their work, still naming it", () => {
    const rows = [you("y")];
    for (let i = 1; i <= 8; i++) rows.push(prose(`p${i}`), tool(`t${i}`));
    rows.push(prose("answer"));
    const parts = turnParts(rows, 6);
    const fold = parts[1] as Extract<ReturnType<typeof turnParts>[number], { kind: "work" }>;
    expect(fold.kind).toBe("work");
    expect(fold.rows.map((r) => r.id)).toEqual(["p1", "t1", "p2", "t2"]);
    expect(fold.label).toContain("/a/t1");
    const standing = parts.flatMap((p) => (p.kind === "row" && p.row.kind === "prose" ? [p.row.id] : []));
    expect(standing).toEqual(["p3", "p4", "p5", "p6", "p7", "p8", "answer"]);
  });
  it("can fold all preceding narration while leaving the answer and trailing failure visible", () => {
    const parts = turnParts([you("y"), prose("p1"), tool("t1"), prose("p2"), prose("answer"), tool("failed", "Ran", "run", "error")], 0);
    expect(parts.map((p) => p.kind === "row" ? p.row.id : p.rows.map((r) => r.id))).toEqual(["y", ["p1", "t1", "p2"], "answer", "failed"]);
  });

  it("never hides a running call or an unanswered failure when folding a settled turn", () => {
    const parts = turnParts([prose("before"), tool("running", "Ran", "run", "running"), prose("answer"), tool("t1"), tool("failed", "Ran", "run", "error"), tool("missing", "Ran", "run", "no_result")], 0, true);
    const visible = parts.flatMap(p => p.kind === "row" ? [p.row.id] : []);
    expect(visible).toEqual(expect.arrayContaining(["running", "answer", "failed", "missing"]));
  });

  it("a plate breaks the cap's fold too, and prose with nothing to name stays up", () => {
    const plate = { id: "b", gap: 0, kind: "plate", item: {} } as never;
    const rows = [prose("p1"), plate, prose("p2"), prose("p3"), tool("t3"), prose("last")];
    const parts = turnParts(rows, 1);
    expect(parts.map((p) => (p.kind === "row" ? p.row.id : "work"))).toEqual(["p1", "b", "p2", "p3", "t3", "last"]);
  });
  it("does not fold a lone tool or prose with no work", () => {
    expect(turnParts([tool("t1"), prose("p")]).map((p) => p.kind)).toEqual(["row", "row"]);
    expect(turnParts([prose("a"), prose("b"), prose("c")]).map((p) => p.kind)).toEqual(["row", "row", "row"]);
  });
  it("a card or a message breaks a stretch and is never folded", () => {
    const parts = turnParts([tool("t1"), tool("t2"), you("y"), tool("t3"), tool("t4"), prose("p")]);
    expect(parts.map((p) => p.kind)).toEqual(["work", "row", "work", "row"]);
  });
  it("names what the work touched, what changed first, and counts failures", () => {
    const parts = turnParts([tool("r1", "Read", "read"), tool("c1", "Ran", "run", "error"), tool("e1", "Edit", "edit"), prose("p")]);
    const work = parts[0] as Extract<ReturnType<typeof turnParts>[number], { kind: "work" }>;
    expect(work.label).toBe("Edit e1 · Ran /a/c1 · Read r1");
    expect(work.failed).toBe(1);
  });

  it("segments name what was touched in the past tense, in the label's order, three names a verb", () => {
    const edit = (id: string, verb = "Edit") => ({ id, gap: 0, kind: "tool", verb, target: `/a/${id}`, status: "ok", tool: { category: "edit", added: 3, removed: 1 }, item: {}, previewOpen: false }) as never;
    const reads = ["a", "b", "c", "d", "e"].map((n) => tool(n, "Read", "read"));
    const segs = workSegments([...reads, tool("make", "Ran", "run"), edit("x"), edit("y"), edit("z", "Write"), tool("q", "Search", "read")]);
    expect(segs.map((s) => s.verb)).toEqual(["Edited", "Wrote", "Ran", "Read", "Searched"]);
    expect(segs[0]).toEqual({ verb: "Edited", names: ["x", "y"], rest: 0, stat: "+6 −2" });
    expect(segs[3]).toEqual({ verb: "Read", names: ["a", "b", "c"], rest: 2, stat: "" });
  });
});

describe("the rail", () => {
  const row = (r: Record<string, unknown>) => ({ id: "r", ids: [], gap: 0, at: 0, ...r }) as never;
  it("a beat's size comes from its text, so the last word can change without a reflow", () => {
    expect(answerShaped("I'll read the config first.")).toBe(false);
    expect(answerShaped("x".repeat(281))).toBe(true);
    expect(answerShaped("Short.\n\n## Heading")).toBe(true);
    expect(answerShaped("- one\n- two")).toBe(true);
    expect(answerShaped("| a | b |")).toBe(true);
    expect(answerShaped("Item 2. Is fine and # is not a heading")).toBe(false);
  });
  it("a turn has an answer once it ended, and while the agent can still speak it has none", () => {
    const rows = [row({ kind: "prose", id: "p1" }), row({ kind: "prose", id: "p2" })];
    expect(answerRowId({ rows, receipt: undefined }, true)).toBe("");
    expect(answerRowId({ rows, receipt: undefined }, false)).toBe("p2");
    expect(answerRowId({ rows, receipt: rows[0] }, true)).toBe("p2");
  });
  it("a mark says what a row is, and only an unsettled failure takes the bad tick", () => {
    expect(railKind(row({ kind: "prose", id: "p" }), "p")).toBe("answer");
    expect(railKind(row({ kind: "prose", id: "p" }), "")).toBe("narration");
    expect(railKind(row({ kind: "tool", status: "running" }), "")).toBe("running");
    expect(railKind(row({ kind: "tool", status: "error" }), "")).toBe("tick-bad");
    expect(railKind(row({ kind: "tool", status: "error", settled: true }), "")).toBe("tick");
    expect(railKind(row({ kind: "run", failed: 1 }), "")).toBe("tick-bad");
    expect(railKind(row({ kind: "run", failed: 1, settled: true }), "")).toBe("tick");
    expect(railKind(row({ kind: "plate", item: { block: { why: "w" } } }), "")).toBe("plate-why");
    expect(railKind(row({ kind: "plate", item: { block: { why: "w" } } }), "", "facts")).toBe("plate");
    expect(railKind(row({ kind: "element", item: { element: { kind: "finding" } } }), "")).toBe("finding");
    expect(railKind(row({ kind: "moment" }), "")).toBe("none");
  });
});

describe("turnFiles", () => {
  const edit = (target: string, added = 1, removed = 0, category = "edit") => ({ id: target, gap: 0, kind: "tool", verb: "Edit", target, status: "ok", tool: { category, added, removed }, item: {}, previewOpen: false }) as never;

  it("names each edited file once, with its last segment, and sums its stat", () => {
    expect(turnFiles([edit("src/a.go", 3, 1), edit("src/a.go", 2, 2), edit("lib/b.ts")])).toEqual({
      files: [
        { name: "a.go", path: "src/a.go", added: 5, removed: 3 },
        { name: "b.ts", path: "lib/b.ts", added: 1, removed: 0 },
      ],
      more: 0,
    });
  });

  it("stops at five and says how many more", () => {
    const r = turnFiles(["a", "b", "c", "d", "e", "f", "g"].map((n) => edit(n)));
    expect(r.files).toHaveLength(5);
    expect(r.more).toBe(2);
  });

  it("reads inside a run, and ignores what was not an edit", () => {
    const run = { id: "r", gap: 0, kind: "run", verb: "Edit", category: "edit", names: ["x"], items: [edit("x.go")], added: 1, removed: 0, durationMs: 0, failed: 0 } as never;
    expect(turnFiles([run, edit("read.go", 0, 0, "read")]).files.map((f) => f.name)).toEqual(["x.go"]);
    expect(turnFiles([edit("read.go", 0, 0, "read")])).toEqual({ files: [], more: 0 });
  });
});

describe("activity summaries", () => {
  it("counts file identities and commands without inventing intent", () => {
    const rows = collapseWork([tool("read", "a.go"), tool("read", "a.go"), tool("read", "b.go"), tool("run", "git status"), tool("run", "npm test")], true);
    expect(activitySummary(rows).label).toBe("Ran 2 commands · Read 2 files");
  });
  it("does not wrap a single historical call in another disclosure", () => {
    const rows = collapseWork([tool("run", "gh api graphql")], false);
    const parts = turnParts(rows, 0, true);
    expect(parts).toHaveLength(1);
    expect(parts[0].kind).toBe("row");
  });
  it("does not wrap an existing call group in another disclosure", () => {
    const rows = collapseWork([tool("read", "a.go"), tool("read", "b.go")], false);
    expect(turnParts(rows, 0, true)[0].kind).toBe("row");
  });
});
