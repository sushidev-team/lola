import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";

vi.mock("@bindings/desktop", () => ({
  SSHService: { Hosts: vi.fn().mockResolvedValue({ hosts: [] }), Timeline: vi.fn() },
  DaemonService: {},
  ConfigService: {},
  TermService: {},
}));

const { default: SidebarRoster } = await import("./SidebarRoster.svelte");
const { store } = await import("$lib/store.svelte");
const { nav } = await import("$lib/nav.svelte");
const { ssh } = await import("$lib/ssh.svelte");

const row = (id: string, o: Record<string, unknown> = {}) => ({ id, project: "lola", title: id, agentState: "idle", firstSeen: "2026-01-01T00:00:00Z", ...o }) as never;
const order = () => [...document.querySelectorAll("[data-roster-row]")].map((e) => e.getAttribute("data-roster-row"));

beforeEach(() => {
  cleanup();
  store.alive = true;
  store.projects = [{ name: "lola" }, { name: "web" }] as never;
  nav.focusedTerm = "";
  nav.selectedId = "";
  nav.sidebarQuery = "";
  nav.triage = "";
  nav.scoped = false;
});

describe("SidebarRoster", () => {
  it("order ignores state and recency", async () => {
    store.sessions = [row("needs", { agentState: "waiting_input", firstSeen: "2026-01-03T00:00:00Z" }), row("first"), row("web", { project: "web" }), row("busy", { agentState: "working", firstSeen: "2026-01-02T00:00:00Z" })];
    render(SidebarRoster);
    expect(order()).toEqual(["first", "busy", "needs", "web"]);
    store.sessions = [row("first", { agentState: "waiting_input", agentStateSince: "2026-01-09T00:00:00Z" }), row("needs", { firstSeen: "2026-01-03T00:00:00Z" }), row("web", { project: "web" }), row("busy", { firstSeen: "2026-01-02T00:00:00Z" })];
    await new Promise((r) => setTimeout(r));
    expect(order()).toEqual(["first", "busy", "needs", "web"]);
  });
  it("line 2 prefers lola's ask", () => {
    store.sessions = [row("a", { agentState: "waiting_input", inputReason: "question", delivery: "ci_failed", currentTool: "Bash" })];
    render(SidebarRoster);
    const line = document.querySelector("[data-roster-line]");
    expect(line).toHaveTextContent("Has a question");
    expect(line).toHaveClass("text-orange");
  });
  it("shows a draft mark", () => {
    store.sessions = [row("drafted"), row("clean")];
    ssh.view("drafted").agentDraft = "half a sentence";
    render(SidebarRoster);
    expect(document.querySelector('[data-roster-row="drafted"] [data-draft-mark]')).not.toBeNull();
    expect(document.querySelector('[data-roster-row="clean"] [data-draft-mark]')).toBeNull();
    ssh.view("drafted").agentDraft = "";
  });
  it("a click opens the session, and a second click keeps it open", async () => {
    store.sessions = [row("a")];
    render(SidebarRoster);
    const el = document.querySelector('[data-roster-row="a"]')!;
    await fireEvent.click(el);
    await fireEvent.click(el);
    expect(nav.focusedTerm).toBe("a");
  });
  it("keeps listing the last known sessions while the daemon is down", () => {
    (store as unknown as { retained: unknown[] }).retained = [row("kept")];
    store.sessions = [];
    store.alive = false;
    render(SidebarRoster);
    expect(screen.getByText("kept")).toBeInTheDocument();
    (store as unknown as { retained: unknown[] }).retained = [];
  });
  it("shows issue, delivery and current activity separately", () => {
    store.sessions = [row("a", { issue: "LOLA-42", prNumber: 84, delivery: "review_pending", headline: "Finished sidebar layout" })];
    render(SidebarRoster);
    expect(document.querySelector('[data-roster-meta]')).toHaveTextContent('LOLA-42');
    expect(document.querySelector('[data-roster-meta]')).toHaveTextContent('#84');
    expect(document.querySelector('[data-roster-line]')).toHaveTextContent('Finished sidebar layout');
  });
  it("filters by multiple search words without changing the active session", async () => {
    store.sessions = [row("a", { issue: "LOLA-42", prNumber: 84 }), row("b")];
    nav.openSession('b');
    nav.sidebarQuery = 'LOLA-42 #84';
    render(SidebarRoster);
    expect(order()).toEqual(['a']);
    expect(nav.focusedTerm).toBe('b');
  });
  it("Needs you includes idle failed checks but excludes agents already fixing them", () => {
    store.sessions = [row('idle', { delivery: 'ci_failed' }), row('fixing', { delivery: 'ci_failed', agentState: 'working' }), row('review', { delivery: 'review_pending' })];
    nav.triage = 'Needs You';
    render(SidebarRoster);
    expect(order()).toEqual(['idle']);
  });

});
