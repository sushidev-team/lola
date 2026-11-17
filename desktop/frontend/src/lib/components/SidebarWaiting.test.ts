import { describe, it, expect, vi, beforeEach } from "vitest";
import { tick } from "svelte";
import { render, screen, cleanup } from "@testing-library/svelte";

vi.mock("@bindings/desktop", () => ({ SSHService: {}, DaemonService: {}, ConfigService: {}, TermService: {} }));

const { default: SidebarWaiting } = await import("./SidebarWaiting.svelte");
const { store } = await import("$lib/store.svelte");

const box = () => document.querySelector("[data-sidebar-waiting]");

beforeEach(() => {
  cleanup();
  store.alive = true;
  store.sessions = [];
});

describe("SidebarWaiting", () => {
  it("says State unknown when down, never All clear", () => {
    store.alive = false;
    render(SidebarWaiting);
    expect(box()).toHaveTextContent("State unknown");
    expect(box()).not.toHaveTextContent("All clear");
  });
  it("says what is clear and how much runs", () => {
    store.sessions = [{ id: "a", agentState: "working" }, { id: "b", agentState: "idle" }] as never;
    render(SidebarWaiting);
    expect(box()).toHaveTextContent("All clear · 1 running");
  });
  it("marks the moment the last wait clears, and only that moment", async () => {
    store.sessions = [{ id: "a", agentState: "waiting_input" }] as never;
    render(SidebarWaiting);
    store.sessions = [{ id: "a", agentState: "idle" }] as never;
    await tick();
    expect(box()?.querySelector(".text-ink")).not.toBeNull();
    cleanup();
    render(SidebarWaiting);
    await tick();
    expect(box()?.querySelector(".text-ink")).toBeNull();
  });
  it("counts what waits and names the chord", () => {
    store.sessions = [{ id: "a", agentState: "waiting_input" }, { id: "b", agentState: "idle", delivery: "ci_failed" }] as never;
    render(SidebarWaiting);
    expect(screen.getByRole("button")).toHaveTextContent("Waiting 2 · ⌘J");
  });
});
