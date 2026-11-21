import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";

import NewSession, { branchFor } from "./newSession.svelte";
import { confirm } from "$lib/confirm.svelte";
import { store } from "$lib/store.svelte";
import { router } from "@mobile/lib/router.svelte";
import { copy } from "@mobile/lib/copy";
import { tiers } from "@mobile/lib/tiers.svelte";

describe("branchFor", () => {
  it("derives from the prompt, else a unique fallback", () => {
    expect(branchFor("Fix the login bug")).toBe("lola/fix-the-login-bug");
    expect(branchFor("  ", 36)).toBe("lola/session-10");
  });
});

describe("newSession sheet", () => {
  beforeEach(() => {
    tiers.set(["read", "control"]);
    store.projects = [{ name: "nori", agentOk: true } as never];
    router.stack = [{ name: "tab", tab: "projects", project: "nori" }];
    router.sheet = "newSession";
  });

  it("opens the PR picker for the project from the segment bar", async () => {
    render(NewSession);
    await fireEvent.click(screen.getByRole("tab", { name: copy.newSession.from.pr }));
    expect(router.top).toEqual({ name: "tab", tab: "projects", project: "nori", pick: "prs" });
  });

  it("opens the ticket picker from the segment bar", async () => {
    render(NewSession);
    await fireEvent.click(screen.getByRole("tab", { name: copy.newSession.from.ticket }));
    expect(router.top).toEqual({ name: "tab", tab: "projects", project: "nori", pick: "tickets" });
  });

  it("names the resolved agent in the placeholder and the default option", () => {
    render(NewSession);
    expect(screen.getByPlaceholderText("What should Claude do?")).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Claude" })).toBeInTheDocument();
  });

  it("keeps Start off, with no helper line, until there is a prompt", async () => {
    render(NewSession);
    expect(screen.getByRole("button", { name: copy.newSession.start })).toBeDisabled();
    expect(screen.queryByText(copy.newSession.needsText)).toBeNull();
    await fireEvent.input(screen.getByLabelText("Prompt"), { target: { value: "Add retries" } });
    expect(screen.getByRole("button", { name: copy.newSession.start })).toBeEnabled();
  });

  it("is a fit sheet of inset groups, and closes at once when nothing is typed", async () => {
    confirm.cancel();
    render(NewSession);
    expect(screen.getByRole("dialog")).toHaveAttribute("data-detent", "fit");
    expect(document.querySelectorAll("[data-list-group]").length).toBeGreaterThanOrEqual(2);
    await fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(router.sheet).toBe("");
  });

  it("does not close a typed prompt without asking", async () => {
    confirm.cancel();
    render(NewSession);
    await fireEvent.input(screen.getByLabelText("Prompt"), { target: { value: "Add retries" } });
    await fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(confirm.request?.title).toBe("Discard changes?");
    expect(router.sheet).toBe("newSession");
    confirm.cancel();
  });

  it("starts a worktree session and pushes it", async () => {
    const open = vi.spyOn(store, "openManual").mockResolvedValue({ sessionId: "nori-1" } as never);
    render(NewSession);
    await fireEvent.input(screen.getByLabelText("Prompt"), { target: { value: "Add retries" } });
    await fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect(open).toHaveBeenCalled());
    expect(open.mock.calls[0][0]).toMatchObject({ project: "nori", branch: "lola/add-retries", agent: true, prompt: "Add retries" });
    await waitFor(() => expect(router.top).toMatchObject({ name: "session", id: "nori-1" }));
  });

  it("starts a new branch with a chosen name and base", async () => {
    const open = vi.spyOn(store, "openManual").mockResolvedValue({ sessionId: "nori-3" } as never);
    open.mockClear();
    render(NewSession);
    await fireEvent.input(screen.getByLabelText("Prompt"), { target: { value: "Add retries" } });
    await fireEvent.input(screen.getByLabelText("New branch"), { target: { value: " feat/mine " } });
    await fireEvent.input(screen.getByLabelText("Base branch"), { target: { value: "develop" } });
    await fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect(open).toHaveBeenCalled());
    expect(open.mock.calls.at(-1)![0]).toMatchObject({ branch: "feat/mine", base: "develop", existing: false });
  });

  it("starts on an existing branch when the switch is on", async () => {
    const open = vi.spyOn(store, "openManual").mockResolvedValue({ sessionId: "nori-2" } as never);
    open.mockClear();
    render(NewSession);
    await fireEvent.click(screen.getByRole("switch", { name: copy.newSession.existingBranch }));
    await fireEvent.input(screen.getByLabelText("Prompt"), { target: { value: "Review it" } });
    expect(screen.getByRole("button", { name: "Start" })).toBeDisabled();
    await fireEvent.input(screen.getByLabelText("Branch"), { target: { value: " feature/x " } });
    await fireEvent.click(screen.getByRole("button", { name: "Start" }));
    await waitFor(() => expect(open).toHaveBeenCalled());
    expect(open.mock.calls.at(-1)![0]).toMatchObject({ project: "nori", branch: "feature/x", existing: true });
  });

  it("offers Terminal only just with the shell tier", () => {
    render(NewSession);
    expect(screen.queryByText(copy.newSession.shellOnly)).toBeNull();
  });

  it("offers Terminal only with the shell tier", () => {
    tiers.set(["read", "control", "shell"]);
    render(NewSession);
    expect(screen.getByText(copy.newSession.shellOnly)).toBeInTheDocument();
  });
});
