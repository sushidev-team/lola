import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/svelte";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const { runMock, startDaemon } = vi.hoisted(() => ({ runMock: vi.fn(), startDaemon: vi.fn() }));
vi.mock("@bindings/desktop", () => ({ DoctorService: { Run: runMock } }));
vi.mock("$lib/store.svelte", () => ({
  store: { projects: [] as { name: string; stack?: string; pollsEnabled: number }[], startDaemon },
}));

import SetupChecklist, { checklist, fixFor, FIXES, FOCUS_RECHECK_MS } from "./SetupChecklist.svelte";
import { nav } from "$lib/nav.svelte";
import { store } from "$lib/store.svelte";

const row = (name: string, ok: boolean, extra: Record<string, unknown> = {}) => ({ id: name, name, ok, detail: "", critical: false, ...extra });
const allGood = [
  row("tmux", true), row("git", true), row("claude", true), row("gh", true), row("gh auth", true),
  row("linear api key", true), row("polling", true, { detail: "polling: acme" }),
];
const stateOf = (items: ReturnType<typeof checklist>, id: string) => items.find((i) => i.id === id)!;

beforeEach(() => {
  runMock.mockReset();
  startDaemon.mockReset();
  (store as { projects: unknown[] }).projects = [];
  nav.closeOverlay();
});
afterEach(cleanup);

describe("checklist()", () => {
  it("passes every item on a healthy report", () => {
    expect(checklist(allGood).every((i) => i.state === "ok")).toBe(true);
    expect(checklist(allGood).map((i) => i.id)).toEqual(["tmux", "git", "gh", "agent", "linear", "poll"]);
  });

  it("counts gh only when it is installed AND signed in", () => {
    const items = checklist(allGood.map((r) => (r.name === "gh auth" ? row("gh auth", false, { fix: "gh_auth_login" }) : r)));
    const gh = stateOf(items, "gh");
    expect(gh.state).toBe("todo");
    expect(gh.fix).toEqual({ kind: "command", command: "gh auth login" });
  });

  it("offers the install when gh is missing", () => {
    const items = checklist(allGood.filter((r) => r.name !== "gh auth").map((r) => (r.name === "gh" ? row("gh", false, { fix: "install_gh" }) : r)));
    expect(stateOf(items, "gh").fix).toEqual({ kind: "command", command: "brew install gh" });
  });

  it("names the agent CLI that is missing, with its install command", () => {
    const items = checklist(allGood.map((r) => (r.name === "claude" ? row("claude", false) : r)));
    const agent = stateOf(items, "agent");
    expect(agent.state).toBe("todo");
    expect(agent.sentence).toMatch(/claude/);
    expect(agent.fix).toEqual({ kind: "command", command: "npm install -g @anthropic-ai/claude-code" });
  });

  it("reads an unreported row as unknown, never ok", () => {
    const items = checklist([row("tmux", true)]);
    expect(stateOf(items, "git").state).toBe("unknown");
    expect(stateOf(items, "poll").state).toBe("unknown");
  });

  it("asks for a project, then for a polling team", () => {
    const noProjects = checklist([...allGood.filter((r) => r.name !== "polling"), row("projects", false, { fix: "add_project" })]);
    expect(stateOf(noProjects, "poll").fix).toBe(FIXES.add_project);
    const noPoll = checklist(allGood.map((r) => (r.name === "polling" ? row("polling", false, { fix: "enable_poll" }) : r)));
    expect(stateOf(noPoll, "poll").fix).toBe(FIXES.enable_poll);
  });

  it("judges the key by the setup screen's own check before a config exists", () => {
    const report = [...allGood.filter((r) => r.name !== "linear api key"), row("linear api key", false)];
    expect(stateOf(checklist(report, { setup: true, linearKey: "ok" }), "linear").state).toBe("ok");
    expect(stateOf(checklist(report, { setup: true }), "linear").state).toBe("unknown");
    expect(stateOf(checklist(report, { setup: true }), "poll").fix).toBeNull();
  });

  it("gives a passing row no fix, and an unknown fix id none either", () => {
    expect(fixFor({ ok: true, fix: "gh_auth_login" })).toBeNull();
    expect(fixFor({ ok: false, fix: "not_a_fix" })).toBeNull();
    expect(fixFor({ ok: false, fix: "" })).toBeNull();
  });
});

describe("FIXES actions", () => {
  it("opens the first project that does not poll on its Issue pickup tab", () => {
    (store as { projects: unknown[] }).projects = [
      { name: "a", pollsEnabled: 1 }, { name: "b", pollsEnabled: 0 }, { name: "m", stack: "s", pollsEnabled: 0 },
    ];
    (FIXES.enable_poll as { run: () => void }).run();
    expect(nav.overlay).toBe("project");
    expect(nav.overlayProject).toBe("b");
    expect(nav.overlayTab).toBe("filter");
  });

  it("opens a new project when there is none to enable", () => {
    (FIXES.enable_poll as { run: () => void }).run();
    expect(nav.overlay).toBe("project");
    expect(nav.overlayProject).toBe("");
  });

  it("starts the daemon itself", () => {
    (FIXES.start_daemon as { run: () => void }).run();
    expect(startDaemon).toHaveBeenCalledOnce();
  });
});

describe("SetupChecklist", () => {
  it("runs the checks and shows each item with its state and fix", async () => {
    runMock.mockResolvedValue({ results: [...allGood.filter((r) => r.name !== "gh auth"), row("gh auth", false, { fix: "gh_auth_login" })], summary: "", ok: false });
    render(SetupChecklist);
    expect(await screen.findByText("GitHub CLI")).toBeInTheDocument();
    expect(screen.getByText("gh is installed but not signed in.")).toBeInTheDocument();
    expect(screen.getByText("gh auth login")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("1 left");
  });

  it("copies a fix command", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    runMock.mockResolvedValue({ results: allGood.map((r) => (r.name === "tmux" ? row("tmux", false) : r)), summary: "", ok: false });
    render(SetupChecklist);
    await fireEvent.click(await screen.findByRole("button", { name: "Copy command: brew install tmux" }));
    expect(writeText).toHaveBeenCalledWith("brew install tmux");
    expect(await screen.findByText("Copied")).toBeInTheDocument();
  });

  it("runs an app action from its button", async () => {
    runMock.mockResolvedValue({ results: [...allGood.filter((r) => r.name !== "polling"), row("projects", false, { fix: "add_project" })], summary: "", ok: false });
    render(SetupChecklist);
    await fireEvent.click(await screen.findByRole("button", { name: "Add project" }));
    expect(nav.overlay).toBe("project");
    expect(nav.overlayProject).toBe("");
  });

  it("checks again when the window regains focus, at most once a minute", async () => {
    let now = 1_000_000;
    const clock = vi.spyOn(Date, "now").mockImplementation(() => now);
    try {
      runMock.mockResolvedValue({ results: allGood, summary: "", ok: true });
      render(SetupChecklist);
      await screen.findByText("All set.");
      // A focus right after a run is already answered by it.
      window.dispatchEvent(new Event("focus"));
      await Promise.resolve();
      expect(runMock).toHaveBeenCalledTimes(1);
      now += FOCUS_RECHECK_MS;
      window.dispatchEvent(new Event("focus"));
      await waitFor(() => expect(runMock).toHaveBeenCalledTimes(2));
      // The button is never throttled.
      await screen.findByText("All set.");
      await fireEvent.click(screen.getByRole("button", { name: "Check again" }));
      await waitFor(() => expect(runMock).toHaveBeenCalledTimes(3));
    } finally {
      clock.mockRestore();
    }
  });

  it("does not stay up for an optional item alone", async () => {
    const noPoll = allGood.map((r) => (r.name === "polling" ? row("polling", false, { fix: "enable_poll" }) : r));
    runMock.mockResolvedValue({ results: noPoll, summary: "", ok: false });
    render(SetupChecklist, { hideWhenComplete: true, optional: ["poll"] });
    await waitFor(() => expect(runMock).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByText("Setup checklist")).not.toBeInTheDocument());
  });

  it("stays up while a required item is left, showing the optional one too", async () => {
    const left = allGood.map((r) => (r.name === "polling" ? row("polling", false, { fix: "enable_poll" }) : r.name === "tmux" ? row("tmux", false) : r));
    runMock.mockResolvedValue({ results: left, summary: "", ok: false });
    render(SetupChecklist, { hideWhenComplete: true, optional: ["poll"] });
    expect(await screen.findByRole("status")).toHaveTextContent("2 left");
    expect(screen.getByText("A project picks up issues")).toBeInTheDocument();
  });

  it("hides itself once everything passes, when asked to", async () => {
    runMock.mockResolvedValue({ results: allGood, summary: "", ok: true });
    render(SetupChecklist, { hideWhenComplete: true });
    await waitFor(() => expect(runMock).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByText("Setup checklist")).not.toBeInTheDocument());
  });

  it("says so when the checks cannot run", async () => {
    runMock.mockRejectedValue(new Error("bridge down"));
    render(SetupChecklist);
    expect(await screen.findByRole("alert")).toHaveTextContent("The checks could not run.");
  });
});
