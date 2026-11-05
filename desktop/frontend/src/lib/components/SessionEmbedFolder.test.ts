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
}));

const { default: SessionEmbed } = await import("./SessionEmbed.svelte");
const { store } = await import("$lib/store.svelte");
const { terms, DIFF } = await import("$lib/terms.svelte");

beforeEach(() => {
  cleanup();
  OpenFolder.mockReset().mockResolvedValue(undefined);
  store.flash = null;
  terms.select("s1", DIFF);
});

it.each(["", "main", "temporary"])("opens the session directory for location %s", async (location) => {
  store.sessions = [{ id: "s1", worktree: "/code/My session", location }] as never;
  render(SessionEmbed, { sessionId: "s1" });
  await fireEvent.click(screen.getByRole("button", { name: "Open session folder in Finder" }));
  expect(OpenFolder).toHaveBeenCalledWith("/code/My session");
});

it("does not offer Finder without a session directory", () => {
  store.sessions = [{ id: "s1", worktree: "" }] as never;
  render(SessionEmbed, { sessionId: "s1" });
  expect(screen.queryByRole("button", { name: "Open session folder in Finder" })).not.toBeInTheDocument();
});

it("shows failures from opening the directory", async () => {
  OpenFolder.mockRejectedValue(new Error("folder no longer exists"));
  store.sessions = [{ id: "s1", worktree: "/missing" }] as never;
  render(SessionEmbed, { sessionId: "s1" });
  await fireEvent.click(screen.getByRole("button", { name: "Open session folder in Finder" }));
  await waitFor(() => expect(store.flash?.text).toContain("folder no longer exists"));
});

it("offers a new SSH tab from the header, on every kind of session", async () => {
  const { nav } = await import("$lib/nav.svelte");
  for (const extra of [{}, { location: "ssh" }]) {
    store.sessions = [{ id: "s1", worktree: "/code/x", ...extra }] as never;
    render(SessionEmbed, { sessionId: "s1" });
    await fireEvent.click(screen.getByRole("button", { name: "Open a new SSH tab" }));
    expect(nav.overlay).toBe("sshconnect");
    expect(nav.overlayProject).toBe("s1");
    nav.closeOverlay();
    cleanup();
  }
});

it("gives each connection its own tab, named after its host, with a delete control", async () => {
  const { ssh } = await import("$lib/ssh.svelte");
  const trash = vi.spyOn(ssh, "trashTab").mockImplementation(() => {});
  const a = { id: "c1", host: "dev", label: "Dev", state: "connected" };
  const b = { id: "c2", host: "dev", label: "Dev", state: "closed" };
  const c = { id: "c3", host: "api", label: "API", state: "requested" };
  const d = { id: "c4", host: "api", label: "API", state: "denied" };
  store.sessions = [{ id: "s1", worktree: "/code/x", location: "ssh", ssh: { connections: [a, b, c, d] } }] as never;
  render(SessionEmbed, { sessionId: "s1" });
  const tabs = [...document.querySelectorAll("[data-ssh-tab]")].map((t) => t.getAttribute("data-ssh-tab"));
  expect(tabs).toEqual(["c1", "c2", "c3"]);
  await fireEvent.click(screen.getByRole("button", { name: "Delete the Dev 2 tab" }));
  expect(trash).toHaveBeenCalledWith("s1", b);
  await fireEvent.click(screen.getByRole("button", { name: "API 1" }));
  expect(ssh.view("s1").activeConn).toBe("c3");
  trash.mockRestore();
});

it("falls back to the agent when the SSH tab it was showing no longer exists", () => {
  store.sessions = [{ id: "s1", worktree: "/code/x" }] as never;
  terms.select("s1", "ssh");
  render(SessionEmbed, { sessionId: "s1" });
  expect(document.querySelector("[data-ssh-workspace]")).toBeNull();
});
