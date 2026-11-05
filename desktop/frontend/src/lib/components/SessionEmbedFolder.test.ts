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
