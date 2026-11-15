import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { store } from "$lib/store.svelte";
import { PrPickerModel, TicketPickerModel, projectActions } from "./projects.svelte";
import { ScriptedDaemon, project } from "./projects.testkit";
import { router } from "./router.svelte";

const all = () => true;
const d = new ScriptedDaemon();

beforeEach(async () => {
  await d.install();
  d.replies = {};
  store.projects = [project()];
  store.sessions = [];
  router.stack = [{ name: "tab", tab: "projects", project: "nori-app", pick: "prs" }];
});
afterEach(() => d.uninstall());

describe("projectActions", () => {
  it("draws no row whose command the tiers lack", () => {
    const ids = projectActions(project(), (c) => c !== "openPr" && c !== "pollOnce").map((a) => a.id);
    expect(ids).toEqual(["tickets", "worktree", "polling"]);
  });

  it("explains a row the Mac's config rules out", () => {
    const rows = projectActions(project({ repoConfigured: false, pollCount: 0 }), all);
    expect(rows.find((a) => a.id === "prs")).toMatchObject({ enabled: false, hint: "No repo set." });
    expect(rows.find((a) => a.id === "pollOnce")).toMatchObject({ enabled: false });
  });

  it("turns every launch row off while the agent is not ready", () => {
    const rows = projectActions(project({ agentOk: false }), all);
    for (const id of ["prs", "tickets", "worktree"])
      expect(rows.find((a) => a.id === id)?.enabled).toBe(false);
  });

  it("offers the opposite poll command of the current state", () => {
    expect(projectActions(project({ pollsEnabled: 1 }), all).at(-1)?.label).toBe("Pause new tickets");
    expect(projectActions(project({ pollsEnabled: 0 }), all).at(-1)?.label).toBe("Resume new tickets");
    expect(projectActions(project({ pollsEnabled: 1 }), (c) => c === "enable")).toHaveLength(0);
  });
});

describe("PrPickerModel", () => {
  const pr = { number: 34, title: "T", author: "a", branch: "feat/x", isDraft: false, isFork: true, checks: "pass", review: "", url: "", status: "approved", alreadyOpen: false };

  it("opens the tapped row's own fields and lands on the new session", async () => {
    d.replies.openPr = { ok: true, data: { sessionId: "nori-app-34" } };
    const m = new PrPickerModel();
    await m.open("nori-app", pr);
    expect(d.payloadOf("openPr")).toEqual({
      cmd: "openPr",
      args: { project: "nori-app", branch: "feat/x", number: 34, isFork: true },
    });
    expect(router.top).toEqual({ name: "session", id: "nori-app-34", seg: "conversation" });
    expect(router.stack[0]).toEqual({ name: "tab", tab: "projects", project: "nori-app" });
  });

  it("keeps the picker and the daemon's sentence on a refusal", async () => {
    d.replies.openPr = { ok: false, error: "already open in session nori-app-9" };
    const m = new PrPickerModel();
    await m.open("nori-app", pr);
    expect(m.refusal).toContain("already open in session nori-app-9");
    expect(router.top).toMatchObject({ name: "tab", pick: "prs" });
    expect(m.opening).toBe(0);
  });

  it("sends refresh only when asked", async () => {
    d.replies.prs = { ok: true, data: { repo: "o/n", prs: [], ageSeconds: 5, stale: false } };
    const m = new PrPickerModel();
    await m.load("nori-app");
    expect(d.payloadOf("prs")).toEqual({ cmd: "prs", args: { project: "nori-app", refresh: false } });
    await m.load("nori-app", true);
    expect(m.loading).toBe(false);
    expect(m.snapshotAt).toBeDefined();
  });
});

describe("TicketPickerModel", () => {
  const row = { identifier: "NOR-1", uuid: "u1", title: "T", branch: "nor-1", priority: 1, alreadyLive: false };

  it("starts an issue with its own fields", async () => {
    d.replies.openTicket = { ok: true, data: { sessionId: "nori-app-1" } };
    const m = new TicketPickerModel();
    await m.start("nori-app", row);
    expect(d.payloadOf("openTicket")).toEqual({
      cmd: "openTicket",
      args: { project: "nori-app", identifier: "NOR-1", uuid: "u1", branch: "nor-1", title: "T" },
    });
    expect(router.top).toMatchObject({ name: "session", id: "nori-app-1" });
  });

  it("opens the live session of a running issue and sends nothing", async () => {
    store.sessions = [{ id: "nori-app-7", project: "nori-app", issue: "NOR-1" } as never];
    const m = new TicketPickerModel();
    await m.start("nori-app", { ...row, alreadyLive: true });
    expect(d.count("openTicket")).toBe(0);
    expect(router.top).toMatchObject({ name: "session", id: "nori-app-7" });
  });

  it("never lets a slow older scope overwrite a newer one", async () => {
    const m = new TicketPickerModel();
    d.replies.tickets = { ok: true, data: { team: "t", issues: [row] } };
    const first = m.load("nori-app");
    m.scope = "team";
    d.replies.tickets = { ok: true, data: { team: "t", issues: [] } };
    await Promise.all([first, m.load("nori-app")]);
    expect(m.rows).toEqual([]);
    expect(m.loading).toBe(false);
  });

  it("prints the daemon's failure verbatim", async () => {
    d.replies.tickets = { ok: false, error: "project nori-app has no Linear team" };
    const m = new TicketPickerModel();
    await m.load("nori-app");
    expect(m.error).toContain("no Linear team");
    expect(m.data).toBeNull();
  });
});
