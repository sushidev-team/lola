import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";

import Rename from "./rename.svelte";
import Broadcast from "./broadcast.svelte";
import SwitchAgent from "./switchAgent.svelte";
import { confirm } from "$lib/confirm.svelte";
import { store } from "$lib/store.svelte";
import { copy } from "@mobile/lib/copy";
import { router } from "@mobile/lib/router.svelte";

beforeEach(() => {
  store.sessions = [{ id: "s1", project: "p", title: "Old", agent: "claude" } as never];
  router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: "s1", seg: "conversation" }];
  router.sheet = "rename";
});

describe("one-field sheets", () => {
  it("rename starts from the title, saves the trimmed name and closes", async () => {
    const rename = vi.spyOn(store, "renameSession").mockResolvedValue(true as never);
    render(Rename);
    const field = screen.getByPlaceholderText(copy.rename.placeholder) as HTMLInputElement;
    expect(field.value).toBe("Old");
    await fireEvent.input(field, { target: { value: "  New  " } });
    await fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(rename).toHaveBeenCalledWith("s1", "New"));
    await waitFor(() => expect(router.sheet).toBe(""));
  });

  it("rename keeps Save off until the name is new and not empty", async () => {
    render(Rename);
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    const field = screen.getByPlaceholderText(copy.rename.placeholder);
    await fireEvent.input(field, { target: { value: "  " } });
    expect(save).toBeDisabled();
    await fireEvent.input(field, { target: { value: "New" } });
    expect(save).toBeEnabled();
  });

  it("rename stays open when the daemon refuses", async () => {
    vi.spyOn(store, "renameSession").mockResolvedValue(undefined as never);
    render(Rename);
    await fireEvent.input(screen.getByPlaceholderText(copy.rename.placeholder), { target: { value: "New" } });
    await fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Save" })).toBeEnabled());
    expect(router.sheet).toBe("rename");
  });

  it("rename selects the name on open, clears it, and asks before discarding a change", async () => {
    render(Rename);
    const field = screen.getByPlaceholderText(copy.rename.placeholder) as HTMLInputElement;
    await waitFor(() => expect(document.activeElement).toBe(field));
    expect(field.selectionStart).toBe(0);
    expect(field.selectionEnd).toBe(3);
    await fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(field.value).toBe("");
    await fireEvent.input(field, { target: { value: "New" } });
    await fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(confirm.request?.title).toBe("Discard changes?");
    expect(router.sheet).toBe("rename");
    confirm.cancel();
  });

  it("broadcast counts the live agents of the project", () => {
    store.sessions = [
      { id: "s1", project: "p", title: "Old", agent: "claude", agentState: "idle" },
      { id: "s2", project: "p", agent: "claude", agentState: "working" },
      { id: "s3", project: "p", agent: "claude", agentState: "dead" },
      { id: "s4", project: "q", agent: "claude", agentState: "idle" },
    ] as never;
    router.sheet = "broadcast";
    render(Broadcast);
    expect(screen.getByText("To 2 agents")).toBeInTheDocument();
  });

  it("broadcast says No agents running and disables Send at zero", async () => {
    store.sessions = [{ id: "s3", project: "p", agent: "claude", agentState: "dead" }] as never;
    router.sheet = "broadcast";
    render(Broadcast);
    expect(screen.getByText("No agents running")).toBeInTheDocument();
    await fireEvent.input(screen.getByPlaceholderText(copy.broadcast.placeholder), { target: { value: "hi" } });
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
  });

  it("broadcast sends to the session's project", async () => {
    const send = vi.spyOn(store, "broadcast").mockResolvedValue({} as never);
    router.sheet = "broadcast";
    render(Broadcast);
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
    await fireEvent.input(screen.getByPlaceholderText(copy.broadcast.placeholder), { target: { value: " rebase please " } });
    await fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(send).toHaveBeenCalledWith("p", "rebase please"));
  });

  it("switch agent marks the current one (full colour, not tappable) and asks before switching", async () => {
    const ask = vi.spyOn(store, "askSwitchAgent").mockImplementation(() => {});
    render(SwitchAgent);
    expect(screen.queryByRole("button", { name: copy.agents.claude })).toBeNull();
    const current = screen.getByText(copy.agents.claude).closest("[data-list-row]") as HTMLElement;
    expect(current).toHaveAttribute("aria-current", "true");
    expect(current.className).not.toContain("opacity-40");
    expect(current.querySelector("[data-row-check]")).not.toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: copy.agents.codex }));
    expect(ask).toHaveBeenCalledWith("s1", "codex");
  });
});
