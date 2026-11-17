// The one line that says the terminal is still there. Once, across the whole app.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";

vi.mock("@bindings/desktop", () => ({
  SSHService: {},
  TalkService: {},
  DaemonService: { Alive: vi.fn().mockResolvedValue(true) },
  ConfigService: {},
  TermService: { Shells: vi.fn(async () => []) },
}));

const { default: FirstOpen } = await import("./FirstOpen.svelte");
const { terms, AGENT } = await import("$lib/terms.svelte");
const { store } = await import("$lib/store.svelte");

// What a relaunch forgets, which is everything that is not in storage.
const relaunch = () => Object.assign(terms as unknown as { introSession: string; introDismissed: boolean }, { introSession: "", introDismissed: false });

beforeEach(() => {
  cleanup();
  localStorage.clear();
  relaunch();
  store.sessions = [{ id: "a", agent: "claude", agentState: "idle", tmuxName: "a" }, { id: "b", agent: "claude", agentState: "idle", tmuxName: "b" }] as never;
});

describe("the first-open line", () => {
  it("shows once, globally, in the first conversation, and survives a relaunch as seen", () => {
    const r = render(FirstOpen, { sessionId: "a" });
    expect(screen.getByText("Your agent still runs in its terminal, unchanged.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Terminal/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Always open there" })).toBeInTheDocument();
    r.unmount();
    render(FirstOpen, { sessionId: "b" });
    expect(document.querySelector("[data-first-open]")).toBeNull();
    cleanup();
    relaunch();
    render(FirstOpen, { sessionId: "a" });
    expect(document.querySelector("[data-first-open]")).toBeNull();
  });

  it("waits while a terminal covers the conversation", async () => {
    const r = render(FirstOpen, { sessionId: "a", covered: true });
    expect(document.querySelector("[data-first-open]")).toBeNull();
    expect(localStorage.getItem("lola.session.intro")).toBeNull();
    await r.rerender({ sessionId: "a", covered: false });
    expect(document.querySelector("[data-first-open]")).not.toBeNull();
  });

  it("Always open there sets the preference and goes to the terminal", async () => {
    render(FirstOpen, { sessionId: "a" });
    await fireEvent.click(screen.getByRole("button", { name: "Always open there" }));
    expect(terms.globalView).toBe("terminal");
    expect(localStorage.getItem("lola.session.view")).toBe("terminal");
    expect(terms.activeTab("a")).toBe(AGENT);
    expect(document.querySelector("[data-first-open]")).toBeNull();
    terms.setGlobalView("conversation");
  });

  it("an answered line stays gone when the same conversation remounts", async () => {
    const r = render(FirstOpen, { sessionId: "a" });
    await fireEvent.click(screen.getByRole("button", { name: "Always open there" }));
    r.unmount();
    terms.setGlobalView("conversation");
    render(FirstOpen, { sessionId: "a" });
    expect(document.querySelector("[data-first-open]")).toBeNull();
  });
});
