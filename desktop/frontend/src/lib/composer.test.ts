import { describe, expect, it } from "vitest";
import { composerState, DAEMON_DOWN_LINE, footerFor, isFrameLine, isPlainChoice, isPrimaryChoice, queuedItems, runTarget, trayAnnouncement, trayCards, type FooterInput } from "./composer";

const base: FooterInput = { state: "resting", agent: "Claude", hasText: false, canStop: true, running: false };
const f = (o: Partial<FooterInput>) => footerFor({ ...base, ...o });
const host = { name: "prod-api", prompt: "deploy@prod-api:/srv" };
const words = (b: { label: string; keys?: string }) => [b.label, b.keys].filter(Boolean).join(" ");

describe("the footer table, one test per row", () => {
  it("resting, proof holds", () => {
    const r = f({ state: "resting", hasText: true });
    expect([r.glyph, r.text]).toEqual(["○", "Resting"]);
    expect(r.secondary).toEqual([]);
    expect(words(r.primary)).toBe("Send ↩");
    expect(r.primary).toMatchObject({ act: "send", variant: "primary", disabled: false });
  });

  it("working, empty field", () => {
    const r = f({ state: "working" });
    expect([r.glyph, r.text, r.clock]).toEqual(["◐", "Working", true]);
    expect(r.left).toEqual(["stop"]);
    expect(r.secondary).toEqual([]);
    expect(words(r.primary)).toBe("Send ↩");
    expect(r.primary.disabled).toBe(true);
  });

  it("working, text", () => {
    const r = f({ state: "working", hasText: true });
    expect(r.left).toEqual(["stop"]);
    expect(r.secondary.map(words)).toEqual(["Stop & send"]);
    expect(words(r.primary)).toBe("Send ↩");
    expect(r.primary.disabled).toBe(false);
  });

  it("tray card waiting", () => {
    const r = f({ state: "card", hasText: true });
    expect([r.glyph, r.text]).toEqual(["◉", "Waiting on the card"]);
    expect(r.secondary).toEqual([]);
    expect(r.primary).toMatchObject({ label: "Queue", variant: "secondary" });
  });

  it("modal", () => {
    const r = f({ state: "modal", hasText: true });
    expect([r.glyph, r.text]).toEqual(["◉", "A screen is open in the terminal"]);
    // The card right above is the way there, and a second button says nothing new.
    expect(r.left).toEqual([]);
    expect(r.textCls).toBe("text-ink-2");
    expect(r.primary).toMatchObject({ label: "Queue", variant: "secondary" });
  });

  it("stopped and held offers one send at a time", () => {
    const empty = f({ state: "held", held: 2, hasText: false });
    expect(empty.left).toEqual([]);
    expect(empty.primary).toMatchObject({ act: "sendHeld", label: "Send them", variant: "primary" });
    expect(empty.primary.disabled).toBeFalsy();
    const typed = f({ state: "held", held: 2, hasText: true });
    expect(typed.left).toEqual(["sendThem"]);
    expect(typed.primary).toMatchObject({ act: "send", label: "Send" });
  });

  it("pane holds a draft", () => {
    const r = f({ state: "draft", hasText: true });
    expect([r.glyph, r.text]).toEqual(["○", "Claude's own prompt has text"]);
    expect(r.left).toEqual(["terminal"]);
    expect(r.primary).toMatchObject({ label: "Queue", variant: "primary" });
  });

  it("stopped, held", () => {
    const r = f({ state: "held", held: 2, hasText: true });
    expect([r.glyph, r.text]).toEqual(["○", "Stopped · 2 held"]);
    expect(r.left).toEqual(["sendThem"]);
    expect(words(r.primary)).toBe("Send ↩");
  });

  it("host in scope, ask", () => {
    const r = f({ state: "resting", target: host, hasText: true });
    expect(r.text).toBe("Resting");
    expect(r.secondary.map(words)).toEqual(["Run on prod-api ⌘↩"]);
    expect(words(r.primary)).toBe("Send ↩");
  });

  it("all, 2+ live hosts", () => {
    const r = f({ state: "working", pick: true });
    expect(r.text).toBe("Working");
    expect(r.hint).toBe("Pick a host above to run");
    expect(r.secondary).toEqual([]);
    expect(words(r.primary)).toBe("Send ↩");
  });

  it("run", () => {
    const r = f({ state: "working", running: true, target: host, hasText: true });
    expect(r.text).toBe("deploy@prod-api:/srv");
    expect(r.mono).toBe(true);
    expect(r.left).toEqual([]);
    expect(r.secondary.map(words)).toEqual(["Ask ↩"]);
    expect(words(r.primary)).toBe("Run on prod-api ⌘↩");
    expect(r.primary.variant).toBe("primary");
  });

  it("run after a double ⌘ names the keys that are true now", () => {
    const r = f({ running: true, runsByDefault: true, target: host });
    expect(r.secondary.map(words)).toEqual(["Ask ⌘↩"]);
    expect(words(r.primary)).toBe("Run on prod-api ↩");
  });

  it("you drive a host", () => {
    const r = f({ state: "resting", driving: "prod-api", target: host, hasText: true });
    expect(r.text).toBe("You're driving prod-api");
    expect(r.left).toEqual(["handBack"]);
    expect(words(r.primary)).toBe("Send ↩");
  });

  it("gone", () => {
    const r = f({ state: "gone", hasText: true });
    expect([r.glyph, r.text]).toEqual(["✕", "Claude exited"]);
    expect(r.secondary.map(words)).toEqual(["Queue ↩"]);
    expect(r.primary).toMatchObject({ act: "revive", label: "Revive", variant: "primary" });
    // Reviving needs no text.
    expect(f({ state: "gone" }).primary.disabled).toBeFalsy();
  });

  it("agent cannot prove an empty prompt", () => {
    const r = f({ state: "unproven", hasText: true });
    expect(r.text).toBe("lola can't confirm this agent's prompt is free");
    expect(r.left).toEqual(["terminal"]);
    expect(r.primary).toMatchObject({ label: "Queue", variant: "secondary" });
  });

  it("daemon down", () => {
    const r = f({ state: "down", hasText: true, target: host, pick: true });
    expect(r.text).toBe(DAEMON_DOWN_LINE);
    expect(r.left).toEqual(["terminal"]);
    expect(r.secondary).toEqual([]);
    expect(r.hint).toBeUndefined();
    expect(words(r.primary)).toBe("Send ↩");
    expect(r.primary.disabled).toBe(true);
  });

  it("usage limit, from 2.3 L", () => {
    const r = f({ state: "quota", resets: "14:00", hasText: true });
    expect([r.glyph, r.text]).toEqual(["◉", "Usage limit · resets 14:00"]);
    expect(r.left).toEqual(["switchAgent"]);
    expect(words(r.primary)).toBe("Queue ↩");
    expect(f({ state: "quota" }).text).toBe("Usage limit");
  });

  it("no Stop where the agent cannot be stopped, and the words stay", () => {
    const r = f({ state: "working", canStop: false, hasText: true });
    expect(r.left).toEqual([]);
    expect(r.secondary).toEqual([]);
    expect(words(r.primary)).toBe("Send ↩");
  });

  it("over the limit, nothing sends", () => {
    expect(f({ hasText: true, over: true }).primary.disabled).toBe(true);
    expect(f({ state: "working", hasText: true, over: true }).secondary[0].disabled).toBe(true);
  });
});

describe("composerState", () => {
  const caps = { feed: true, send: true, stop: true, cards: true };
  const s = (o: any) => composerState({ alive: true, talk: { caps, composer: "resting" } as never, cardWaiting: false, held: 0, ...o });
  const t = (composer: string, more = {}) => ({ talk: { caps, composer, ...more } });

  it("reads each word of the pane", () => {
    expect(s({})).toBe("resting");
    expect(s(t("working"))).toBe("working");
    expect(s(t("draft"))).toBe("draft");
    expect(s(t("modal"))).toBe("modal");
    expect(s(t("gone"))).toBe("gone");
    expect(s(t("unknown"))).toBe("unproven");
  });

  it("puts the daemon first, then a gone agent, and a card over a turn", () => {
    expect(s({ alive: false, agentState: "dead" })).toBe("down");
    expect(s({ agentState: "dead", cardWaiting: true })).toBe("gone");
    expect(s({ ...t("working"), cardWaiting: true })).toBe("card");
    expect(s({ ...t("modal"), cardWaiting: true })).toBe("modal");
  });

  it("says held only with something held", () => {
    expect(s({ ...t("resting", { queueHeld: true }), held: 1 })).toBe("held");
    expect(s({ ...t("resting", { queueHeld: true }), held: 0 })).toBe("resting");
  });

  it("an agent with no send cap never reads as resting", () => {
    expect(s({ talk: { caps: { ...caps, send: false }, composer: "resting" } })).toBe("unproven");
    expect(s({ talk: { caps: { ...caps, send: false }, composer: "unknown" }, agentState: "working" })).toBe("working");
  });

  it("before any answer the axis speaks, and it never promises Send", () => {
    expect(s({ talk: null, agentState: "working" })).toBe("working");
    expect(s({ talk: null, agentState: "idle" })).toBe("pending");
    expect(footerFor({ ...base, state: "pending", hasText: true }).primary.label).toBe("Queue");
  });

  it("names the usage limit", () => {
    expect(s({ ...t("unknown"), inputReason: "quota_limited" })).toBe("quota");
  });
});

describe("runTarget", () => {
  const c = (id: string, state = "connected", o = {}) => ({ id, state, host: id, ...o }) as never;
  it("has none with no host", () => expect(runTarget([], "")).toEqual({ why: "none" }));
  it("takes the only live host under All", () => expect(runTarget([c("a"), c("b", "closed")], "").conn).toMatchObject({ id: "a" }));
  it("takes none with two live under All", () => expect(runTarget([c("a"), c("b")], "")).toEqual({ why: "pick" }));
  it("takes the scope", () => expect(runTarget([c("a"), c("b")], "b").conn).toMatchObject({ id: "b" }));
  it("a host that only asked is not live", () => expect(runTarget([c("a", "requested")], "").why).toBe("none"));
});

describe("the tray's order", () => {
  const conn = (id: string, o = {}) => ({ id, state: "connected", host: id, ...o }) as never;
  const approval = (id: string, conn: string, createdAt: string) => ({ id, at: createdAt, block: { id, conn, status: "pending_approval", createdAt } }) as never;

  it("is oldest first across hosts and kinds, and stable", () => {
    const cards = trayCards({
      connections: [conn("a"), conn("b", { state: "requested", requestedAt: "2026-01-01T00:00:02Z" })],
      items: [approval("b2", "a", "2026-01-01T00:00:03Z"), approval("b1", "a", "2026-01-01T00:00:01Z")],
      activeConn: "",
      agentPrompt: { kind: "permission", hash: "h", lines: [], since: "2026-01-01T00:00:04Z" } as never,
    });
    expect(cards.map((x) => x.id)).toEqual(["approval-b1", "go-b", "approval-b2", "agent-h"]);
    const again = trayCards({ connections: [conn("a")], items: [approval("b1", "a", "2026-01-01T00:00:01Z"), approval("b2", "a", "2026-01-01T00:00:03Z")], activeConn: "" });
    expect(again.map((x) => x.id)).toEqual(["approval-b1", "approval-b2"]);
  });

  it("does not count an agent's own question, and shows it last", () => {
    const cards = trayCards({
      connections: [conn("a")],
      items: [{ id: "q", at: "2026-01-01T00:00:00Z", element: { id: "q", kind: "question", title: "?", revision: 1 } }, approval("b", "a", "2026-01-01T00:00:09Z")] as never,
      activeConn: "",
    });
    expect(cards.map((x) => [x.id, x.tier])).toEqual([["approval-b", 1], ["element-q", 2]]);
  });

  it("drops an answered question and an answered shell prompt", () => {
    const cards = trayCards({
      connections: [conn("a", { prompt: { id: "p1", line: "Password:" } })],
      items: [{ id: "q", element: { id: "q", kind: "question", state: "answered", revision: 1 } }] as never,
      activeConn: "",
      answered: new Set(["p1"]),
    });
    expect(cards).toEqual([]);
  });

  it("gives a sign-in with no question its own card, and a question takes its place", () => {
    expect(trayCards({ connections: [conn("a", { state: "authenticating" })], items: [], activeConn: "" }).map((c) => c.kind)).toEqual(["auth"]);
    expect(trayCards({ connections: [conn("a", { state: "authenticating", prompt: { id: "p", line: "x" } })], items: [], activeConn: "" }).map((c) => c.kind)).toEqual(["prompt"]);
  });

  it("announces one short line", () => {
    const cards = trayCards({ connections: [], items: [], activeConn: "", agentPrompt: { kind: "question", hash: "h", lines: [] } as never });
    expect(trayAnnouncement(cards, "Claude")).toBe("Claude has a question.");
    expect(trayAnnouncement([], "Claude")).toBe("");
  });
});

describe("choices and the queue", () => {
  it("quiets only the dialog's own words", () => {
    expect(isFrameLine("Bash command")).toBe(true);
    expect(isFrameLine("Do you want to proceed?")).toBe(true);
    expect(isFrameLine("rm -rf build")).toBe(false);
  });
  it("only an exact Yes is primary", () => {
    expect(isPrimaryChoice("Yes")).toBe(true);
    for (const l of ["Yes, and always allow access to migrations/", "yes", "Yes ", "No"]) expect(isPrimaryChoice(l)).toBe(false);
  });
  it("Yes and No stay in view when narrow", () => {
    expect(isPlainChoice("No")).toBe(true);
    expect(isPlainChoice("Yes, and switch to auto mode")).toBe(false);
  });
  it("lists what has not gone and was not recalled", () => {
    const h = (id: string, o = {}) => ({ id, human: { text: id, delivered: false, ...o } }) as never;
    expect(queuedItems([h("a"), h("b", { delivered: true }), h("c", { recalled: true })]).map((i) => i.id)).toEqual(["a"]);
  });
});
