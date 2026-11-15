import { describe, expect, it } from "vitest";
import { parseLocation } from "../lib/router.svelte";
import { WireRefusalError, parseTopic, type Frame, type WatchData } from "../wire";
import { DROP_AFTER_MS, previewTransport } from "./daemon";
import { FIXTURE_NAMES, fixtureFor, isFixtureName, loadFixture, type Fixture } from "./index";
import { readParams } from "./params";

const all = async (): Promise<Fixture[]> => Promise.all(FIXTURE_NAMES.map(loadFixture));

describe("fixture registry", () => {
  it("names every fixture of the spec and each file carries its own name", async () => {
    expect(FIXTURE_NAMES).toHaveLength(17);
    for (const name of FIXTURE_NAMES) expect((await loadFixture(name)).name).toBe(name);
  });

  it("serves only routes and sheets inside the router's grammar", async () => {
    for (const fx of await all()) {
      expect(fx.routes.length, fx.name).toBeGreaterThan(0);
      for (const path of fx.routes) {
        const at = parseLocation(path, { paired: fx.daemons.length > 0 });
        expect(at, `${fx.name} ${path}`).not.toBeNull();
        // A paired phone never lands on Connect, so Connect belongs to the unpaired fixture.
        if (path.startsWith("/connect")) expect(fx.daemons, fx.name).toHaveLength(0);
      }
    }
  });

  it("finds the fixture of a route without a fixture parameter", async () => {
    expect(await fixtureFor("/connect/expired")).toBe("pairing");
    expect(await fixtureFor("/session/s4/changes")).toBe("diff");
    expect(await fixtureFor("/settings")).toBe("settings");
    expect(await fixtureFor("/inbox")).toBe("busy");
    expect(await fixtureFor("/nowhere")).toBeNull();
  });

  it("keeps ids unique and panes named the way the daemon names them", async () => {
    for (const fx of await all()) {
      const ids = fx.sessions.map((s) => s.id);
      expect(new Set(ids).size, fx.name).toBe(ids.length);
      for (const s of fx.sessions) expect(s.tmuxName).toBe(`lola-${s.id}`);
    }
  });
});

describe("fixture content (spec 5.6)", () => {
  it("busy: fourteen sessions over every column and thirty events", async () => {
    const fx = await loadFixture("busy");
    expect(fx.sessions).toHaveLength(14);
    expect(fx.events).toHaveLength(30);
    const state = (s: Fixture["sessions"][number]) => `${s.agentState}/${s.delivery}`;
    expect(fx.sessions.filter((s) => s.agentState === "waiting_input")).toHaveLength(3);
    expect(fx.sessions.some((s) => state(s) === "idle/ci_failed")).toBe(true);
    expect(fx.sessions.some((s) => state(s) === "idle/approved")).toBe(true);
    expect(fx.sessions.filter((s) => s.delivery === "merged")).toHaveLength(2);
  });

  it("permission and question: the prompts the cards render", async () => {
    const perm = (await loadFixture("permission")).talk.s1.prompt;
    expect(perm?.kind).toBe("permission");
    expect(perm?.choices?.map((c) => c.label)).toEqual(["Yes", "Yes, and don't ask again", "No"]);
    const ask = (await loadFixture("question")).talk.s2.prompt;
    expect(ask?.kind).toBe("question");
    expect(ask?.choices).toHaveLength(4);
  });

  it("long-story: 300 items, forty tool rows or more, one subagent", async () => {
    const items = (await loadFixture("long-story")).feeds.s3;
    expect(items).toHaveLength(300);
    expect(items.filter((i) => i.kind === "tool").length).toBeGreaterThanOrEqual(40);
    expect(items.filter((i) => i.tool?.subagent)).toHaveLength(1);
    expect(new Set(items.map((i) => i.id)).size).toBe(300);
    expect(items.map((i) => i.seq)).toEqual([...items.keys()].map((i) => i + 1));
  });

  it("diff: forty files, about two thousand lines and three drafts", async () => {
    const fx = await loadFixture("diff");
    const files = fx.diffs.s4.files ?? [];
    expect(files).toHaveLength(40);
    const lines = files.reduce((n, f) => n + (f.patch?.split("\n").length ?? 0), 0);
    expect(lines).toBeGreaterThanOrEqual(2000);
    expect(lines).toBeLessThan(2200);
    const saved = JSON.parse(fx.storage["lola.diff.drafts"]) as Record<string, { drafts: unknown[] }>;
    expect(saved.s4.drafts).toHaveLength(3);
  });

  it("merge-ready and ci-red: the readiness the sheets read", async () => {
    const ok = (await loadFixture("merge-ready")).readiness.s5;
    expect([ok.state, ok.mergeable, ok.checks, ok.review]).toEqual(["OPEN", "MERGEABLE", "pass", "approved"]);
    const red = await loadFixture("ci-red");
    expect(red.sessions[0].board?.checks?.filter((c) => c.state === "fail")).toHaveLength(2);
    expect(red.readiness.s6.checks).toBe("fail");
  });

  it("review-package: two packages and three screenshots", async () => {
    const data = (await loadFixture("review-package")).packages.s5;
    expect(data.packages).toHaveLength(2);
    expect(data.packages?.reduce((n, p) => n + (p.screens?.length ?? 0), 0)).toBe(3);
  });

  it("terminal: an 80x24 screen for the agent pane", async () => {
    const fx = await loadFixture("terminal");
    expect(fx.screens["lola-s1"]).toMatchObject({ cols: 80, rows: 24 });
    expect(fx.panes.s1[0]).toMatchObject({ name: "lola-s1", kind: "agent" });
  });

  it("offline, key-changed, pairing and settings", async () => {
    expect((await loadFixture("offline")).link).toEqual({ kind: "down", code: "unreachable" });
    expect((await loadFixture("key-changed")).link).toEqual({ kind: "down", code: "key_changed" });
    expect((await loadFixture("pairing")).daemons).toEqual([]);
    const settings = await loadFixture("settings");
    expect(settings.path.kind).toBe("relay");
    expect(settings.tiers).toEqual(["read", "answer", "control"]);
  });
});

describe("PreviewDaemon", () => {
  async function connected(name: (typeof FIXTURE_NAMES)[number]) {
    const fx = await loadFixture(name);
    const t = previewTransport(fx);
    await t.connect({ daemonId: fx.daemons[0]?.daemonId ?? "none" });
    return { fx, t };
  }

  it("answers a read on its id and refuses an unscripted command", async () => {
    const { fx, t } = await connected("busy");
    const data = await t.request<{ sessions: unknown[] }>("sessions");
    expect(data.sessions).toHaveLength(fx.sessions.length);
    await expect(t.request("noSuchCommand")).rejects.toBeInstanceOf(WireRefusalError);
    await expect(t.request("agentSay", { args: { session: "s1", text: "hi" } })).resolves.toEqual({});
  });

  it("starts a watch with per-topic results and a first event that resets", async () => {
    const { fx, t } = await connected("permission");
    const events: Frame[] = [];
    t.onEvent((f) => events.push(f));
    const topics = ["sessions", "projects", "status", "talk:s1", "feed:s1", "timeline:s1", "diff:s1", "talk:nope", "bogus"];
    const { id, done } = t.begin<WatchData>("watch", { args: { topics } });
    const data = await done;
    expect(data.topics.map((r) => [r.topic, r.ok, r.code])).toEqual([
      ["sessions", true, undefined],
      ["projects", true, undefined],
      ["status", true, undefined],
      ["talk:s1", true, undefined],
      ["feed:s1", true, undefined],
      ["timeline:s1", true, undefined],
      ["diff:s1", true, undefined],
      ["talk:nope", false, "unknown_session"],
      ["bogus", false, "invalid_topic"],
    ]);
    await new Promise((r) => setTimeout(r, 20));
    expect(events.map((e) => [e.topic, e.id, e.seq])).toEqual(topics.slice(0, 7).map((topic) => [topic, id, 1]));
    const sessions = events.find((e) => e.topic === "sessions")!.payload as { reset: boolean; upsert: unknown[] };
    expect(sessions.reset).toBe(true);
    expect(sessions.upsert).toHaveLength(fx.sessions.length);
    for (const topic of topics.slice(0, 7)) expect(parseTopic(topic)).not.toBeNull();
  });

  it("subscribes a pane with its fixture screen", async () => {
    const { fx, t } = await connected("terminal");
    const sub = await t.subscribe("lola-s1", { cols: 80, rows: 24 });
    expect(sub.screen?.lines).toEqual(fx.screens["lola-s1"].lines);
  });

  it("drops a down link after its first snapshot and keeps the epoch's data", async () => {
    const { t } = await connected("key-changed");
    const dropped = new Promise<void>((resolve) =>
      t.onStatus((s) => {
        if (s.phase !== "ready") resolve();
      }),
    );
    await t.begin("watch", { args: { topics: ["sessions"] } }).done;
    await dropped;
    expect(t.status.failure).toBe("key_changed");
    expect(DROP_AFTER_MS).toBeGreaterThan(0);
  });
});

describe("review fixtures", () => {
  it("ci-red lists each check, merge-ready carries a tally, review-package opens on screenshots", async () => {
    const red = (await loadFixture("ci-red")).readiness.s6.checkList ?? [];
    expect(red.filter((c) => c.bucket === "fail" && !c.gate).map((c) => c.name)).toEqual(["Unit tests", "Parallel suite"]);
    expect(red.some((c) => c.gate)).toBe(true);
    expect(red.every((c) => !c.url || c.url.startsWith("https://"))).toBe(true);
    const ready = await loadFixture("merge-ready");
    expect(ready.sessions[0].reviewTallies?.[0]).toMatchObject({ kind: "codex-session", major: 1, minor: 2 });
    expect((ready.readiness.s5.checkList ?? []).every((c) => c.bucket === "pass")).toBe(true);
    expect((await loadFixture("review-package")).routes).toContain("/session/s5/review?sheet=reviewPackage&tab=visuals");
    expect((await loadFixture("ci-red")).routes).toContain("/session/s6/review?sheet=checks");
  });
});

describe("readParams", () => {
  it("folds sheet into the route's own query", () => {
    expect(readParams("?route=/inbox&sheet=filter").path).toBe("/inbox?sheet=filter");
    expect(readParams("?route=/inbox%3Ffilter%3Dneeds&sheet=filter").path).toBe("/inbox?filter=needs&sheet=filter");
    expect(readParams("?route=/inbox?sheet=filter").path).toBe("/inbox?sheet=filter");
  });

  it("folds a sheet's tab after its sheet, and only with one", () => {
    expect(readParams("?route=/session/s5/review&sheet=reviewPackage&tab=visuals").path).toBe("/session/s5/review?sheet=reviewPackage&tab=visuals");
    expect(readParams("?route=/session/s5/review&tab=visuals").path).toBe("/session/s5/review");
  });

  it("ignores what it does not know", () => {
    const p = readParams("?fixture=nope&kb=-4&theme=catppuccin-latte");
    expect(p).toEqual({ path: "", fixture: null, theme: "catppuccin-latte", kb: 0 });
    expect(readParams("?kb=99999").kb).toBe(600);
    expect(readParams("?kb=abc").kb).toBe(0);
    expect(isFixtureName("busy")).toBe(true);
    expect(isFixtureName("constructor")).toBe(false);
  });
});
