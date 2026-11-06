import { describe, it, expect, afterEach, beforeEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";
import { svc, tool, text, user, human, system, plate } from "./story.testkit";
import { store } from "$lib/store.svelte";
import { nav } from "$lib/nav.svelte";
import { ssh } from "$lib/ssh.svelte";
import { feed } from "$lib/feed.svelte";
import { buildStory, findInStory } from "$lib/turns";
import Timeline from "./Timeline.svelte";
import TimelineSlot from "./TimelineSlot.svelte";

let n = 0;
let SID = "";

function setup(items: unknown[], feedItems: unknown[] = [], session: Record<string, unknown> = {}, conns: unknown[] = []) {
  store.alive = true;
  store.sessions = [{ id: SID, agent: "claude", agentState: "idle", title: "Fix login", ...session }] as never;
  const v = ssh.view(SID);
  v.items = items as never;
  v.connections = conns as never;
  v.loaded = true;
  svc.Feed.mockResolvedValue({ session: SID, epoch: "e1", lastSeq: feedItems.length, health: "ok", items: feedItems });
  return v;
}

/** jsdom lays nothing out, so a test states the geometry the scroller would have. */
function geometry(el: HTMLElement, g: { scrollHeight: number; clientHeight: number; scrollTop: number }) {
  Object.defineProperty(el, "scrollHeight", { configurable: true, get: () => g.scrollHeight });
  Object.defineProperty(el, "clientHeight", { configurable: true, get: () => g.clientHeight });
  el.scrollTop = g.scrollTop;
}

const log = () => screen.getByRole("log") as HTMLElement;
const settle = async () => {
  await tick();
  await new Promise((r) => setTimeout(r, 0));
  await tick();
};

beforeEach(() => {
  SID = `lola-story-${++n}`;
  Element.prototype.scrollIntoView = vi.fn();
  svc.Feed.mockReset();
  svc.FeedOutput.mockReset();
  svc.Output.mockReset().mockResolvedValue({ data: "output text", nextOffset: 11, totalBytes: 11, eof: true });
  svc.Stop.mockReset().mockResolvedValue({});
  svc.CaptureMany.mockReset().mockResolvedValue({});
  nav.focusedTerm = "";
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  localStorage.clear();
});

describe("Timeline voices", () => {
  it("only store items get the you fill", async () => {
    const { container } = (setup([human("h1", "from the store")], [user("u1", "typed in the pane", 2, { origin: "terminal" }), text("x1", "the agent says", 3)]), render(Timeline, { sessionId: SID }));
    await settle();
    const fills = container.querySelectorAll("[data-voice=you]");
    expect(fills).toHaveLength(1);
    expect(fills[0].textContent).toContain("from the store");
    expect(fills[0].className).toContain("bg-sel/50");
    expect(screen.getByText("In terminal")).toBeInTheDocument();
    expect(container.querySelector("[data-voice=terminal]")?.className).not.toContain("bg-sel");
    expect(container.querySelector("[data-voice=agent]")?.className).not.toContain("bg-");
  });

  it("a log row never says exit and never gets a plate", async () => {
    setup([], [tool("t1", "run", "make test", { status: "ok" }, 1), tool("t2", "run", "make vet", { status: "error", preview: "boom" }, 2)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    expect(container.querySelector("[data-plate]")).toBeNull();
    expect(log().textContent).not.toMatch(/exit/);
    const failed = container.querySelector("[data-status=error]");
    expect(failed?.textContent).toContain("failed");
    expect(failed?.querySelector(".text-bad")).not.toBeNull();
    expect(container.querySelector("[data-status=ok] .text-good")).toBeNull();
  });

  it("plate shows exit at fixed right", async () => {
    setup([plate("b1", 1, { why: "see whether it restarted" })]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const p = container.querySelector("[data-plate]") as HTMLElement;
    expect(p.className).toContain("border");
    expect(p.className).toContain("bg-panel");
    const result = p.querySelector("[data-result]") as HTMLElement;
    expect(result.textContent).toMatch(/^exit 0 · /);
    expect(result.querySelector(".text-good")?.textContent).toBe("exit 0");
    // The right column is a fixed track, whatever the command's length.
    expect(p.querySelector("button > span")?.className).toContain("grid-cols-[minmax(0,1fr)_auto_16px]");
    // The why is a claim, so it stays out of the plate.
    expect(p.querySelector("[data-claim=why]")).toBeNull();
    expect(container.querySelector("[data-claim=why]")?.textContent).toContain("see whether it restarted");
  });

  it("a choice row is a moment, not the you fill", async () => {
    setup([human("h1", "You chose: 1 Yes", 1, { origin: "choice" })]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    expect(container.querySelector("[data-voice=you]")).toBeNull();
    expect(container.querySelector("[data-voice=lola]")?.textContent).toContain("You chose: 1 Yes");
  });

  it("a question and a proposal leave a record row, and their controls are the tray's", async () => {
    const q = { id: "e1", kind: "element", order: 1, seq: 1, at: "", element: { id: "e1", kind: "question", title: "Which db?", choices: ["a", "b"], revision: 1 } };
    const prop = { id: "e2", kind: "element", order: 2, seq: 2, at: "", element: { id: "e2", kind: "proposal", title: "Restart it", command: "systemctl restart api", revision: 1 } };
    setup([q, prop]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    expect(screen.getByText("Which db?")).toBeInTheDocument();
    expect(screen.getByText(/systemctl restart api/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "a" })).toBeNull();
    expect(screen.queryByRole("button", { name: /Run on/ })).toBeNull();
    expect(screen.queryByRole("button", { name: "Decline…" })).toBeNull();
    expect(container.querySelector("input")).toBeNull();
  });

  it("role log with aria-live off", async () => {
    setup([human("h1", "hi")]);
    render(Timeline, { sessionId: SID });
    await settle();
    expect(log().getAttribute("aria-live")).toBe("off");
    expect(log().getAttribute("aria-label")).toBe("Conversation");
    expect(screen.getByRole("status")).toBeInTheDocument();
  });

  it("a finding's severity is a word, and never orange", async () => {
    const e = { id: "e1", kind: "element", order: 1, seq: 1, at: "", element: { id: "e1", kind: "finding", severity: "high", title: "OOM", revision: 1 } };
    setup([e]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const word = screen.getByText("High");
    expect(word.className).toContain("text-bad");
    expect(word.className).toContain("font-medium");
    expect(container.innerHTML).not.toContain("text-orange");
    expect(container.querySelector("[data-claim=finding] .rounded-full")).toBeNull();
  });
});

describe("Timeline expansion", () => {
  it("expansion survives refetch", async () => {
    const t = tool("t1", "run", "go test", { hasOutput: true }, 1);
    setup([], [t]);
    svc.FeedOutput.mockResolvedValue({ text: "ok pkg 4.1s" });
    render(Timeline, { sessionId: SID });
    await settle();
    await fireEvent.click(screen.getByRole("button", { expanded: false }));
    await waitFor(() => expect(screen.getByText("ok pkg 4.1s")).toBeInTheDocument());
    // The same id comes back changed, and so does the whole list under a new epoch.
    const f = feed.view(SID);
    svc.Feed.mockResolvedValue({ session: SID, epoch: "e2", lastSeq: 9, health: "ok", items: [{ ...t, seq: 9, tool: { ...t.tool, durationMs: 999 } }] });
    await f.fetch();
    await settle();
    expect(f.items[0].seq).toBe(9);
    expect(screen.getByText("ok pkg 4.1s")).toBeInTheDocument();
    expect(svc.FeedOutput).toHaveBeenCalledTimes(1);
  });

  it("opening a run line lists its calls, a row of their own each", async () => {
    setup([], [tool("t1", "read", "a.go", { hasOutput: false }, 1), tool("t2", "read", "b.go", { hasOutput: false }, 2)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    expect(container.querySelector("[data-run=read]")?.textContent).toContain("a.go, b.go");
    await fireEvent.click(container.querySelector("[data-run=read] button") as HTMLElement);
    expect(container.querySelector('[data-row="t2"]')).not.toBeNull();
  });
});

describe("Timeline rail", () => {
  it("a settled turn's last word is the answer, and earlier beats are narration, at sizes set by their text", async () => {
    const long = "word ".repeat(70);
    setup([human("h1", "ask", 1), system("s1", "checkpoint", "Done", 9, { seq: 1 })], [text("x1", "looking", 2), tool("t1", "read", "a.go", {}, 3), text("x2", long, 4)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const beat = container.querySelector('[data-row="x1"] [data-voice=agent]');
    const answer = container.querySelector('[data-row="x2"] [data-voice=agent]');
    expect(beat?.getAttribute("data-role")).toBe("narration");
    expect(beat?.className).toContain("text-prose");
    expect(beat?.className).toContain("text-ink-2");
    expect(answer?.getAttribute("data-role")).toBe("answer");
    expect(answer?.className).toContain("text-prose");
    expect(container.querySelector("[data-rail]")).not.toBeNull();
    expect(container.querySelector('[data-rail-mark="answer"]')).not.toBeNull();
    expect(container.querySelector('[data-rail-mark="receipt"]')).not.toBeNull();
  });

  it("a word keeps its size when a later one arrives, and only its role changes", async () => {
    setup([human("h1", "ask", 1), system("s1", "checkpoint", "Done", 9, { seq: 1 })], [text("x1", "short beat", 2)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const before = container.querySelector('[data-row="x1"] [data-voice=agent]')?.className ?? "";
    expect(before).toContain("text-prose");
    expect(before).toContain("text-ink ");
    const fv = feed.view(SID);
    fv.items = [...fv.items, tool("t1", "read", "a.go", {}, 3) as never, text("x2", "later", 4) as never];
    await settle();
    const after = container.querySelector('[data-row="x1"] [data-voice=agent]')?.className ?? "";
    expect(after).toContain("text-prose");
    expect(after).toContain("text-ink-2");
  });

  it("the rail is one line for the whole story, even a turn of one row", async () => {
    setup([human("h1", "ask", 1), system("s1", "checkpoint", "Done", 3, { seq: 1 }), human("h2", "again", 5)], [text("x1", "only this", 2)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    expect(container.querySelectorAll("[data-rail]")).toHaveLength(1);
  });

  it("an empty story draws no rail", async () => {
    setup([], []);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    expect(container.querySelector("[data-rail]")).toBeNull();
  });

  it("the person comes first in the DOM, on their own side, and only the agent's rows are in its column", async () => {
    setup([human("h1", "ask", 1), system("s1", "checkpoint", "Done", 9, { seq: 1 })], [text("x1", "the answer", 2)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const turn = container.querySelector("[data-turn] .cv-turn") as HTMLElement;
    const person = turn.querySelector(".cv-person") as HTMLElement;
    const agent = turn.querySelector(".cv-agent") as HTMLElement;
    expect(person.compareDocumentPosition(agent) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(person.querySelector("[data-voice=you]")?.textContent).toContain("ask");
    expect(agent.querySelector("[data-voice=you]")).toBeNull();
    expect(agent.querySelector("[data-voice=agent]")?.textContent).toContain("the answer");
    expect(agent.querySelector("[data-receipt]")).toBeNull();
    expect(turn.querySelector(".cv-turn-end [data-receipt]")).not.toBeNull();
  });

  it("a terminal line sits on the person's side and keeps its label and no fill", async () => {
    setup([], [user("u1", "typed in the pane", 1, { origin: "terminal" }), text("x1", "ok", 2)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const person = container.querySelector(".cv-person") as HTMLElement;
    expect(person.textContent).toContain("In terminal");
    expect(person.querySelector("[data-voice=terminal]")).not.toBeNull();
    expect(person.innerHTML).not.toContain("bg-sel");
  });

  it("an answer's text becomes elements and its literal markers are gone", async () => {
    setup([human("h1", "ask", 1), system("s1", "checkpoint", "Done", 9, { seq: 1 })], [text("x1", "Stays in PR #74.\n\n- **Still open:** one thing\n- **Overlay:** another\n- **`#74`:** conflicting", 2)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    expect(container.querySelector("[data-rich-lead]")?.textContent).toContain("Stays in PR");
    expect(container.querySelector("[data-rich-lead] [data-pr]")?.textContent).toBe("#74");
    const labels = [...container.querySelectorAll("[data-rich-ledger] dt")];
    expect(labels.map((d) => d.textContent)).toEqual(["Still open", "Overlay", "#74"]);
    expect(labels[0].className).toContain("text-warn");
    expect(container.querySelector("[data-rich-ledger]")?.textContent).not.toContain("\`");
    expect(log().innerHTML).not.toContain("text-good");
  });

  it("a person's message is their words, and is never parsed", async () => {
    setup([human("h1", "**not bold** and #12", 1)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const you = container.querySelector("[data-voice=you]") as HTMLElement;
    expect(you.textContent).toContain("**not bold** and #12");
    expect(you.querySelector("strong,[data-pr]")).toBeNull();
  });

  it("the receipt names the files the turn edited, and only the newest turn keeps its actions out", async () => {
    setup(
      [human("h1", "one", 1), system("s1", "checkpoint", "Done", 3, { seq: 1 }), human("h2", "two", 5), system("s2", "checkpoint", "Done", 9, { seq: 2 })],
      [tool("t1", "edit", "src/a.go", { added: 3, removed: 1 }, 2), tool("t2", "edit", "src/b.go", { added: 1, removed: 0 }, 6), text("x1", "done", 7)],
    );
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const lists = container.querySelectorAll("[data-receipt-files]");
    expect(lists).toHaveLength(2);
    expect(lists[0].textContent).toContain("a.go");
    expect(lists[1].textContent).toContain("b.go");
    expect(lists[1].textContent).not.toContain("a.go");
    const actions = container.querySelectorAll("[data-receipt-actions]");
    if (actions.length === 2) {
      expect(actions[0].className).toContain("opacity-0");
      expect(actions[1].className).not.toContain("opacity-0");
    }
  });

  it("a folded turn opened by no person keeps the person's lane empty", async () => {
    const items: unknown[] = [];
    const feedRows: unknown[] = [user("u0", "Background task finished", 1, { origin: "task" })];
    for (let i = 1; i < 14; i++) items.push(system(`s${i}`, "checkpoint", "Done", i * 10 + 3, { seq: i }), human(`h${i}`, `ask ${i}`, i * 10 + 5));
    setup(items, feedRows);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const fold = container.querySelector("[data-turn-fold]") as HTMLElement;
    expect(fold.querySelector("[data-turn-ask]")?.textContent).toBe("");
    expect(fold.textContent).toContain("Background task finished");
  });

  it("a folded turn has both lanes, without literal markers on the agent's side", async () => {
    const items: unknown[] = [];
    const feedRows: unknown[] = [];
    for (let i = 0; i < 14; i++) {
      items.push(human(`h${i}`, `ask ${i}`, i * 10 + 1), system(`s${i}`, "checkpoint", "Done", i * 10 + 3, { seq: i + 1 }));
      feedRows.push(text(`x${i}`, "Pushed `abc" + i + "` on PR", i * 10 + 2));
    }
    setup(items, feedRows);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const fold = container.querySelector("[data-turn-fold]") as HTMLElement;
    expect(fold.querySelector("[data-turn-ask]")?.textContent).toBe("ask 0");
    expect(fold.textContent).toContain("Pushed abc0 on PR");
    expect(fold.textContent).not.toContain("\`");
  });

  it("a resting tool is a dig line: no mono, it names what it touched, and opening brings the mono", async () => {
    setup([], [tool("t1", "edit", "src/a.go", { added: 3, removed: 1 }, 1), tool("t2", "read", "b.go", { hasOutput: true, durationMs: 5 }, 2), tool("t3", "read", "c.go", { hasOutput: false }, 3), text("x1", "done", 4)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const line = container.querySelector("[data-work-fold]") as HTMLElement;
    expect(line.textContent).toContain("Edited");
    expect(line.textContent).toContain("a.go");
    expect(line.textContent).toContain("+3 −1");
    expect(line.innerHTML).not.toContain("font-mono");
    expect(log().querySelector("[data-tool], [data-run]")).toBeNull();
    await fireEvent.click(line);
    expect(container.querySelector("[data-run=read] .font-mono, [data-tool] .font-mono")).not.toBeNull();
  });

  it("a settled failure keeps its word without the colour, and one at the tail takes it with a red tick", async () => {
    setup([], [tool("t1", "run", "make", { status: "error", preview: "boom" }, 1), text("x1", "fixed it", 2), tool("t2", "run", "vet", { status: "error", preview: "bang" }, 3)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const [first, second] = container.querySelectorAll("[data-status=error]");
    expect(first.textContent).toContain("failed");
    expect(first.querySelector(".text-bad")).toBeNull();
    expect(first.querySelector("[data-preview]")).toBeNull();
    expect(second.querySelector(".text-bad")).not.toBeNull();
    expect(second.querySelector("[data-preview]")).not.toBeNull();
    expect(container.querySelectorAll('[data-rail-mark="tick-bad"]')).toHaveLength(1);
  });

  it("a sent image's path reads as a number beside a tile, while the record keeps the path", async () => {
    const path = "/w/.lola/images/a.png";
    const v = setup([human("h1", `look at ${path} please`, 1)]);
    v.sentThumbs.set(path, "data:image/jpeg;base64,AA");
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const you = container.querySelector("[data-voice=you]") as HTMLElement;
    expect(you.textContent).not.toContain(".lola/images");
    expect(you.textContent).toContain("look at (1) please");
    expect(you.querySelector('[data-you-image="1"] img')?.getAttribute("src")).toContain("data:image");
    expect(findInStory(buildStory({ feed: [], items: v.items }), "images/a.png")).toHaveLength(1);
  });

  it("a sent image with no thumbnail draws a glyph and no word", async () => {
    setup([human("h1", "/w/.lola/images/a.png", 1)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const tile = container.querySelector('[data-you-image="1"]') as HTMLElement;
    expect(tile.querySelector("[data-attach-glyph=image]")).not.toBeNull();
    expect(tile.textContent?.trim()).toBe("1");
  });

  it("a run of folded turns sits close, and the first keeps its distance from what is above", async () => {
    const items: unknown[] = [];
    for (let i = 0; i < 14; i++) items.push(human(`h${i}`, `ask ${i}`, i * 10 + 1), system(`s${i}`, "checkpoint", "Done", i * 10 + 3, { seq: i + 1, added: 1, removed: 0, files: 1 }));
    setup(items);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const folds = [...container.querySelectorAll<HTMLElement>("[data-turn]")].filter((t) => t.querySelector("[data-turn-fold]"));
    expect(folds).toHaveLength(4);
    expect(folds[1].style.marginTop).toBe("4px");
    expect(folds[0].textContent).toContain("+1 −0 in 1 file");
    expect(container.querySelector('[data-rail-mark="folded"]')).not.toBeNull();
    expect(container.querySelector("[data-turn-fold] ~ [data-receipt]")).toBeNull();
  });

  it("a reader above the tail stays put when a beat folds away over them", async () => {
    const observed: ResizeObserverCallback[] = [];
    vi.stubGlobal("ResizeObserver", class { constructor(cb: ResizeObserverCallback) { observed.push(cb); } observe() {} disconnect() {} unobserve() {} });
    setup([human("h1", "ask", 1)], [text("x1", "one", 2), text("x2", "two", 3)]);
    render(Timeline, { sessionId: SID });
    await settle();
    const scroller = log();
    geometry(scroller, { scrollHeight: 4000, clientHeight: 450, scrollTop: 1000 });
    const rect = (top: number, bottom: number) => ({ top, bottom, left: 0, right: 0, width: 0, height: bottom - top, x: 0, y: top, toJSON: () => ({}) });
    let shift = 0;
    const spy = vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(function (this: Element) {
      if (this === scroller) return rect(0, 450);
      if (this.hasAttribute("data-turn")) return rect(-500, 900);
      if (this.getAttribute("data-kind") === "you") return rect(-500, -400);
      return rect(20 + shift, 40 + shift);
    });
    await fireEvent.wheel(scroller, { deltaY: -10 });
    await fireEvent.scroll(scroller);
    shift = -60;
    for (const cb of observed) cb([], {} as ResizeObserver);
    expect(scroller.scrollTop).toBe(940);
    spy.mockRestore();
    vi.unstubAllGlobals();
  });

  it("the running head names the ask once it has scrolled off, and goes when it returns", async () => {
    setup([human("h1", "the question", 1)], [text("x1", "a long answer", 2)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const scroller = log();
    const rect = (top: number, bottom: number) => ({ top, bottom, left: 0, right: 0, width: 0, height: bottom - top, x: 0, y: top, toJSON: () => ({}) });
    let youBottom = -10;
    const spy = vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(function (this: Element) {
      if (this === scroller) return rect(0, 450);
      if (this.hasAttribute("data-turn")) return rect(-100, 900);
      if (this.getAttribute("data-kind") === "you") return rect(-100, youBottom);
      return rect(100, 200);
    });
    await fireEvent.scroll(scroller);
    expect(container.querySelector("[data-turn-head]")?.textContent).toContain("the question");
    youBottom = 40;
    await fireEvent.scroll(scroller);
    expect(container.querySelector("[data-turn-head]")).toBeNull();
    spy.mockRestore();
  });
});

describe("Timeline scrolling", () => {
  it("scrolling up stops following", async () => {
    const v = setup([human("h1", "one")]);
    render(Timeline, { sessionId: SID });
    await settle();
    const el = log();
    geometry(el, { scrollHeight: 1000, clientHeight: 200, scrollTop: 800 });
    await fireEvent.scroll(el);
    await fireEvent.wheel(el, { deltaY: -40 });
    el.scrollTop = 300;
    await fireEvent.scroll(el);
    v.items = [...v.items, system("s2", "pr", "PR #2 opened", 2)] as never;
    await settle();
    // It stays where the reader put it, and says what arrived.
    expect(el.scrollTop).toBe(300);
    expect(screen.getByRole("button", { name: /1 new/ })).toBeInTheDocument();
  });

  it("at the tail it follows", async () => {
    const v = setup([human("h1", "one")]);
    render(Timeline, { sessionId: SID });
    await settle();
    const el = log();
    geometry(el, { scrollHeight: 1000, clientHeight: 200, scrollTop: 800 });
    await fireEvent.scroll(el);
    Object.defineProperty(el, "scrollHeight", { configurable: true, get: () => 1400 });
    v.items = [...v.items, text("x", "more", 2)] as never;
    await settle();
    expect(el.scrollTop).toBe(1400);
  });

  it("selection suspends following", async () => {
    const v = setup([human("h1", "one")]);
    render(Timeline, { sessionId: SID });
    await settle();
    const el = log();
    geometry(el, { scrollHeight: 1000, clientHeight: 200, scrollTop: 800 });
    await fireEvent.scroll(el);
    const spy = vi.spyOn(window, "getSelection").mockReturnValue({ isCollapsed: false, anchorNode: el.firstChild, containsNode: () => true } as unknown as Selection);
    document.dispatchEvent(new Event("selectionchange"));
    Object.defineProperty(el, "scrollHeight", { configurable: true, get: () => 1400 });
    v.items = [...v.items, system("s1", "pr", "PR #1 opened", 2)] as never;
    await settle();
    expect(el.scrollTop).toBe(800);
    spy.mockRestore();
  });

  it("sending returns to the bottom", async () => {
    const v = setup([human("h1", "one")]);
    render(Timeline, { sessionId: SID });
    await settle();
    const el = log();
    geometry(el, { scrollHeight: 1000, clientHeight: 200, scrollTop: 300 });
    await fireEvent.wheel(el, { deltaY: -40 });
    await fireEvent.scroll(el);
    v.items = [...v.items, human("h2", "two", 2)] as never;
    await settle();
    expect(el.scrollTop).toBe(1000);
  });

  it("a card waiting while you read further up names itself on the pill", async () => {
    const v = setup([human("h1", "one")]);
    const b = plate("b9", 2, { status: "pending_approval", exitCode: undefined });
    render(Timeline, { sessionId: SID });
    await settle();
    const el = log();
    geometry(el, { scrollHeight: 1000, clientHeight: 200, scrollTop: 300 });
    await fireEvent.wheel(el, { deltaY: -40 });
    await fireEvent.scroll(el);
    v.connections = [{ id: "c1", host: "prod-api", state: "connected", wheel: "agent" }] as never;
    v.items = [...v.items, b] as never;
    await settle();
    expect(screen.getByRole("button", { name: "Waiting for you" })).toBeInTheDocument();
  });
});

describe("Timeline digging", () => {
  it("find reaches a plate", async () => {
    const v = setup([plate("b1", 1, { command: "journalctl -u api" }), human("h1", "unrelated", 2)]);
    render(Timeline, { sessionId: SID });
    await settle();
    v.findFocus++;
    await settle();
    const box = screen.getByRole("textbox", { name: /Find/ });
    await fireEvent.input(box, { target: { value: "journalctl" } });
    await settle();
    expect(screen.getByText("1 of 1")).toBeInTheDocument();
    await fireEvent.keyDown(box, { key: "Enter" });
    await settle();
    expect(v.cursor).toBe("b1");
    expect(Element.prototype.scrollIntoView).toHaveBeenCalled();
  });

  it("an evidence link jumps to its block", async () => {
    const e = { id: "e1", kind: "element", order: 2, seq: 2, at: "", element: { id: "e1", kind: "finding", severity: "high", title: "t", evidence: [{ block: "b1", verified: true, status: "succeeded", exitCode: 0 }], revision: 1 } };
    const v = setup([plate("b1", 1), e]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    await fireEvent.click(container.querySelector("[data-claim=finding] button") as HTMLElement);
    await settle();
    expect(v.cursor).toBe("b1");
    expect(v.expanded.has("b1")).toBe(true);
    expect(v.flashBlock).toBe("b1");
    expect(screen.getByRole("button", { name: /Back to where you were/ })).toBeInTheDocument();
  });

  it("a jump opens the fold holding its target", async () => {
    const mk = (id: string, order: number) => plate(id, order, { command: `cmd-${id}` });
    const v = setup([mk("a", 1), mk("b", 2), mk("c", 3)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    expect(container.querySelector("[data-kind=platefold]")?.textContent).toContain("cmd-a");
    v.jump = { block: "b", n: 1 };
    await settle();
    await settle();
    expect(container.querySelector("[data-kind=platefold]")).toBeNull();
    expect(container.querySelector('[data-row="b"]')).not.toBeNull();
  });

  it("a plate keeps its keys", async () => {
    const v = setup([plate("b1", 1, { status: "running", exitCode: undefined, stderrBytes: 4 })]);
    render(Timeline, { sessionId: SID });
    await settle();
    const el = log();
    el.focus();
    await fireEvent.keyDown(el, { key: "G" });
    expect(v.cursor).toBe("b1");
    await fireEvent.keyDown(el, { key: "Enter" });
    expect(v.expanded.has("b1")).toBe(true);
    await fireEvent.keyDown(el, { key: "s" });
    expect(v.streams.get("b1")).toBe("stderr");
    await fireEvent.keyDown(el, { key: "ArrowLeft" });
    expect(v.expanded.has("b1")).toBe(false);
    await fireEvent.keyDown(el, { key: "a" });
    expect(v.agentDraft).toContain("About b1");
    await fireEvent.keyDown(el, { key: "X" });
    expect(svc.Stop).toHaveBeenCalled();
    await fireEvent.keyDown(el, { key: "ArrowRight" });
    expect(v.expanded.has("b1")).toBe(true);
  });

  it("a key it does not use goes on to the app", async () => {
    setup([plate("b1", 1)]);
    render(Timeline, { sessionId: SID });
    await settle();
    const el = log();
    const seen = vi.fn();
    window.addEventListener("keydown", seen);
    await fireEvent.keyDown(el, { key: "w" });
    window.removeEventListener("keydown", seen);
    expect(seen).toHaveBeenCalled();
  });

  it("[ and ] walk turns, wherever the keyboard is", async () => {
    setup([human("h1", "one", 1), system("s1", "checkpoint", "Done", 2), human("h2", "two", 3)]);
    nav.focusedTerm = SID;
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    expect(container.querySelectorAll("[data-turn]")).toHaveLength(2);
    (Element.prototype.scrollIntoView as ReturnType<typeof vi.fn>).mockClear();
    await fireEvent.keyDown(window, { key: "]" });
    expect(Element.prototype.scrollIntoView).toHaveBeenCalled();
  });

  it("a session that regressed lands on its last moment", async () => {
    const v = setup([system("s1", "ci", "CI failed on #4", 1), human("h1", "later", 2)], [], { delivery: "ci_failed", agentState: "idle" });
    render(Timeline, { sessionId: SID });
    await settle();
    v.streamFocus++;
    await settle();
    expect(v.cursor).toBe("s1");
  });

  it("full output opens and closes", async () => {
    const v = setup([plate("b1", 1)]);
    v.expanded.add("b1");
    const { container } = render(TimelineSlot, { sessionId: SID });
    await settle();
    await waitFor(() => expect(svc.Output).toHaveBeenCalled());
    v.outputMax = "b1";
    await settle();
    expect(container.querySelector(".absolute.inset-0")).not.toBeNull();
    v.outputMax = "";
    await settle();
    expect(container.querySelector(".absolute.inset-0")).toBeNull();
  });
});

describe("Timeline's own states", () => {
  it("a new log says what the agent is doing, and shows the brief it was given", async () => {
    setup([human("h0", "Fix the redirect.", 1, { origin: "initial" })], [], { agentState: "working" });
    svc.Feed.mockResolvedValue({ session: SID, epoch: "e1", lastSeq: 0, health: "empty", items: [] });
    render(Timeline, { sessionId: SID });
    await settle();
    expect(screen.getByRole("heading", { name: "Fix login" })).toBeInTheDocument();
    expect(screen.getByText("Fix the redirect.")).toBeInTheDocument();
    expect(screen.getByText("Claude is reading the task")).toBeInTheDocument();
  });

  it("while the scanner reads, it says so and asks for the tail again", async () => {
    setup([]);
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    svc.Feed.mockResolvedValue({ session: SID, epoch: "e1", lastSeq: 3, scanning: true, health: "ok", items: [text("x1", "from the file's start")] });
    render(Timeline, { sessionId: SID });
    await settle();
    expect(screen.getByText("Reading this conversation")).toBeInTheDocument();
    svc.Feed.mockResolvedValue({ session: SID, epoch: "e1", lastSeq: 900, health: "ok", items: [text("x900", "the real tail", 900)] });
    vi.advanceTimersByTime(1000);
    await settle();
    expect(screen.queryByText("Reading this conversation")).toBeNull();
    expect(screen.getByText("the real tail")).toBeInTheDocument();
    // What the scan answered was not the tail, so it does not stay.
    expect(screen.queryByText("from the file's start")).toBeNull();
  });

  it("older turns fold to their first answer and mount no rows", async () => {
    const items: unknown[] = [];
    const log: unknown[] = [];
    for (let i = 0; i < 12; i++) {
      items.push(human(`h${i}`, `ask ${i}`, i * 10 + 1), system(`s${i}`, "checkpoint", "Done", i * 10 + 3, { seq: i + 1 }));
      log.push(text(`x${i}`, `answer ${i}`, i * 10 + 2));
    }
    setup(items, log);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    expect(container.querySelectorAll("[data-turn-fold]")).toHaveLength(2);
    expect(container.querySelector('[data-row="x0"]')).toBeNull();
    const fold = container.querySelector("[data-turn-fold]");
    expect(fold?.textContent).toContain("ask 0");
    expect(fold?.textContent).toContain("answer 0");
    await fireEvent.click(container.querySelector("[data-turn-fold]") as HTMLElement);
    expect(container.querySelector('[data-row="x0"]')).not.toBeNull();
  });

  it("an earlier life collapses behind a line that says when", async () => {
    const start = system("s9", "session_started", "Claude started", 5);
    setup([human("h1", "old", 1), start, human("h2", "new", 9)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    expect(container.querySelector('[data-row="h1"]')).toBeNull();
    const line = container.querySelector("[data-earlier-life]") as HTMLElement;
    expect(line.textContent).toMatch(/Earlier session, /);
    await fireEvent.click(line);
    expect(container.querySelector('[data-row="h1"]')).not.toBeNull();
  });

  it("since you looked marks the first unseen row, from a timestamp", async () => {
    const stamp = new Date(1_700_000_000_000 + 50 * 1000).getTime();
    localStorage.setItem(`lola.read.${SID}`, String(stamp));
    setup([human("h1", "seen", 10), human("h2", "new", 60)]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    expect(container.querySelector("[data-catch-up]")?.textContent).toContain("Since you looked: 1 turn");
    const line = container.querySelector("[data-catch-up]") as HTMLElement;
    expect(line.compareDocumentPosition(container.querySelector('[data-row="h2"]') as Node)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });

  it("clicking the story without selecting returns the keyboard to the composer", async () => {
    const v = setup([human("h1", "one")]);
    const { container } = render(Timeline, { sessionId: SID });
    await settle();
    const before = v.composerFocus;
    await fireEvent.click(container.querySelector("[data-stream-column]") as HTMLElement);
    expect(v.composerFocus).toBe(before + 1);
  });
});

