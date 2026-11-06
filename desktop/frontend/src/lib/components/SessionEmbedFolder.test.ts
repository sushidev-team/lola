import { beforeEach, it, expect, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/svelte";

const OpenFolder = vi.fn();
vi.mock("@bindings/desktop", () => ({
  DaemonService: {
    Diff: vi.fn().mockResolvedValue({ session: "s1", base: "main", mergeBase: "abc", files: [] }),
    OpenFolder: (...args: unknown[]) => OpenFolder(...args),
  },
  ConfigService: {},
  TermService: { Shells: vi.fn(async () => []) },
  TalkService: { State: vi.fn(async () => ({ caps: {}, composer: "unknown", feedSeq: 0, health: "ok" })) },
}));

const { default: SessionEmbed } = await import("./SessionEmbed.svelte");
const { default: SessionMenu } = await import("./SessionMenu.svelte");
const { store } = await import("$lib/store.svelte");
const { nav } = await import("$lib/nav.svelte");
const { sessionMenu } = await import("$lib/sessionmenu.svelte");
const { terms, DIFF, SSH } = await import("$lib/terms.svelte");

beforeEach(() => {
  cleanup();
  OpenFolder.mockReset().mockResolvedValue(undefined);
  store.flash = null;
  terms.select("s1", DIFF);
  nav.focusedTerm = "s1";
});

async function fromTheMenu(name: string) {
  sessionMenu.request = { id: "s1", x: 1, y: 1 };
  render(SessionMenu);
  await fireEvent.click(screen.getByRole("menuitem", { name }));
}

it.each(["", "main", "temporary"])("opens the session directory for location %s", async (location) => {
  store.sessions = [{ id: "s1", worktree: "/code/My session", location }] as never;
  await fromTheMenu("Open in Finder");
  expect(OpenFolder).toHaveBeenCalledWith("/code/My session");
});

it("does not offer Finder without a session directory", () => {
  store.sessions = [{ id: "s1", worktree: "" }] as never;
  sessionMenu.request = { id: "s1", x: 1, y: 1 };
  render(SessionMenu);
  expect(screen.queryByRole("menuitem", { name: "Open in Finder" })).not.toBeInTheDocument();
});

it("shows failures from opening the directory", async () => {
  OpenFolder.mockRejectedValue(new Error("folder no longer exists"));
  store.sessions = [{ id: "s1", worktree: "/missing" }] as never;
  await fromTheMenu("Open in Finder");
  await waitFor(() => expect(store.flash?.text).toContain("folder no longer exists"));
});

it("offers Connect from the strip's +, on every kind of session that can have a host", async () => {
  for (const extra of [{}, { location: "ssh" }]) {
    store.sessions = [{ id: "s1", worktree: "/code/x", ...extra }] as never;
    render(SessionEmbed, { sessionId: "s1" });
    await fireEvent.click(screen.getByRole("button", { name: "Add a terminal or a host" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Connect to host…" }));
    expect(nav.overlay).toBe("sshconnect");
    expect(nav.overlayProject).toBe("s1");
    nav.closeOverlay();
    cleanup();
  }
});

it("gives each live host one `$` cell with no ×, and a click leaves the scope alone", async () => {
  const { ssh } = await import("$lib/ssh.svelte");
  const a = { id: "c1", host: "dev", label: "Dev", state: "connected" };
  const b = { id: "c2", host: "dev", label: "Dev", state: "closed" };
  const c = { id: "c3", host: "api", label: "API", state: "connected" };
  const d = { id: "c4", host: "api", label: "API", state: "denied" };
  store.sessions = [{ id: "s1", worktree: "/code/x", location: "ssh", ssh: { connections: [a, b, c, d] } }] as never;
  terms.select("s1", SSH);
  render(SessionEmbed, { sessionId: "s1" });
  const cells = [...document.querySelectorAll("[data-strip-cell]")].map((t) => t.getAttribute("data-strip-cell"));
  expect(cells).toEqual(["conversation", "terminal", "host:c1", "host:c3"]);
  const cell = document.querySelector('[data-strip-cell="host:c3"]') as HTMLElement;
  expect(cell.parentElement?.querySelector('[aria-label*="close"]')).toBeNull();
  await fireEvent.click(cell);
  expect(ssh.view("s1").shellConn).toBe("c3");
  expect(ssh.view("s1").scope).toBe("");
  expect(terms.activeTab("s1")).toBe(SSH);
});

it("falls back to the terminal when the conversation it was showing is no longer offered", () => {
  store.sessions = [{ id: "s1", worktree: "/code/x", location: "temporary", status: "shell", agentState: "shell" }] as never;
  terms.select("s1", "ssh");
  render(SessionEmbed, { sessionId: "s1" });
  expect(document.querySelector("[data-ssh-workspace]")).toBeNull();
  // Agentless with one terminal: no strip at all, and the terminal is what is on show.
  expect(screen.queryByRole("tab", { name: "Conversation" })).toBeNull();
  expect(terms.activeTab("s1")).toBe("agent");
});
