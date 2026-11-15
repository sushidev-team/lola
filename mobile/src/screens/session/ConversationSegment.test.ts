import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";
import type { SessionInfo } from "$lib/store.svelte";

vi.mock("@bindings/desktop", () => ({
  DaemonService: { ReviewPackages: vi.fn(async () => ({ packages: [] })) },
}));
vi.mock("$lib/components/session/Workspace.svelte", async () => await import("./Stub.test.svelte"));

import ConversationSegment from "./ConversationSegment.svelte";
import { store } from "$lib/store.svelte";
import { terms } from "$lib/terms.svelte";
import { router } from "@mobile/lib/router.svelte";

function s(over: Partial<SessionInfo> = {}): SessionInfo {
  return { id: "s1", project: "p", issue: "NOR-1", title: "T", status: "working", agentState: "working", delivery: "", worktree: "/w/s1", tmuxName: "t", prNumber: 0, ...over } as SessionInfo;
}

describe("ConversationSegment css", () => {
  it("hides only the composer's model chip, never the Model sheet's settings", () => {
    const src = readFileSync(resolve(process.cwd(), "src/screens/session/ConversationSegment.svelte"), "utf8");
    expect(src).not.toMatch(/\[data-phone-desk\] \[data-model-settings\]/);
    expect(src).toContain("[data-phone-desk] .cv-composer [data-model-settings]");
  });
});

describe("terminal hand-off bridge", () => {
  beforeEach(() => {
    store.sessions = [s()];
    router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: "s1", seg: "conversation" }];
    router.sheet = "";
  });
  afterEach(() => vi.clearAllMocks());

  it("moves to the Terminal segment when a desktop Terminal button asks for focus", async () => {
    terms.focusTerminal("s1"); // an old ask: the baseline at mount
    render(ConversationSegment, { id: "s1" });
    await tick();
    expect(router.top).toEqual({ name: "session", id: "s1", seg: "conversation" });
    terms.focusTerminal("s1");
    await waitFor(() => expect(router.top).toEqual({ name: "session", id: "s1", seg: "terminal" }));
  });

  it("stays put when the session offers no Terminal", async () => {
    store.sessions = [s({ tmuxName: "" })];
    render(ConversationSegment, { id: "s1" });
    await tick();
    terms.focusTerminal("s1");
    await tick();
    expect(router.top).toEqual({ name: "session", id: "s1", seg: "conversation" });
  });
});

describe("CI failed card", () => {
  beforeEach(() => {
    router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: "s1", seg: "conversation" }];
    router.sheet = "";
  });

  it("is absent unless the delivery is ci_failed", () => {
    store.sessions = [s()];
    render(ConversationSegment, { id: "s1" });
    expect(document.querySelector("[data-ci-card]")).toBeNull();
  });

  it("shows the title, the failing count and a See checks button that opens Review", async () => {
    store.sessions = [s({ delivery: "ci_failed", board: { checks: [{ name: "a", state: "fail" }, { name: "b", state: "fail" }, { name: "c", state: "pass" }] } } as Partial<SessionInfo>)];
    render(ConversationSegment, { id: "s1" });
    const card = document.querySelector("[data-ci-card]")!;
    expect(card.textContent).toContain("CI failed");
    expect(card.textContent).toContain("2 checks failed");
    await fireEvent.click(screen.getByRole("button", { name: "See checks" }));
    expect(router.top).toEqual({ name: "session", id: "s1", seg: "review" });
  });

  it("shows only the title when no count is known", () => {
    store.sessions = [s({ delivery: "ci_failed" })];
    render(ConversationSegment, { id: "s1" });
    expect(document.querySelector("[data-ci-card]")!.textContent).not.toMatch(/checks failed/);
    expect(document.querySelector("[data-ci-count]")).toBeNull();
  });
});

describe("phone css", () => {
  const src = readFileSync(resolve(process.cwd(), "src/screens/session/ConversationSegment.svelte"), "utf8");
  it("shows only the time after a user message and has no terminal header button rule", () => {
    expect(src).not.toContain("Terminal\"; }");
    expect(src).not.toContain("data-card-terminal");
  });
  it("folds the keyboard-up chrome", () => {
    expect(src).toContain("html[data-kb] [data-phone-desk] [data-phone-footer-text]");
    expect(src).toContain("[data-phone-status]:not(:has(button))");
  });
});
