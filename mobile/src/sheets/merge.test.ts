import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";

const readiness = vi.fn();
const mergePR = vi.fn(async (..._a: unknown[]) => {});
vi.mock("$lib/prreadiness", () => ({ readPRReadiness: (...a: unknown[]) => readiness(...a) }));
vi.mock("@bindings/desktop", () => ({ DaemonService: { MergePR: (...a: unknown[]) => mergePR(...a) } }));

import Merge from "./merge.svelte";
import { confirm } from "$lib/confirm.svelte";
import { store } from "$lib/store.svelte";
import { router } from "@mobile/lib/router.svelte";

const data = (over: object = {}) => ({
  repo: "o/r", number: 4, url: "", state: "OPEN", draft: false, mergeable: "MERGEABLE", mergeState: "CLEAN",
  review: "APPROVED", checks: "pass", head: "abc", threads: [], mergeMethod: "squash", mergeBlocked: "", ...over,
});

describe("merge sheet", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    store.sessions = [{ id: "s1", prNumber: 4 } as never];
    store.refresh = vi.fn(async () => {}) as never;
    router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: "s1", seg: "review" }];
    router.sheet = "merge";
    confirm.cancel();
  });

  it("merges a ready PR with the daemon's method and closes", async () => {
    readiness.mockResolvedValue(data());
    render(Merge);
    const btn = await screen.findByRole("button", { name: "Squash and merge" });
    await waitFor(() => expect(btn).toBeEnabled());
    await fireEvent.click(btn);
    await waitFor(() => expect(mergePR).toHaveBeenCalledWith("s1", 4, "o/r", "abc", "squash", false));
    await waitFor(() => expect(router.sheet).toBe(""));
  });

  it("has a leading Cancel, no Done, and the merge button pinned in the footer", async () => {
    readiness.mockResolvedValue(data());
    render(Merge);
    expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Done" })).toBeNull();
    const btn = await screen.findByRole("button", { name: "Squash and merge" });
    expect(btn.closest("[data-sheet-footer]")).not.toBeNull();
  });

  it("remembers a chosen method", async () => {
    readiness.mockResolvedValue(data());
    render(Merge);
    await fireEvent.click(screen.getByRole("tab", { name: "Rebase" }));
    expect(localStorage.getItem("lola.mergeMethod")).toBe("rebase");
  });

  it("says in one line where the PR lands, and lists no facts", async () => {
    readiness.mockResolvedValue(data({ base: "main" }));
    render(Merge);
    expect(await screen.findByText("Into main.")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Readiness" })).toBeNull();
  });

  it("the call to action follows the method", async () => {
    readiness.mockResolvedValue(data());
    render(Merge);
    await fireEvent.click(await screen.findByRole("tab", { name: "Rebase" }));
    expect(screen.getByRole("button", { name: "Rebase and merge" })).toBeInTheDocument();
  });

  it("an unready PR merges only through a second confirmation", async () => {
    readiness.mockResolvedValue(data({ mergeBlocked: "Checks are failing." }));
    render(Merge);
    expect(await screen.findByText("Not ready.")).toBeInTheDocument();
    const anyway = screen.getByRole("button", { name: "Merge anyway" });
    await waitFor(() => expect(anyway).toBeEnabled());
    await fireEvent.click(anyway);
    expect(mergePR).not.toHaveBeenCalled();
    confirm.accept();
    await waitFor(() => expect(mergePR).toHaveBeenCalledWith("s1", 4, "o/r", "abc", "squash", true));
  });

  it("shows a refusal and stays open", async () => {
    readiness.mockResolvedValue(data());
    mergePR.mockRejectedValueOnce(new Error("blocked by protection"));
    render(Merge);
    const btn = await screen.findByRole("button", { name: "Squash and merge" });
    await waitFor(() => expect(btn).toBeEnabled());
    await fireEvent.click(btn);
    expect(await screen.findByRole("alert")).toHaveTextContent("blocked by protection");
    expect(router.sheet).toBe("merge");
  });
});
