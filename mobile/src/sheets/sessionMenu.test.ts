import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";

const { Kill, Revive } = vi.hoisted(() => ({ Kill: vi.fn(), Revive: vi.fn() }));
vi.mock("@bindings/desktop", () => ({
  DaemonService: { Kill: (...a: unknown[]) => Kill(...a), Revive: (...a: unknown[]) => Revive(...a), List: () => Promise.resolve([]) },
  TermService: { CloseSessionShells: () => Promise.resolve() },
}));

import SessionMenu, { menuRows } from "./sessionMenu.svelte";
import FlashToast from "@mobile/lib/components/FlashToast.svelte";
import { copy } from "@mobile/lib/copy";
import { confirm } from "$lib/confirm.svelte";
import { store } from "$lib/store.svelte";
import { router } from "@mobile/lib/router.svelte";
import { tiers, can } from "@mobile/lib/tiers.svelte";

const base = { id: "s1", project: "p", status: "working", delivery: "", devCommands: null } as never;

describe("menuRows", () => {
  it("hides every row whose command the device lacks", () => {
    tiers.set(["read"]);
    expect(menuRows(base, can)).toEqual(["checkpoints"]);
  });

  it("offers Review plan only while a plan waits, and only with the control tier", () => {
    const waiting = { ...(base as object), plan: { gate: "submitted", text: "p", round: 1 } } as never;
    tiers.set(["read", "control"]);
    expect(menuRows(waiting, can)).toContain("plan");
    expect(menuRows(base, can)).not.toContain("plan");
    expect(menuRows({ ...(base as object), plan: { gate: "approved" } } as never, can)).not.toContain("plan");
    tiers.set(["read"]);
    expect(menuRows(waiting, can)).not.toContain("plan");
  });

  it("offers Review and CodeRabbit only for a PR that is not on an ssh host", () => {
    tiers.set(["read", "control"]);
    expect(menuRows(base, can)).not.toContain("review");
    expect(menuRows(base, can)).not.toContain("coderabbit");
    const pr = { ...(base as object), prNumber: 7 } as never;
    expect(menuRows(pr, can)).toEqual(expect.arrayContaining(["review", "coderabbit"]));
    const ssh = { ...(base as object), prNumber: 7, location: "ssh" } as never;
    expect(menuRows(ssh, can)).not.toContain("review");
    expect(menuRows(ssh, can)).not.toContain("coderabbit");
  });

  it("offers Model after Switch agent for a live session with the answer tier", () => {
    tiers.set(["read", "control", "answer"]);
    const rows = menuRows(base, can);
    expect(rows).toContain("model");
    expect(rows.indexOf("model")).toBe(rows.indexOf("switchAgent") + 1);
    expect(menuRows({ ...(base as object), status: "dead" } as never, can)).not.toContain("model");
    expect(menuRows({ ...(base as object), status: "shell" } as never, can)).not.toContain("model");
    tiers.set(["read", "control"]);
    expect(menuRows(base, can)).not.toContain("model");
  });

  it("offers state-specific rows only in their state", () => {
    tiers.set(["read", "control", "destructive"]);
    const rows = menuRows({ ...(base as object), status: "dead", delivery: "merge_conflict" } as never, can);
    expect(rows).toEqual(expect.arrayContaining(["revive", "resolve", "remove", "rename"]));
    expect(menuRows(base, can)).not.toContain("revive");
    expect(menuRows(base, can)).not.toContain("resolve");
  });
});

describe("sessionMenu sheet", () => {
  afterEach(() => {
    cleanup();
    store.dismissFlash();
    store.noForceKill = null;
    vi.restoreAllMocks();
  });

  beforeEach(() => {
    Kill.mockReset();
    Revive.mockReset();
    tiers.set(["read", "control", "destructive"]);
    store.sessions = [{ ...(base as object) } as never];
    router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: "s1", seg: "conversation" }];
    router.sheet = "sessionMenu";
    confirm.cancel();
  });

  it("opens the rename sheet from Rename", async () => {
    render(SessionMenu);
    await fireEvent.click(screen.getByRole("menuitem", { name: "Rename" }));
    expect(router.sheet).toBe("rename");
  });

  it("opens the model sheet from Model", async () => {
    tiers.set(["read", "control", "destructive", "answer"]);
    render(SessionMenu);
    await fireEvent.click(screen.getByRole("menuitem", { name: copy.session.menu.model }));
    expect(router.sheet).toBe("model");
  });

  it("closes from the Cancel row and keeps Remove in its own group", async () => {
    render(SessionMenu);
    const remove = screen.getByRole("menuitem", { name: copy.session.menu.remove });
    const rename = screen.getByRole("menuitem", { name: copy.session.menu.rename });
    expect(remove.closest("[data-list-group]") ?? remove.parentElement).not.toBe(rename.closest("[data-list-group]") ?? rename.parentElement);
    await fireEvent.click(screen.getByRole("menuitem", { name: copy.session.menu.cancel }));
    expect(router.sheet).toBe("");
  });

  it("is an action sheet: no header, groups in order, Cancel centred on its own", async () => {
    tiers.set(["read", "control", "destructive", "answer"]);
    store.sessions = [{ ...(base as object), prNumber: 7, devCommands: ["npm run dev"] } as never];
    render(SessionMenu);
    expect(document.querySelector("[data-sheet-header]")).toBeNull();
    const names = (el: HTMLElement) => [...el.querySelectorAll("[role=menuitem]")].map((r) => r.textContent?.trim());
    const groups = [...document.querySelectorAll<HTMLElement>("[data-list-group]")].map(names);
    expect(groups).toEqual([
      ["Rename", "Switch agent", "Model"],
      ["Tasks", "Checkpoints", "Message all", "Start dev server"],
      ["Agent review", "CodeRabbit review"],
      ["Remove session"],
      ["Cancel"],
    ]);
    const cancel = screen.getByRole("menuitem", { name: "Cancel" });
    expect(cancel.className).toContain("min-h-14");
    expect(cancel.querySelector("span.font-semibold")).not.toBeNull();
  });

  it("asks through the shared confirm before removing", async () => {
    const kill = vi.spyOn(store, "kill").mockResolvedValue(undefined);
    render(SessionMenu);
    await fireEvent.click(screen.getByRole("menuitem", { name: copy.session.menu.remove }));
    expect(router.sheet).toBe("");
    expect(confirm.request?.title).toBe("Remove session?");
    expect(kill).not.toHaveBeenCalled();
    confirm.accept();
    expect(kill).toHaveBeenCalledWith("s1", false, false, expect.any(Function));
  });

  it("opens the dev links sheet from Open dev URL", async () => {
    store.sessions = [{ ...(base as object), devActive: true, devForwards: [{ url: "http://10.0.0.2:5173" }, { url: "http://10.0.0.2:3000" }] } as never];
    render(SessionMenu);
    await fireEvent.click(screen.getByRole("menuitem", { name: copy.session.menu.devUrl }));
    expect(router.sheet).toBe("devLinks");
  });

  it("shows the daemon's sentence when it refuses an action", async () => {
    Revive.mockRejectedValue(new Error("revive refused: the session is not dead"));
    store.sessions = [{ ...(base as object), status: "dead" } as never];
    render(FlashToast);
    render(SessionMenu);
    await fireEvent.click(screen.getByRole("menuitem", { name: "Revive" }));
    await waitFor(() => expect(screen.getByText(/revive refused: the session is not dead/)).toBeTruthy());
  });

  it("says to remove a dirty worktree on the Mac instead of offering a force", async () => {
    store.noForceKill = copy.notices.dirtyKill.body;
    Kill.mockRejectedValue(
      new Error("session s1 terminated; worktree kept (uncommitted changes) at /w/s1 — rerun with --force to remove it"),
    );
    render(FlashToast);
    render(SessionMenu);
    await fireEvent.click(screen.getByRole("menuitem", { name: copy.session.menu.remove }));
    confirm.accept();
    await waitFor(() => expect(screen.getByText(copy.notices.dirtyKill.body)).toBeTruthy());
    expect(confirm.request).toBeNull();
    expect(Kill).toHaveBeenCalledTimes(1);
    expect(Kill).toHaveBeenCalledWith("s1", false, false);
  });
});
