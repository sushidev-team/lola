import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/svelte";

const m = vi.hoisted(() => ({
  HubReadiness: vi.fn(),
  HubProjects: vi.fn(),
  SaveHubProject: vi.fn(),
  Status: vi.fn(),
  Board: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  ConfigService: { HubReadiness: m.HubReadiness, HubProjects: m.HubProjects, SaveHubProject: m.SaveHubProject },
  HubService: { Status: m.Status, Board: m.Board, Org: vi.fn().mockResolvedValue({ devices: [] }) },
}));

import Readiness, { readinessRows, readinessMissing } from "./HubReadiness.svelte";
import { hub } from "$lib/hub.svelte";

const web = { name: "web", label: "Web", repo: "acme/web", suggestedRepo: "", hubChores: false, sandboxed: true, stack: "" };
const api = { name: "api", label: "API", repo: "", suggestedRepo: "acme/api", hubChores: false, sandboxed: false, stack: "" };
const docs = { name: "docs", label: "Docs", repo: "acme/docs", suggestedRepo: "", hubChores: false, sandboxed: false, stack: "" };

const allGood = [
  { name: "hub joined", ok: true, detail: "joined", fix: "" },
  { name: "hub chores", ok: true, detail: "", fix: "" },
  { name: "hub docker", ok: true, detail: "docker answers", fix: "" },
  { name: "hub sandbox image", ok: true, detail: "present: img", fix: "" },
  { name: "hub sandbox login", ok: true, detail: "ok", fix: "" },
  { name: "hub github access", ok: true, detail: "ok", fix: "" },
  { name: "hub policy key", ok: true, detail: "pinned", fix: "" },
];
const row = (id: string) => document.querySelector(`[data-readiness="${id}"]`) as HTMLElement;

describe("readinessRows", () => {
  it("pre-ticks nothing and suggests only projects whose repository is on the board", () => {
    const rows = readinessRows({ doctor: allGood, projects: [web, api, docs], boardRepos: ["acme/web", "ACME/API"] });
    const p = rows.find((r) => r.id === "projects")!;
    expect(p.state).toBe("missing");
    expect(p.suggest?.map((s) => s.name)).toEqual(["web", "api"]);
    expect(rows.find((r) => r.id === "repo")).toBeUndefined();
    expect(readinessMissing(rows)).toBe(true);
  });

  it("maps the doctor's fix ids onto plain rows and fixes", () => {
    const rows = readinessRows({
      doctor: [
        { name: "hub docker", ok: false, detail: "docker does not answer", fix: "start_docker" },
        { name: "hub sandbox image", ok: false, detail: "unknown until docker runs", fix: "" },
        { name: "hub sandbox login", ok: false, detail: "cannot sign in", fix: "sandbox_token" },
        { name: "hub github access", ok: false, detail: "not logged in", fix: "gh_auth_login" },
        { name: "hub policy key", ok: false, detail: "none", fix: "pin_policy_key" },
      ],
      projects: [{ ...web, hubChores: true }],
    });
    const by = Object.fromEntries(rows.map((r) => [r.id, r]));
    expect(by.projects.state).toBe("ok");
    expect(by.repo.state).toBe("ok");
    expect(by.docker).toMatchObject({ state: "missing", fix: { kind: "goto", target: "sandbox" } });
    expect(by.image.state).toBe("info");
    expect(by.login).toMatchObject({ state: "missing", fix: { kind: "goto", target: "sandbox" } });
    expect(by.github).toMatchObject({ state: "missing", fix: { kind: "copy", command: "gh auth login" } });
    // No policy key blocks no chore: worth knowing, not needed.
    expect(by.policy).toMatchObject({ state: "info", fix: { kind: "goto", target: "keys" } });
  });

  it("reads per-repository GitHub access from the findings health", () => {
    const rows = readinessRows({
      doctor: allGood,
      projects: [{ ...web, hubChores: true }],
      health: [{ repo: "acme/web", at: "", alerts: "no_access", pullRequests: "ok" } as never],
    });
    const gh = rows.find((r) => r.id === "github")!;
    expect(gh.state).toBe("missing");
    expect(gh.detail).toContain("acme/web");
  });

  it("is ready when nothing is missing, image download pending is only a note", () => {
    const doctor = allGood.map((r) => (r.name === "hub sandbox image" ? { ...r, detail: "not pulled yet (pulled on the first chore): img" } : r));
    const rows = readinessRows({ doctor, projects: [{ ...web, hubChores: true }] });
    expect(rows.find((r) => r.id === "image")!.state).toBe("info");
    expect(readinessMissing(rows)).toBe(false);
  });
});

describe("HubReadiness", () => {
  beforeEach(() => {
    hub.reset();
    m.Status.mockResolvedValue({ enabled: true, joined: true, claimsToday: 0, maxClaimsPerDay: 4, pendingOutcomes: 0, sessions: [] });
    m.Board.mockResolvedValue({ items: [{ id: "i1", kind: "dep_advisory", repo: "acme/api", label: "x", package: "lodash", state: "open" }] });
    m.HubReadiness.mockResolvedValue(allGood);
    m.HubProjects.mockResolvedValue([web, api]);
    m.SaveHubProject.mockResolvedValue(undefined);
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
    hub.reset();
  });

  it("turns a suggested project on only when the person presses it", async () => {
    const onProjectsChanged = vi.fn();
    render(Readiness, { onProjectsChanged });
    const btn = await screen.findByRole("button", { name: "Turn on API" });
    expect(m.SaveHubProject).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Turn on Web" })).toBeNull();
    m.HubProjects.mockResolvedValue([web, { ...api, repo: "acme/api", hubChores: true }]);
    await fireEvent.click(btn);
    await waitFor(() => expect(m.SaveHubProject).toHaveBeenCalledWith({ name: "api", repo: "acme/api", hubChores: true, sandboxed: null }));
    expect(onProjectsChanged).toHaveBeenCalled();
    await waitFor(() => expect(row("projects").dataset.state).toBe("ok"));
  });

  it("leads each fix to its place and checks again on request and on focus", async () => {
    m.HubReadiness.mockResolvedValue(allGood.map((r) => (r.name === "hub sandbox login" ? { ...r, ok: false, fix: "sandbox_token" } : r)));
    const onFix = vi.fn();
    render(Readiness, { onFix });
    await waitFor(() => expect(row("login")).not.toBeNull());
    await fireEvent.click(within(row("login")).getByRole("button", { name: "Log in" }));
    expect(onFix).toHaveBeenCalledWith("sandbox");
    const calls = m.HubReadiness.mock.calls.length;
    await fireEvent.click(screen.getByRole("button", { name: "Check again" }));
    await waitFor(() => expect(m.HubReadiness.mock.calls.length).toBe(calls + 1));
    window.dispatchEvent(new Event("focus"));
    await waitFor(() => expect(m.HubReadiness.mock.calls.length).toBe(calls + 2));
  });

  it("hides itself at the top of the tab once nothing is missing", async () => {
    m.HubProjects.mockResolvedValue([{ ...web, hubChores: true }]);
    render(Readiness, { hideWhenReady: true });
    await waitFor(() => expect(m.HubReadiness).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 0));
    expect(document.querySelector("[data-hub-readiness]")).toBeNull();
  });
});
