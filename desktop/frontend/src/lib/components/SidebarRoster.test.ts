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
    expect(document.querySelector('[data-roster-pr]')).toHaveTextContent('#84');
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

  it("opens a PR directly without switching sessions", async () => {
    store.sessions = [row('a', { prNumber: 84, prUrl: 'https://github.com/example/repo/pull/84' }), row('b')];
    nav.openSession('b');
    const openURL = vi.spyOn(store, 'openURL').mockResolvedValue(undefined);
    render(SidebarRoster);
    const chip = screen.getByRole('button', { name: 'Open pull request #84' });
    expect(chip.closest('[data-roster-row]')).toBeNull();
    await fireEvent.click(chip);
    expect(openURL).toHaveBeenCalledWith('https://github.com/example/repo/pull/84');
    expect(nav.focusedTerm).toBe('b');
    openURL.mockRestore();
  });
  it("offers a session menu without opening that session, with a keyboard anchor", async () => {
    const { sessionMenu } = await import('$lib/sessionmenu.svelte');
    store.sessions = [row('a'), row('b')];
    nav.openSession('b');
    render(SidebarRoster);
    const menu = screen.getByRole('button', { name: 'Actions for a' });
    vi.spyOn(menu, 'getBoundingClientRect').mockReturnValue({ right: 300, bottom: 120 } as DOMRect);
    await fireEvent.click(menu, { detail: 0 });
    expect(sessionMenu.request).toEqual({ id: 'a', x: 300, y: 120 });
    expect(nav.focusedTerm).toBe('b');
    sessionMenu.close();
  });
  it("leaves a PR without a URL as a noninteractive label", () => {
    store.sessions = [row('a', { prNumber: 84 })];
    render(SidebarRoster);
    expect(screen.queryByRole('button', { name: 'Open pull request #84' })).toBeNull();
    expect(document.querySelector('[data-roster-pr]')).toHaveTextContent('#84');
  });

  it("avoids repeating an orphaned session's identifier and status", () => {
    store.sessions = [row('orphan', { title: 'NOR-389', issue: 'NOR-389', agentState: 'orphaned' })];
    render(SidebarRoster);
    expect(screen.getAllByText('NOR-389')).toHaveLength(1);
    expect(document.querySelector('[data-roster-meta]')).toBeNull();
    expect(screen.getByRole('img', { name: 'Orphaned' }).querySelector('svg')).not.toBeNull();
    expect(document.querySelector('[data-roster-line]')).toHaveTextContent('Orphaned');
  });

  it("uses a review icon for a resting reviewer without repeating the review label", () => {
    store.sessions = [row('review', { issue: 'CE-79', delivery: 'review_pending' })];
    render(SidebarRoster);
    expect(screen.getByRole('img', { name: 'Awaiting review' })).toBeInTheDocument();
    expect(document.querySelector('[data-roster-meta]')).toHaveTextContent('CE-79');
    expect(document.querySelector('[data-roster-meta]')).not.toHaveTextContent('Awaiting review');
  });
  it("keeps working and needs-you icons when a PR is also awaiting review", () => {
    store.sessions = [row('working', { delivery: 'review_pending', agentState: 'working' }), row('waiting', { delivery: 'review_pending', agentState: 'waiting_input' })];
    render(SidebarRoster);
    expect(screen.getByRole('img', { name: 'Working' })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: 'Needs you' })).toBeInTheDocument();
    expect(screen.queryByRole('img', { name: 'Awaiting review' })).toBeNull();
  });

  it("collapses routine review notifications into a two-line row", () => {
    store.sessions = [row('review', { issue: 'CE-79', delivery: 'review_pending', lastNotification: 'Claude is waiting for your input' })];
    render(SidebarRoster);
    const rowEl = document.querySelector('[data-roster-row="review"]')!;
    expect(rowEl.closest('.sidebar-session-item')).toHaveClass('compact');
    expect(rowEl.querySelector('[data-roster-line]')).toBeNull();
    expect(screen.getByRole('img', { name: 'Awaiting review' })).toBeInTheDocument();
  });

  it("keeps exit details inline with the issue", () => {
    store.sessions = [row('exited', { issue: 'NOR-507', agentState: 'exited' })];
    render(SidebarRoster);
    expect(document.querySelector('[data-roster-meta] [data-roster-line]')).toHaveTextContent('Exited');
    expect(document.querySelector('.sidebar-session-activity')).toBeNull();
  });

  it("marks only the session with Dev active and clears the mark when stopped", async () => {
    store.sessions = [row('dev', { devActive: true }), row('other')];
    render(SidebarRoster);
    expect(screen.getAllByRole('img', { name: 'Dev active' })).toHaveLength(1);
    expect(document.querySelector('[data-roster-row="dev"]')!.closest('.sidebar-session-item')).toHaveClass('dev-active');
    store.sessions = [row('dev', { devActive: false }), row('other')];
    await new Promise(resolve => setTimeout(resolve));
    expect(screen.queryByRole('img', { name: 'Dev active' })).toBeNull();
  });

  it("does not render empty SSH or Temporary sections", () => {
    store.sessions = [row('one')];
    render(SidebarRoster);
    expect(screen.queryByRole('heading', { name: /SSH|Temporary/ })).toBeNull();
  });

  it("folds project groups without changing the active session; search reopens them", async () => {
    store.sessions = [row('first', { title: 'First task' }), row('second', { project: 'web', title: 'Second task' })];
    nav.openSession('first');
    render(SidebarRoster);
    const heading = screen.getByRole('button', { name: /lola 1/i });
    await fireEvent.click(heading);
    expect(heading).toHaveAttribute('aria-expanded', 'false');
    expect(document.querySelector('[data-roster-row="first"]')).not.toBeVisible();
    expect(document.querySelector('[data-roster-row="second"]')).toBeVisible();
    expect(nav.focusedTerm).toBe('first');
    nav.sidebarQuery = 'First';
    await new Promise(resolve => setTimeout(resolve));
    expect(heading).toHaveAttribute('aria-expanded', 'true');
    expect(document.querySelector('[data-roster-row="first"]')).toBeVisible();
  });

});
