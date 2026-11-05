import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
// @ts-expect-error jsdom ships no types, and this one test file only needs the constructor.
import { JSDOM, VirtualConsole } from "jsdom";
import { appearance } from "$lib/theme-runtime.svelte";
import { terms, AGENT } from "$lib/terms.svelte";
import { WIDGET_BOOT_GRACE_MS, WIDGET_CSP, WIDGET_ERROR_CAP, WIDGET_PING_MISSES, WIDGET_PING_MS, WIDGET_READY_MS, WIDGET_SANDBOX, inlineSpans, itemStatus, richBlocks, sshTabs, widgetDoc, widgetFault, widgetText } from "$lib/ssh";
import { confirm } from "$lib/confirm.svelte";
import { tick } from "svelte";
import { render, screen, cleanup, fireEvent, waitFor, within } from "@testing-library/svelte";

const svc = vi.hoisted(() => ({
  Timeline: vi.fn(),
  PromptAnswer: vi.fn(),
  Output: vi.fn(),
  Approve: vi.fn(),
  Go: vi.fn(),
  Deny: vi.fn(),
  TakeWheel: vi.fn(),
  Handback: vi.fn(),
  HandbackPreview: vi.fn(),
  Say: vi.fn(),
  RunCommand: vi.fn(),
  TerminalCd: vi.fn(),
  ShareBlock: vi.fn(),
  Stop: vi.fn(),
  Recall: vi.fn(),
  Proposal: vi.fn(),
  Answer: vi.fn(),
  Hosts: vi.fn(),
  Open: vi.fn(),
  Resolve: vi.fn(),
  Terminal: vi.fn(),
  Close: vi.fn(),
  Trash: vi.fn(),
  RevokeGrant: vi.fn(),
  WidgetStatus: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  SSHService: svc,
  DaemonService: { Alive: vi.fn().mockResolvedValue(false) },
  ConfigService: {
    SSHConfigAliases: vi.fn().mockResolvedValue([]),
    ResolveSSHHost: vi.fn().mockResolvedValue({ hostname: "web-1", user: "deploy", port: "22" }),
    SSHHostForms: vi.fn().mockResolvedValue([]),
    SSHHostPreview: vi.fn().mockResolvedValue({ directLine: "" }),
    SaveSSHHost: vi.fn().mockResolvedValue(undefined),
    RemoveSSHHost: vi.fn().mockResolvedValue(undefined),
  },
  TermService: { Shells: vi.fn().mockResolvedValue([]) },
}));
vi.mock("../LiveTerminal.svelte", async () => ({ default: (await import("./TerminalStub.test.svelte")).default }));

const { ssh } = await import("$lib/ssh.svelte");
const { store } = await import("$lib/store.svelte");
const { default: SSHBlock } = await import("./SSHBlock.svelte");
const { default: SSHTerminalPane } = await import("./SSHTerminalPane.svelte");
const { default: SSHElement } = await import("./SSHElement.svelte");
const { default: SSHWidget } = await import("./SSHWidget.svelte");
const { default: SSHEvidence } = await import("./SSHEvidence.svelte");
const { default: SSHApproval } = await import("./SSHApproval.svelte");
const { default: SSHGoCard } = await import("./SSHGoCard.svelte");
const { default: SSHConnCard } = await import("./SSHConnCard.svelte");
const { default: SSHEnded } = await import("./SSHEnded.svelte");
const { default: SSHHandback } = await import("./SSHHandback.svelte");
const { default: SSHStream } = await import("./SSHStream.svelte");
const { default: SSHTierTag } = await import("./SSHTierTag.svelte");
const { default: SSHDock } = await import("./SSHDock.svelte");
const { default: SSHHeader } = await import("./SSHHeader.svelte");
const { default: SSHComposer } = await import("./SSHComposer.svelte");
const { default: SSHWorkspace } = await import("./SSHWorkspace.svelte");
const { default: SSHChip } = await import("./SSHChip.svelte");
const { default: SSHHostSettings } = await import("./SSHHostSettings.svelte");
const { default: NewSSHSession } = await import("./NewSSHSession.svelte");
const { ConfigService } = await import("@bindings/desktop");

let n = 0;
let SID = "";

const conn = (o: Record<string, unknown> = {}) => ({
  id: "c1",
  session: SID,
  host: "prod-api",
  alias: "prod-api",
  tier: "production",
  entry: "direct",
  state: "connected",
  wheel: "agent",
  tab: "s-ssh-1",
  tabAlive: true,
  ...o,
});
const block = (o: Record<string, unknown> = {}) => ({
  id: "b1",
  conn: "c1",
  session: SID,
  host: "prod-api",
  tier: "production",
  command: "systemctl status api",
  why: "see whether it restarted",
  actor: "agent",
  status: "succeeded",
  approval: { granted: true },
  exitCode: 0,
  durationMs: 400,
  ...o,
});
const el = (o: Record<string, unknown> = {}) => ({ id: "e1", kind: "narration", title: "hello", revision: 1, ...o });

function useSession(s: Record<string, unknown> = {}) {
  store.sessions = [{ id: SID, title: "Debug", agent: "claude", agentState: "working", ...s }] as never;
}

function seed(items: unknown[], conns: unknown[] = [conn()]) {
  const v = ssh.view(SID);
  v.items = items as never;
  v.connections = conns as never;
  v.loaded = true;
  return v;
}

beforeEach(() => {
  cleanup();
  n++;
  SID = `s${n}`;
  for (const f of Object.values(svc)) f.mockReset();
  store.alive = true;
  svc.Timeline.mockResolvedValue({ session: SID, connections: null, items: [], lastSeq: 0 });
  svc.Output.mockResolvedValue({ data: "", offset: 0, nextOffset: 0, totalBytes: 0, eof: true });
  svc.Approve.mockResolvedValue({});
  svc.Say.mockResolvedValue({ item: "h1", delivered: false });
  svc.TakeWheel.mockResolvedValue({ conn: conn({ wheel: "human" }) });
  svc.Handback.mockResolvedValue({ conn: conn(), delivered: false });
  svc.HandbackPreview.mockResolvedValue({ transcript: "deploy@prod:~$ df -h" });
  svc.Proposal.mockResolvedValue({ element: {} });
  svc.Hosts.mockResolvedValue({ hosts: [] });
  useSession();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("no markup from agent or remote text", () => {
  it("nothing in the workspace uses {@html}", () => {
    const files = import.meta.glob("./*.svelte", { query: "?raw", import: "default", eager: true }) as Record<string, string>;
    expect(Object.keys(files).length).toBeGreaterThan(15);
    for (const [f, raw] of Object.entries(files)) {
      expect(raw.replace(/<!--[\s\S]*?-->/g, "").replace(/\/\/.*$/gm, ""), f).not.toContain("{@html");
    }
  });
});

describe("tier tag", () => {
  it("reads PRODUCTION for an unknown tier", () => {
    render(SSHTierTag, { tier: "mystery" });
    expect(screen.getByText("PRODUCTION")).toBeInTheDocument();
  });
});

describe("a block", () => {
  it("titles the block with the why, and keeps it the agent's voice", () => {
    seed([]);
    const { container } = render(SSHBlock, { sessionId: SID, block: block() as never });
    const why = container.querySelector("[data-plate] [data-claim='why']");
    expect(why?.textContent).toContain("see whether it restarted");
    expect(why?.className).toContain("font-sans");
    const cmd = container.querySelector("[data-plate] [data-command-line]");
    expect(cmd?.textContent).toContain("systemctl status api");
    expect(why?.compareDocumentPosition(cmd as Node)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });
  it("leads with the command when there is no why", () => {
    seed([]);
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ why: "" }) as never });
    expect(container.querySelector("[data-claim='why']")).toBeNull();
    expect(container.querySelector("[data-plate]")?.textContent).toContain("systemctl status api");
  });
  it("loses why in the audit view", () => {
    seed([]);
    render(SSHBlock, { sessionId: SID, block: block() as never, filter: "facts" });
    expect(screen.queryByText(/see whether it restarted/)).toBeNull();
  });
  it("says exit 3 and never failed", () => {
    seed([]);
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ status: "failed", exitCode: 3, preview: "Active: failed" }) as never });
    expect(screen.getByText(/exit 3/)).toBeInTheDocument();
    const words = container.querySelector("[data-plate] .num")?.textContent ?? "";
    expect(words.toLowerCase()).not.toContain("failed");
  });
  it("shows an unknown status in words, with no tick", () => {
    seed([]);
    render(SSHBlock, { sessionId: SID, block: block({ status: "new_state", exitCode: undefined }) as never });
    expect(screen.getByText("new_state")).toBeInTheDocument();
  });
  it("expands in place, fetches output lazily and offers both digs", async () => {
    svc.Output.mockResolvedValue({ data: "disk is full\n", offset: 0, nextOffset: 13, totalBytes: 13, eof: true });
    seed([]);
    render(SSHBlock, { sessionId: SID, block: block({ stdoutBytes: 13 }) as never });
    expect(svc.Output).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole("button", { expanded: false }));
    await screen.findByText(/disk is full/);
    expect(svc.Output).toHaveBeenCalledWith(expect.objectContaining({ session: SID, block: "b1", stream: "stdout" }));
    await fireEvent.click(screen.getByRole("button", { name: "Ask Claude" }));
    expect(ssh.view(SID).draft).toBe("About b1: ");
  });
  it("renders output as text, and links only http(s) through the daemon", async () => {
    const open = vi.spyOn(store, "openURL").mockResolvedValue();
    svc.Output.mockResolvedValue({ data: "<img src=x onerror=1> https://a.example javascript:x\n", offset: 0, nextOffset: 50, totalBytes: 50, eof: true });
    const v = seed([]);
    v.expanded.add("b1");
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ stdoutBytes: 50 }) as never });
    const link = await screen.findByRole("link");
    expect(container.querySelector("img")).toBeNull();
    expect(link.textContent).toBe("https://a.example");
    await fireEvent.click(link);
    expect(open).toHaveBeenCalledWith("https://a.example");
    expect(screen.getAllByRole("link")).toHaveLength(1);
  });
  it("marks the dropped middle, and keeps the tail", async () => {
    svc.Output.mockResolvedValue({ data: "head\n", offset: 0, nextOffset: 5, totalBytes: 5, eof: true, droppedBytes: 3 * 1024 * 1024, tail: "the end\n" });
    const v = seed([]);
    v.expanded.add("b1");
    render(SSHBlock, { sessionId: SID, block: block({ stdoutBytes: 5 }) as never });
    await screen.findByText(/not kept/);
    expect(screen.getByText(/the end/)).toBeInTheDocument();
    expect(screen.getByText(/The middle is gone/)).toBeInTheDocument();
  });
  it("offers a retry when a fetch fails", async () => {
    svc.Output.mockRejectedValue(new Error("nope"));
    const v = seed([]);
    v.expanded.add("b1");
    render(SSHBlock, { sessionId: SID, block: block({ stdoutBytes: 5 }) as never });
    await screen.findByText("Could not load this output.");
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });
  it("stops with no question, and names what may survive", async () => {
    svc.Stop.mockResolvedValue({ block: block() });
    const v = seed([]);
    v.expanded.add("b1");
    render(SSHBlock, { sessionId: SID, block: block({ status: "running", exitCode: undefined }) as never });
    expect(screen.getByRole("button", { name: "Stop" }).getAttribute("title")).toMatch(/The remote process may keep running/);
    await fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(svc.Stop).toHaveBeenCalledWith("c1", "b1");
  });
});

describe("elements", () => {
  it("never draws a plate, for any kind", () => {
    seed([]);
    for (const kind of ["summary", "narration", "hypothesis", "finding", "plan", "metric", "evidence", "recommendation", "proposal", "question", "future_kind"]) {
      const { container, unmount } = render(SSHElement, { sessionId: SID, element: el({ kind, command: "x", steps: [], metrics: [] }) as never });
      expect(container.querySelector("[data-plate]"), kind).toBeNull();
      unmount();
    }
  });
  it("words a confirmed theory as the agent's, with no green", () => {
    seed([]);
    const { container } = render(SSHElement, { sessionId: SID, element: el({ kind: "hypothesis", state: "confirmed", title: "Disk full" }) as never });
    expect(screen.getByText("Agent says confirmed")).toBeInTheDocument();
    expect(container.innerHTML).not.toContain("text-good");
    expect(container.innerHTML).not.toContain("bg-good");
  });
  it("renders an unknown kind as plain text", () => {
    seed([]);
    render(SSHElement, { sessionId: SID, element: el({ kind: "from_the_future", title: "still readable", body: "and this" }) as never });
    expect(screen.getByText("still readable")).toBeInTheDocument();
    expect(screen.getByText("and this")).toBeInTheDocument();
  });
  it("renders text as text, never markup", () => {
    seed([]);
    const { container } = render(SSHElement, { sessionId: SID, element: el({ title: "<script>x</script><b>bold</b>" }) as never });
    expect(container.querySelector("b")).toBeNull();
    expect(container.textContent).toContain("<b>bold</b>");
  });
  it("turns a bare id into a plate only for a real block", () => {
    seed([{ id: "i", kind: "block", order: 1, seq: 1, block: block({ id: "b12" }) }]);
    render(SSHElement, { sessionId: SID, element: el({ title: "see b12 and b99" }) as never });
    expect(screen.getByRole("button", { name: /b12/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /b99/ })).toBeNull();
  });
  it("folds a finding's body behind More", async () => {
    seed([]);
    render(SSHElement, { sessionId: SID, element: el({ kind: "finding", severity: "high", title: "t", body: "the detail" }) as never });
    expect(screen.queryByText("the detail")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: /More/ }));
    expect(screen.getByText("the detail")).toBeInTheDocument();
  });

  const proposal = (o: Record<string, unknown> = {}) =>
    el({ kind: "proposal", title: "Rotate logs", command: "logrotate -f /etc/x", state: "proposed", revision: 3, ...o });

  it("a proposal runs only through the check, with the drawn revision", async () => {
    vi.useFakeTimers();
    seed([]);
    render(SSHElement, { sessionId: SID, element: proposal() as never });
    expect(svc.Proposal).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole("button", { name: "Run on prod-api…" }));
    expect(svc.Proposal).not.toHaveBeenCalled();
    const run = screen.getByRole("button", { name: "Run on prod-api" });
    expect(run).toBeDisabled();
    await vi.advanceTimersByTimeAsync(800);
    expect(run).toBeEnabled();
    expect(screen.getByText("Run this yourself?")).toBeInTheDocument();
    await fireEvent.click(run);
    expect(svc.Proposal).toHaveBeenCalledWith(expect.objectContaining({ element: "e1", revision: 3, action: "run" }));
  });
  it("a moved revision closes the check and says so", async () => {
    seed([]);
    const r = render(SSHElement, { sessionId: SID, element: proposal() as never });
    await fireEvent.click(screen.getByRole("button", { name: "Run on prod-api…" }));
    await r.rerender({ sessionId: SID, element: proposal({ revision: 4, command: "rm -rf /" }) as never });
    await waitFor(() => expect(screen.queryByRole("button", { name: "Run on prod-api" })).toBeNull());
    expect(screen.getByRole("alert")).toHaveTextContent("Claude changed this proposal. Read it again.");
    expect(svc.Proposal).not.toHaveBeenCalled();
  });
  it("a server refusal for the same reason says the same sentence", async () => {
    vi.useFakeTimers();
    svc.Proposal.mockRejectedValue(new Error("the proposal has changed since you read it, so nothing ran"));
    seed([]);
    render(SSHElement, { sessionId: SID, element: proposal() as never });
    await fireEvent.click(screen.getByRole("button", { name: "Run on prod-api…" }));
    await vi.advanceTimersByTimeAsync(800);
    await fireEvent.click(screen.getByRole("button", { name: "Run on prod-api" }));
    await vi.advanceTimersByTimeAsync(0);
    expect(screen.getByRole("alert")).toHaveTextContent("Claude changed this proposal. Read it again.");
  });
  it("any other refusal is the daemon's sentence, not 'it changed'", async () => {
    vi.useFakeTimers();
    svc.Proposal.mockRejectedValue(new Error("the connection to prod-api is disconnected, so nothing ran"));
    const v = seed([]);
    render(SSHElement, { sessionId: SID, element: proposal() as never });
    await fireEvent.click(screen.getByRole("button", { name: "Run on prod-api…" }));
    await vi.advanceTimersByTimeAsync(800);
    await fireEvent.click(screen.getByRole("button", { name: "Run on prod-api" }));
    await vi.advanceTimersByTimeAsync(0);
    expect(screen.queryByRole("alert")).toBeNull();
    expect(v.flash).toContain("is disconnected, so nothing ran");
  });
  it("an answered question shows your words in your voice", () => {
    seed([]);
    render(SSHElement, { sessionId: SID, element: el({ kind: "question", title: "Go on?", state: "answered", outcome: { text: "Keep going" } }) as never });
    expect(screen.getByText("Keep going")).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).toBeNull();
  });
  it("answers with the revision on screen", async () => {
    svc.Answer.mockResolvedValue({ item: "h", delivered: false });
    seed([]);
    render(SSHElement, { sessionId: SID, element: el({ kind: "question", title: "Go on?", state: "open", choices: ["Yes"], revision: 2 }) as never });
    await fireEvent.click(screen.getByRole("button", { name: "Yes" }));
    expect(svc.Answer).toHaveBeenCalledWith({ session: SID, element: "e1", revision: 2, text: "Yes" });
  });
});

describe("evidence plates", () => {
  it("never says verified", () => {
    seed([{ id: "i", kind: "block", order: 1, seq: 1, block: block() }]);
    const { container } = render(SSHEvidence, { sessionId: SID, block: "b1", verified: true });
    expect(container.textContent?.toLowerCase()).not.toContain("verified");
    expect(screen.getByRole("button").getAttribute("title")).toContain("That does not mean Claude's reading of it is correct");
  });
  it("reads not on record when the daemon could not find it, and is no link", () => {
    seed([]);
    render(SSHEvidence, { sessionId: SID, block: "b9", verified: false });
    expect(screen.getByText("b9 not on record")).toBeInTheDocument();
    expect(screen.queryByRole("button")).toBeNull();
  });
  it("digs: expands the block and asks the stream to land on it", async () => {
    const v = seed([{ id: "i", kind: "block", order: 1, seq: 1, block: block() }]);
    render(SSHEvidence, { sessionId: SID, block: "b1", verified: true });
    await fireEvent.click(screen.getByRole("button"));
    expect(v.expanded.has("b1")).toBe(true);
    expect(v.jump?.block).toBe("b1");
  });
});

describe("the approval card", () => {
  it("does not re-arm when a fetch replaces the same block and connection", async () => {
    const pend = () => ({ id: "i", kind: "block", order: 1, seq: 1, block: block({ id: "b9", status: "pending_approval", exitCode: undefined, command: "docker ps", approval: { mode: "per_command" } }) });
    const c = conn({ tier: "staging", state: "connected" });
    const v = seed([pend()] as never, [c]);
    render(SSHDock, { sessionId: SID });
    const run = () => screen.getByRole("button", { name: /Run once/ });
    await waitFor(() => expect(run()).toBeEnabled(), { timeout: 2000 });
    for (let i = 0; i < 3; i++) {
      v.items = [pend()] as never;
      v.connections = [{ ...c }] as never;
      await tick();
      await new Promise((r) => setTimeout(r, 20));
      expect(run()).toBeEnabled();
    }
  });
  it("keeps the small print behind Details", async () => {
    seed([]);
    render(SSHApproval, props());
    const warning = screen.getByText("Everything the agent reads here is sent to its model provider.");
    expect(warning.closest("[hidden]")).not.toBeNull();
  });
  const props = (o: Record<string, unknown> = {}, c: Record<string, unknown> = {}) => ({
    sessionId: SID,
    conn: conn(c) as never,
    block: block({ status: "pending_approval", command: "systemctl restart api", approval: { granted: false }, ...o }) as never,
  });

  it("disables confirm until armed, and re-arms on a new command", async () => {
    vi.useFakeTimers();
    seed([]);
    const r = render(SSHApproval, props());
    const run = () => screen.getByRole("button", { name: "Run once on prod-api" });
    expect(run()).toBeDisabled();
    await vi.advanceTimersByTimeAsync(799);
    expect(run()).toBeDisabled();
    await vi.advanceTimersByTimeAsync(1);
    expect(run()).toBeEnabled();
    await r.rerender(props({ command: "rm -rf /srv/api" }));
    await vi.advanceTimersByTimeAsync(0);
    expect(run()).toBeDisabled();
    await vi.advanceTimersByTimeAsync(800);
    expect(run()).toBeEnabled();
  });
  it("shows the whole command, the warnings and the why outside the facts", () => {
    seed([]);
    const long = `tar -czf x.tgz ${"a".repeat(400)}`;
    const { container } = render(SSHApproval, props({ command: long }));
    expect(container.querySelector("[data-command]")?.textContent).toBe(long);
    expect(container.querySelector("dl")?.textContent).not.toContain("see whether it restarted");
    expect(container.querySelector("[data-claim='why']")?.textContent).toContain("see whether it restarted");
    expect(screen.getByText("Everything the agent reads here is sent to its model provider.")).toBeInTheDocument();
    expect(screen.getByText("Lola does not filter commands. The remote user's permissions are the real limit.")).toBeInTheDocument();
    expect(container.querySelector("[data-unsandboxed]")).not.toBeNull();
    expect(screen.getByText("PRODUCTION")).toBeInTheDocument();
    expect(screen.getByText("login directory")).toBeInTheDocument();
  });
  it("drops the unsandboxed note for a sandboxed agent", () => {
    seed([]);
    render(SSHApproval, props({}, { sandboxed: true }));
    expect(document.querySelector("[data-unsandboxed]")).toBeNull();
  });
  it("has no grant control on production, whatever GrantMaxMinutes says", () => {
    seed([]);
    render(SSHApproval, props({}, { grantMaxMinutes: 120 }));
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(screen.queryByText(/Allow everything/)).toBeNull();
    expect(screen.getByText(/Production asks for every command\./)).toBeInTheDocument();
  });
  it("offers a grant below production, off by default, and renames the button", async () => {
    vi.useFakeTimers();
    seed([]);
    render(SSHApproval, props({}, { tier: "staging", grantMaxMinutes: 30, label: "stage-api", host: "stage-api" }));
    const box = screen.getByRole("checkbox");
    expect(box).not.toBeChecked();
    await fireEvent.click(box);
    await vi.advanceTimersByTimeAsync(800);
    const run = screen.getByRole("button", { name: "Run and allow for 15 min" });
    await fireEvent.click(run);
    expect(svc.Approve).toHaveBeenCalledWith(expect.objectContaining({ decision: "approve", grantMinutes: 15 }));
  });
  it("approves exactly once when armed", async () => {
    vi.useFakeTimers();
    seed([]);
    render(SSHApproval, props());
    await vi.advanceTimersByTimeAsync(800);
    await fireEvent.click(screen.getByRole("button", { name: "Run once on prod-api" }));
    expect(svc.Approve).toHaveBeenCalledWith({ conn: "c1", block: "b1", decision: "approve", grantMinutes: undefined });
  });
  it("denies with a reason, in place", async () => {
    seed([]);
    render(SSHApproval, props());
    await fireEvent.click(screen.getByRole("button", { name: "Deny…" }));
    const field = screen.getByPlaceholderText("Tell Claude why (optional)");
    await fireEvent.input(field, { target: { value: "not now" } });
    await fireEvent.keyDown(field, { key: "Enter" });
    expect(svc.Approve).toHaveBeenCalledWith({ conn: "c1", block: "b1", decision: "deny", reason: "not now" });
  });
  it("Enter on a card does nothing, and Y only works once armed", async () => {
    vi.useFakeTimers();
    seed([]);
    const { container } = render(SSHApproval, props());
    const card = container.querySelector("[data-card]") as HTMLElement;
    await fireEvent.keyDown(card, { key: "Y" });
    expect(svc.Approve).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(800);
    await fireEvent.keyDown(card, { key: "Enter" });
    await fireEvent.keyDown(card, { key: "y" });
    expect(svc.Approve).not.toHaveBeenCalled();
    await fireEvent.keyDown(card, { key: "Y" });
    expect(svc.Approve).toHaveBeenCalledTimes(1);
  });
  it("says so when the daemon refused it in the same moment", async () => {
    vi.useFakeTimers();
    svc.Approve.mockRejectedValue("block b1 is refused, it is not waiting for approval");
    const v = seed([]);
    render(SSHApproval, props());
    await vi.advanceTimersByTimeAsync(800);
    await fireEvent.click(screen.getByRole("button", { name: "Run once on prod-api" }));
    await vi.advanceTimersByTimeAsync(0);
    expect(v.flash).toBe("That command was already refused: block b1 is refused, it is not waiting for approval");
  });
});

describe("approve outcomes", () => {
  it("a grant the host will not give is not called a refusal", async () => {
    svc.Approve.mockRejectedValue("prod-api approves every command, so it cannot have a grant");
    const v = seed([]);
    await ssh.approve(SID, block({ status: "pending_approval" }) as never, 5);
    expect(v.flash).toBe("prod-api approves every command, so it cannot have a grant");
  });
  it("a block refused by the approve itself says why", async () => {
    svc.Approve.mockResolvedValue(block({ status: "refused", statusDetail: "production hosts refuse unsandboxed agents" }));
    const v = seed([]);
    await ssh.approve(SID, block({ status: "pending_approval" }) as never);
    expect(v.flash).toBe("That command was already refused: production hosts refuse unsandboxed agents");
  });
  it("a stop that started a process says the remote may outlive it", async () => {
    svc.Stop.mockResolvedValue({ block: block(), warning: "Stopping ends the local ssh. The remote process may keep running." });
    const v = seed([]);
    await ssh.stop(SID, block() as never);
    expect(v.flash).toContain("may keep running");
  });
});

describe("the daemon is down", () => {
  it("keeps a session with ssh readable and sends nothing", async () => {
    store.alive = true;
    store.sessions = [{ id: SID, title: "t", ssh: { connections: [], lastSeq: 0 } } as never, { id: `${SID}-plain`, title: "p" } as never];
    const v = seed([]);
    store.sessions = store.sessions.filter((s) => s.id !== `${SID}-plain`);
    (store as unknown as { retainedSSH: unknown[] }).retainedSSH = store.sessions.filter((s) => s.ssh);
    store.sessions = [];
    store.alive = false;
    expect(store.sessionById(SID)?.title).toBe("t");
    expect(store.sessionById(`${SID}-plain`)).toBeUndefined();
    await ssh.say(SID, "are you there");
    expect(svc.Say).not.toHaveBeenCalled();
    expect(v.flash).toContain("daemon is down");
    store.alive = true;
    expect(store.sessionById(SID)).toBeUndefined();
  });
});

describe("the go card", () => {
  it("disables go until armed, and marks both claims", async () => {
    vi.useFakeTimers();
    seed([]);
    const { container } = render(SSHGoCard, { sessionId: SID, conn: conn({ state: "requested", why: "logs", scope: "read" }) as never });
    const go = screen.getByRole("button", { name: "Open terminal and connect" });
    expect(go).toBeDisabled();
    expect(container.querySelector("[data-claim='why']")?.textContent).toContain("Claude says why: logs");
    expect(container.querySelector("[data-claim='scope']")?.textContent).toContain("Claude says it will: read");
    expect(screen.getByText("Everything the agent reads here is sent to its model provider.")).toBeInTheDocument();
    expect(svc.Go).not.toHaveBeenCalled();
    svc.Go.mockResolvedValue(conn());
    await vi.advanceTimersByTimeAsync(800);
    await fireEvent.click(go);
    expect(svc.Go).toHaveBeenCalledWith("c1");
  });
  it("words a grantable host's second sentence", () => {
    seed([]);
    render(SSHGoCard, { sessionId: SID, conn: conn({ state: "requested", tier: "dev", grantMaxMinutes: 120 }) as never });
    expect(screen.getByText("Each command will ask, unless you open a grant.")).toBeInTheDocument();
  });
});

describe("the dock", () => {
  const pending = { id: "i", kind: "block", order: 1, seq: 1, block: block({ status: "pending_approval", approval: { granted: false } }) };
  it("is an alert that never takes focus by appearing", async () => {
    seed([pending]);
    const before = document.activeElement;
    render(SSHDock, { sessionId: SID });
    expect(screen.getByRole("alert")).toBeInTheDocument();
    await Promise.resolve();
    expect(document.activeElement).toBe(before);
  });
  it("takes focus only when asked", async () => {
    const v = seed([pending]);
    render(SSHDock, { sessionId: SID });
    v.dockFocus++;
    await waitFor(() => expect(document.activeElement).toBe(document.querySelector("[data-card]")));
  });
  it("leaves a sign-in with no recognized question to the stream, where its card will appear", () => {
    seed([], [conn({ state: "authenticating" })]);
    const r = render(SSHDock, { sessionId: SID });
    expect(document.querySelector('[data-card="auth"]')).toBeNull();
    r.unmount();
    render(SSHStream, { sessionId: SID });
    expect(document.querySelector('[data-prompts] [data-card="auth"]')).not.toBeNull();
    expect(screen.getByText("Waiting for input?")).toBeInTheDocument();
  });
  it("words a network drop and an idle drop differently", () => {
    seed([], [conn({ state: "disconnected" })]);
    const r = render(SSHDock, { sessionId: SID });
    expect(screen.getByText(/Connection to prod-api dropped\./)).toBeInTheDocument();
    r.unmount();
    seed([], [conn({ state: "disconnected", stateDetail: "idle timeout", idleLingerSeconds: 900 })]);
    render(SSHDock, { sessionId: SID });
    expect(screen.getByText(/closed after 15 minutes with nothing using it/)).toBeInTheDocument();
  });
  it("offers a retry after a failure", () => {
    seed([], [conn({ state: "failed" })]);
    render(SSHDock, { sessionId: SID });
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
    expect(screen.getByText(/Could not connect to prod-api/)).toBeInTheDocument();
  });
});

describe("control", () => {
  const live = () => conn({ tier: "dev", state: "connected" });
  it("has no wheel switch: Stop is the action while the agent works", async () => {
    useSession({ agentState: "working" });
    const v = seed([], [live()]);
    render(SSHHeader, { sessionId: SID, conn: live() as never });
    expect(screen.queryByRole("radiogroup", { name: "Who may run commands" })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: /Stop Claude/ }));
    expect(svc.TakeWheel).toHaveBeenCalledWith({ conn: "c1", stopInFlight: true, interrupt: true });
    expect(v.composerFocus).toBeGreaterThan(0);
    expect(v.connections[0].wheel).toBe("human");
  });
  it("offers no Stop while the agent is at rest, and says who is driving", () => {
    useSession({ agentState: "idle" });
    seed([], [live()]);
    render(SSHHeader, { sessionId: SID, conn: live() as never });
    expect(screen.queryByRole("button", { name: /Stop Claude/ })).toBeNull();
    cleanup();
    render(SSHHeader, { sessionId: SID, conn: conn({ tier: "dev", state: "connected", wheel: "human" }) as never });
    expect(screen.getByText("You're driving")).toBeInTheDocument();
  });
  it("puts who you are and how the agent runs in the menu, off the bar", async () => {
    useSession({ agentState: "idle" });
    seed([], [live()]);
    render(SSHHeader, { sessionId: SID, conn: live() as never });
    expect(screen.queryByText(/Unsandboxed/i)).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "More connection actions" }));
    expect(document.querySelector("[data-conn-facts]")?.textContent).toContain("Agent runs");
  });
  it("hands back when you ask the agent something", async () => {
    const v = seed([], [conn({ wheel: "human" })]);
    svc.Say.mockResolvedValue({ item: "x" });
    await ssh.say(SID, "carry on");
    expect(v.connections[0].wheel).toBe("agent");
  });
});

describe("handing back", () => {
  async function open() {
    seed([], [conn({ wheel: "human" })]);
    await ssh.openHandback(SID, "c1");
    return render(SSHHandback, { sessionId: SID });
  }
  it("shows the transcript by default, and offers two equal actions", async () => {
    await open();
    expect(await screen.findByDisplayValue("deploy@prod:~$ df -h")).toBeInTheDocument();
    const without = screen.getByRole("button", { name: "Hand back without it" });
    const withIt = screen.getByRole("button", { name: "Hand back with this text" });
    expect(without.className).toBe(withIt.className);
    expect(screen.getByText("This text goes to the agent and its model provider. Remove anything you would not paste there.")).toBeInTheDocument();
  });
  it("sends a transcript only from the with button, exactly as edited", async () => {
    await open();
    const area = await screen.findByDisplayValue("deploy@prod:~$ df -h");
    await fireEvent.input(area, { target: { value: "edited" } });
    await fireEvent.click(screen.getByRole("button", { name: "Hand back with this text" }));
    expect(svc.Handback).toHaveBeenCalledWith({ conn: "c1", note: undefined, transcript: "edited" });
  });
  it("sends none from the without button", async () => {
    await open();
    await fireEvent.click(await screen.findByRole("button", { name: "Hand back without it" }));
    expect(svc.Handback).toHaveBeenCalledWith({ conn: "c1", note: undefined, transcript: undefined });
  });
  it("no key can choose", async () => {
    await open();
    const note = screen.getByLabelText("Say what you did or what to do next");
    await fireEvent.keyDown(note, { key: "Enter" });
    await fireEvent.keyDown(note, { key: "Enter", metaKey: true });
    expect(svc.Handback).not.toHaveBeenCalled();
  });
  it("Esc keeps driving", async () => {
    await open();
    const note = screen.getByLabelText("Say what you did or what to do next");
    await fireEvent.keyDown(note, { key: "Escape" });
    expect(ssh.view(SID).handback).toBeNull();
    expect(svc.Handback).not.toHaveBeenCalled();
  });
  it("a preview failure still allows a note, and an empty one disables with", async () => {
    svc.HandbackPreview.mockRejectedValue(new Error("gone"));
    await open();
    expect(await screen.findByText(/Could not read your terminal/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Nothing to send" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Hand back without it" })).toBeEnabled();
  });
});

describe("the composer", () => {
  it("says what happens, sends on Enter and keeps a line break on shift", async () => {
    const v = seed([]);
    render(SSHComposer, { sessionId: SID });
    expect(screen.getByText("Claude is working. It gets your message at its next command.")).toBeInTheDocument();
    const field = screen.getByRole("textbox");
    expect(screen.getByRole("button", { name: "Send to Claude" })).toBeDisabled();
    await fireEvent.input(field, { target: { value: "look at logrotate" } });
    await fireEvent.keyDown(field, { key: "Enter", shiftKey: true });
    expect(svc.Say).not.toHaveBeenCalled();
    await fireEvent.keyDown(field, { key: "Enter" });
    expect(svc.Say).toHaveBeenCalledWith(SID, expect.any(String), "look at logrotate");
    await waitFor(() => expect(v.draft).toBe(""));
  });
  it("keeps the draft when sending fails", async () => {
    svc.Say.mockRejectedValue("no");
    const v = seed([]);
    v.draft = "precious";
    render(SSHComposer, { sessionId: SID });
    await fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter" });
    await waitFor(() => expect(svc.Say).toHaveBeenCalled());
    expect(v.draft).toBe("precious");
  });
  it("shows what is still running while you drive, and no explanation", () => {
    seed([{ id: "i", kind: "block", order: 1, seq: 1, block: block({ status: "running", command: "journalctl -fu api", exitCode: undefined }) }], [conn({ wheel: "human" })]);
    render(SSHComposer, { sessionId: SID });
    expect(screen.queryByText(/Your commands appear in the timeline/)).toBeNull();
    expect(screen.getByText("1 command still running.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Hand back…" })).toBeNull();
    expect(screen.getByRole("textbox")).toHaveAttribute("placeholder", "Ask Claude to carry on…");
  });
});

describe("the stream", () => {
  it("swallows a bare key it does not list, so x cannot reach the app", async () => {
    seed([{ id: "i", kind: "block", order: 1, seq: 1, block: block() }]);
    const onWindow = vi.fn();
    window.addEventListener("keydown", onWindow);
    render(SSHStream, { sessionId: SID });
    await fireEvent.keyDown(screen.getByRole("log"), { key: "x" });
    expect(onWindow).not.toHaveBeenCalled();
    window.removeEventListener("keydown", onWindow);
  });
  it("leaves a chord alone", async () => {
    seed([]);
    const onWindow = vi.fn();
    window.addEventListener("keydown", onWindow);
    render(SSHStream, { sessionId: SID });
    await fireEvent.keyDown(screen.getByRole("log"), { key: "c", metaKey: true });
    expect(onWindow).toHaveBeenCalled();
    window.removeEventListener("keydown", onWindow);
  });
  it("bare Y does nothing unless a card holds focus", async () => {
    vi.useFakeTimers();
    seed([{ id: "i", kind: "block", order: 1, seq: 1, block: block({ status: "pending_approval", approval: { granted: false } }) }]);
    render(SSHStream, { sessionId: SID });
    render(SSHDock, { sessionId: SID });
    await vi.advanceTimersByTimeAsync(800);
    await fireEvent.keyDown(screen.getByRole("log"), { key: "Y" });
    await fireEvent.keyDown(window, { key: "Y" });
    expect(svc.Approve).not.toHaveBeenCalled();
  });
  it("walks the items and expands from the keyboard", async () => {
    const v = seed([
      { id: "i1", kind: "block", order: 1, seq: 1, block: block({ id: "b1" }) },
      { id: "i2", kind: "element", order: 2, seq: 2, element: el() },
    ]);
    render(SSHStream, { sessionId: SID });
    const log = screen.getByRole("log");
    await fireEvent.keyDown(log, { key: "j" });
    expect(v.cursor).toBe("i1");
    await fireEvent.keyDown(log, { key: "Enter" });
    expect(v.expanded.has("b1")).toBe(true);
    await fireEvent.keyDown(log, { key: "ArrowLeft" });
    expect(v.expanded.has("b1")).toBe(false);
    await fireEvent.keyDown(log, { key: "a" });
    expect(v.draft).toBe("About b1: ");
  });
  it("Facts hides every claim and folds nothing", async () => {
    const ok = (id: string, order: number) => ({ id, kind: "block", order, seq: order, block: block({ id: `b${order}`, command: `cmd${order}` }) });
    const v = seed([ok("a", 1), ok("b", 2), ok("c", 3), { id: "n", kind: "element", order: 4, seq: 4, element: el({ title: "a claim" }) }]);
    render(SSHStream, { sessionId: SID });
    expect(screen.getByText("3 commands · all exit 0 · 1.2s total")).toBeInTheDocument();
    expect(screen.getByText("a claim")).toBeInTheDocument();
    v.filter = "facts";
    await waitFor(() => expect(screen.queryByText("a claim")).toBeNull());
    expect(screen.getByText("cmd1")).toBeInTheDocument();
  });
  it("names the three voices in its legend", () => {
    seed([]);
    render(SSHStream, { sessionId: SID });
  });
  it("words the empty states", async () => {
    const v = seed([]);
    render(SSHStream, { sessionId: SID });
    expect(screen.getByText("Connected. Claude has not run anything yet.")).toBeInTheDocument();
    v.filter = "facts";
    await screen.findByText("No commands yet.");
    v.filter = "findings";
    await screen.findByText("Claude has not posted any findings or theories yet.");
  });
  it("offers a retry and a restart for the two fetch failures", async () => {
    const v = seed([]);
    v.error = "boom";
    const r = render(SSHStream, { sessionId: SID });
    expect(screen.getByText("Could not load this timeline. Lola keeps recording it.")).toBeInTheDocument();
    r.unmount();
    v.predates = true;
    render(SSHStream, { sessionId: SID });
    expect(screen.getByText("This daemon predates SSH sessions. Restart it to use them.")).toBeInTheDocument();
  });
  it("shows a message's delivery state, with Recall while queued", async () => {
    svc.Recall.mockResolvedValue({ recalled: false });
    const v = seed([{ id: "h1", kind: "human", order: 1, seq: 1, human: { text: "look there", delivered: false } }]);
    render(SSHStream, { sessionId: SID });
    expect(screen.getByText("Queued")).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Recall" }));
    await waitFor(() => expect(v.flash).toBe("Too late: Claude already has this."));
  });
});

describe("the workspace", () => {
  it("fetches on mount and carries the band and header", async () => {
    useSession({ ssh: { connections: [conn()], lastSeq: 7 } });
    const v = ssh.view(SID);
    v.connections = [conn()] as never;
    render(SSHWorkspace, { sessionId: SID });
    await waitFor(() => expect(svc.Timeline).toHaveBeenCalledWith({ session: SID, after: 0 }));
    expect(screen.getAllByText("prod-api").length).toBeGreaterThan(0);
    expect(screen.getAllByText("PRODUCTION").length).toBeGreaterThan(0);
    expect(document.querySelector("[data-ssh-workspace] > div")?.className).toContain("bg-bad/70");
  });
  it("says what will happen when nothing is connected yet", () => {
    useSession({ ssh: { connections: [conn({ state: "requested" })], lastSeq: 1 } });
    ssh.view(SID).connections = [conn({ state: "requested" })] as never;
    render(SSHWorkspace, { sessionId: SID });
    expect(screen.getByText("No connection yet. Nothing has been attempted.")).toBeInTheDocument();
    expect(screen.getByText("Waiting for your go")).toBeInTheDocument();
  });
  it("offers a new shell when the human left theirs", () => {
    useSession({ ssh: { connections: [conn({ tabAlive: false, idleLingerSeconds: 900 })], lastSeq: 1 } });
    ssh.view(SID).connections = [conn({ tabAlive: false, idleLingerSeconds: 900 })] as never;
    render(SSHWorkspace, { sessionId: SID });
    expect(screen.getByRole("button", { name: "Open a new shell" })).toBeInTheDocument();
    expect(screen.getByText(/It closes by itself after 15 minutes/)).toBeInTheDocument();
  });
  it("shows one view at a time, and keeps the terminal mounted behind the timeline", async () => {
    useSession({ ssh: { connections: [conn()], lastSeq: 1 } });
    const v = ssh.view(SID);
    v.connections = [conn()] as never;
    render(SSHWorkspace, { sessionId: SID });
    const t = screen.getByTestId("terminal");
    expect(t.dataset.name).toBe("s-ssh-1");
    expect(t.closest(".invisible")).not.toBeNull();
    expect(screen.getByRole("textbox")).toBeInTheDocument();
    // The switch is gone. It is in the ⋯ menu, and the terminal itself has the way back.
    expect(screen.queryByRole("button", { name: "Terminal" })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "More connection actions" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Open terminal" }));
    expect(screen.getByTestId("terminal")).toBe(t);
    expect(t.closest(".invisible")).toBeNull();
    expect(screen.queryByRole("textbox")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: /Timeline/ }));
    expect(t.closest(".invisible")).not.toBeNull();
  });
  it("asks again by itself after a daemon that predated ssh is replaced", async () => {
    vi.useFakeTimers();
    try {
      useSession({ ssh: { connections: [conn()], lastSeq: 1 } });
      const v = ssh.view(SID);
      v.connections = [conn()] as never;
      const fetch = vi.spyOn(v, "fetch").mockResolvedValue();
      v.predates = true;
      render(SSHWorkspace, { sessionId: SID });
      await vi.advanceTimersByTimeAsync(0);
      fetch.mockClear();
      await vi.advanceTimersByTimeAsync(3100);
      expect(fetch).toHaveBeenCalled();
      fetch.mockRestore();
    } finally {
      vi.useRealTimers();
    }
  });
  it("says closed, and offers the next step for a direct session", () => {
    useSession({ location: "ssh", ssh: { connections: [conn({ state: "closed", commandsRun: 2 })], lastSeq: 1 } });
    ssh.view(SID).connections = [conn({ state: "closed", commandsRun: 2 })] as never;
    render(SSHWorkspace, { sessionId: SID });
    expect(screen.getByRole("button", { name: "New connection" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "End session…" })).toBeInTheDocument();
    expect(screen.getByText(/The remote process may keep running/)).toBeInTheDocument();
  });
});

describe("one composer, two destinations", () => {
  beforeEach(() => {
    svc.RunCommand.mockReset().mockResolvedValue({ id: "b9" });
    svc.ShareBlock.mockReset().mockResolvedValue({ id: "b1" });
    svc.Say.mockReset().mockResolvedValue({ item: "x" });
  });
  const dev = () => conn({ tier: "dev", state: "connected" });

  it("sends to the agent on enter and runs on the host on command-enter", async () => {
    const v = seed([], [dev()]);
    v.draft = "look at the logs";
    render(SSHComposer, { sessionId: SID });
    await fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter" });
    await waitFor(() => expect(svc.Say).toHaveBeenCalledWith(SID, expect.any(String), "look at the logs"));
    expect(svc.RunCommand).not.toHaveBeenCalled();
    await waitFor(() => expect(v.draft).toBe(""));
    v.draft = "uptime";
    await tick();
    await fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter", metaKey: true });
    await waitFor(() => expect(svc.RunCommand).toHaveBeenCalledWith({ session: SID, conn: "c1", command: "uptime" }));
    await waitFor(() => expect(v.draft).toBe(""));
    expect(v.expanded.has("b9")).toBe(true);
    expect(v.history).toEqual(["uptime"]);
  });

  it("never runs on plain enter by default, on any tier", async () => {
    const v = seed([], [conn({ tier: "production", state: "connected" })]);
    v.draft = "please look at the logs";
    render(SSHComposer, { sessionId: SID });
    await fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter" });
    await waitFor(() => expect(svc.Say).toHaveBeenCalled());
    expect(svc.RunCommand).not.toHaveBeenCalled();
  });

  it("shows the run prompt while command is held in the field, and only then", async () => {
    vi.useFakeTimers();
    try {
      seed([], [{ ...dev(), cwd: "/srv/app" }]);
      render(SSHComposer, { sessionId: SID });
      const field = screen.getByRole("textbox");
      await fireEvent.keyDown(field, { key: "Meta" });
      expect(document.querySelector("[data-composer=ask]")).not.toBeNull(); // a chord must not flash it
      await vi.advanceTimersByTimeAsync(200);
      expect(document.querySelector("[data-composer=run]")).not.toBeNull();
      expect(document.querySelector("[data-run-prompt]")?.textContent).toContain(":/srv/app");
      await fireEvent.keyUp(field, { key: "Meta" });
      await vi.advanceTimersByTimeAsync(0);
      expect(document.querySelector("[data-composer=ask]")).not.toBeNull();
      await fireEvent.keyDown(window, { key: "Meta" });
      await vi.advanceTimersByTimeAsync(200);
      expect(document.querySelector("[data-composer=ask]")).not.toBeNull(); // outside the field it means nothing
    } finally {
      vi.useRealTimers();
    }
  });

  const tap = async (field: HTMLElement) => {
    await fireEvent.keyDown(field, { key: "Meta" });
    await fireEvent.keyUp(field, { key: "Meta" });
  };

  it("swaps the keys on a double command tap, and swaps back", async () => {
    const v = seed([], [dev()]);
    v.draft = "uptime";
    render(SSHComposer, { sessionId: SID });
    const field = screen.getByRole("textbox");
    await tap(field);
    expect(v.runDefault).toBe(false); // one tap is nothing
    await tap(field);
    expect(v.runDefault).toBe(true);
    await waitFor(() => expect(document.querySelector("[data-composer=run]")).not.toBeNull());
    await fireEvent.keyDown(field, { key: "Enter" });
    await waitFor(() => expect(svc.RunCommand).toHaveBeenCalled());
    expect(svc.Say).not.toHaveBeenCalled();
    await waitFor(() => expect(v.draft).toBe(""));
    v.draft = "hello";
    await tick();
    await fireEvent.keyDown(field, { key: "Enter", metaKey: true });
    await waitFor(() => expect(svc.Say).toHaveBeenCalledWith(SID, expect.any(String), "hello"));
    await tap(field);
    await tap(field);
    expect(v.runDefault).toBe(false);
  });

  it("does not take a chord or a slow pair for a double tap", async () => {
    const v = seed([], [dev()]);
    render(SSHComposer, { sessionId: SID });
    const field = screen.getByRole("textbox");
    await tap(field);
    await fireEvent.keyDown(field, { key: "Meta" });
    await fireEvent.keyDown(field, { key: "c", metaKey: true });
    await fireEvent.keyUp(field, { key: "Meta" });
    expect(v.runDefault).toBe(false);
  });

  it("swaps the same way on production, in the run look", async () => {
    const v = seed([], [conn({ tier: "production", state: "connected" })]);
    v.draft = "uptime";
    render(SSHComposer, { sessionId: SID });
    const field = screen.getByRole("textbox");
    await tap(field);
    await tap(field);
    expect(v.runDefault).toBe(true);
    await waitFor(() => expect(document.querySelector("[data-composer=run] .bg-bad")).not.toBeNull());
    await fireEvent.keyDown(field, { key: "Enter" });
    await waitFor(() => expect(svc.RunCommand).toHaveBeenCalled());
    expect(svc.Say).not.toHaveBeenCalled();
  });

  it("offers both destinations as buttons, and no toggle or explanation", () => {
    const v = seed([], [dev()]);
    v.draft = "x";
    render(SSHComposer, { sessionId: SID });
    expect(screen.queryByRole("radiogroup")).toBeNull();
    expect(screen.getByRole("button", { name: "Send to Claude" })).toBeEnabled();
    expect(screen.getByRole("button", { name: /^Run on/ })).toBeEnabled();
    expect(document.querySelector("[data-run-note]")).toBeNull();
  });

  it("sends an interactive program into your shell and shows it there", async () => {
    const v = seed([], [{ ...dev(), termTracked: true, termAtPrompt: true }]);
    svc.RunCommand.mockResolvedValue({ id: "", origin: "terminal" });
    v.draft = "vim /etc/hosts";
    render(SSHComposer, { sessionId: SID });
    await fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter", metaKey: true });
    await waitFor(() => expect(svc.RunCommand).toHaveBeenCalled());
    await waitFor(() => expect(v.narrow).toBe("terminal"));
  });

  it("will not run an interactive program with no shell to take it", async () => {
    const v = seed([], [dev()]);
    v.draft = "vim /etc/hosts";
    render(SSHComposer, { sessionId: SID });
    await fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter", metaKey: true });
    expect(svc.RunCommand).not.toHaveBeenCalled();
    expect(screen.getByRole("status").textContent).toMatch(/looks interactive/);
  });

  it("recalls commands with up from an empty field", async () => {
    const v = seed([], [dev()]);
    v.history = ["ls", "uptime"];
    render(SSHComposer, { sessionId: SID });
    await fireEvent.keyDown(screen.getByRole("textbox"), { key: "ArrowUp" });
    expect(v.draft).toBe("uptime");
    await fireEvent.keyDown(screen.getByRole("textbox"), { key: "ArrowUp" });
    expect(v.draft).toBe("ls");
    await fireEvent.keyDown(screen.getByRole("textbox"), { key: "ArrowDown" });
    expect(v.draft).toBe("uptime");
  });

  it("marks sudo on the block, however it ran", () => {
    seed([], [dev()]);
    render(SSHBlock, { sessionId: SID, block: block({ elevated: true, command: "sudo ls" }) as never });
    expect(document.querySelector("[data-elevated]")?.textContent).toBe("sudo");
  });

  it("offers to move the shell to the shared folder only when it can", async () => {
    svc.TerminalCd.mockResolvedValue({});
    const at = { ...dev(), cwd: "/srv/app", termCwd: "/home/m", termTracked: true, termAtPrompt: true, tabAlive: true };
    seed([], [at]);
    render(SSHTerminalPane, { sessionId: SID, conn: at as never });
    await fireEvent.click(screen.getByRole("button", { name: "Go there" }));
    expect(svc.TerminalCd).toHaveBeenCalledWith("c1");
    cleanup();
    render(SSHTerminalPane, { sessionId: SID, conn: { ...at, termAtPrompt: false } as never });
    expect(screen.queryByRole("button", { name: "Go there" })).toBeNull();
    cleanup();
    render(SSHTerminalPane, { sessionId: SID, conn: { ...at, termCwd: "/srv/app" } as never });
    expect(screen.queryByRole("button", { name: "Go there" })).toBeNull();
  });

  it("styles your command apart, leads with the command, and offers sharing on it", async () => {
    const v = seed([], [dev()]);
    v.expanded.add("b1");
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ actor: "human", private: true }) as never });
    expect(container.querySelector("[data-plate]")?.getAttribute("data-actor")).toBe("you");
    await fireEvent.click(screen.getByRole("button", { name: "Share with Claude" }));
    expect(svc.ShareBlock).toHaveBeenCalledWith({ session: SID, block: block().id });
    expect(screen.getAllByRole("button", { name: /copy/i })).toHaveLength(1);
    expect(screen.queryByRole("tablist")).toBeNull();
    cleanup();
    render(SSHBlock, { sessionId: SID, block: block() as never });
    expect(document.querySelector("[data-plate]")?.getAttribute("data-actor")).toBe("agent");
  });

  it("keeps a command's facts behind (i), and shows only what changes the reading", async () => {
    const v = seed([], [dev()]);
    v.expanded.add("b1");
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ stdoutBytes: 5 }) as never });
    expect(container.querySelector("[data-output-info]")).toBeNull();
    expect(screen.queryByText("exit 0")).toBeNull();
    expect(screen.queryByRole("button", { name: /Details/ })).toBeNull();
    const corner = container.querySelector("[data-output-corner]") as HTMLElement;
    expect(within(corner).getByRole("button", { name: "Copy output" })).toBeInTheDocument();
    await fireEvent.click(within(corner).getByRole("button", { name: "About this command" }));
    expect(container.querySelector("[data-output-info] [data-info-who]")).not.toBeNull();
    cleanup();
    v.expanded.add("b1");
    render(SSHBlock, { sessionId: SID, block: block({ status: "failed", exitCode: 3, stdoutBytes: 5 }) as never });
    expect(screen.getByText(/exit 3/)).toBeInTheDocument();
  });

  it("offers the streams only when both have something", () => {
    const v = seed([], [dev()]);
    v.expanded.add("b1");
    render(SSHBlock, { sessionId: SID, block: block({ stdoutBytes: 5, stderrBytes: 5 }) as never });
    expect(screen.getByRole("tab", { name: "Errors" })).toBeInTheDocument();
  });

  it("keeps who-drives bookkeeping out of the timeline, and in the facts", async () => {
    const v = seed([{ id: "s1", kind: "system", order: 1, seq: 1, at: "", system: { kind: "wheel_taken", text: "You took the wheel.", conn: "c1" } }] as never, [dev()]);
    render(SSHStream, { sessionId: SID });
    expect(screen.queryByText("You took the wheel.")).toBeNull();
    v.filter = "facts";
    await tick();
    expect(screen.getByText("You took the wheel.")).toBeInTheDocument();
  });
});

describe("the agent's answer", () => {
  it("renders lists and code as structure, whole, and as text", () => {
    const blocks = richBlocks("3 containers:\n- **registry** on `5000`\n- db\n\n1. first\n```\n<b>x</b>\n```");
    expect(blocks.map((b) => b.kind)).toEqual(["lead", "ul", "ol", "code"]);
    expect(inlineSpans("**registry** on `5000`")).toEqual([
      { kind: "bold", text: "registry" },
      { kind: "text", text: " on " },
      { kind: "code", text: "5000" },
    ]);
    render(SSHElement, { sessionId: SID, element: { id: "e", kind: "narration", title: "Found:\n- <img src=x> one\n- two", revision: 1 } as never });
    expect(screen.getAllByRole("listitem")).toHaveLength(2);
    expect(document.querySelector("img")).toBeNull();
    expect(document.querySelector("[data-answer]")).not.toBeNull();
  });
  it("keeps a one-line status quiet", () => {
    render(SSHElement, { sessionId: SID, element: { id: "e", kind: "narration", title: "Reading the journal", revision: 1 } as never });
    expect(document.querySelector("[data-answer]")).toBeNull();
  });
});

describe("a tab per connection", () => {
  const a = () => conn({ id: "ca", host: "h", state: "connected" });
  const b = () => conn({ id: "cb", host: "h", state: "connected" });
  const block = (id: string, c: string) => ({ id, kind: "block", seq: 1, order: 1, block: { id, conn: c, command: id, status: "succeeded" } });

  it("numbers a second tab to one host, and gives a denied request none", () => {
    const rows = sshTabs([a(), b(), conn({ id: "cd", host: "x", state: "denied" })] as never);
    expect(rows.map((r) => r.conn.id)).toEqual(["ca", "cb"]);
    expect(rows[1].label).toBe(`${rows[0].label} 2`);
    expect(sshTabs([conn({ id: "cp", state: "connected", prompt: { id: "p" } })] as never)[0].count).toBe(1);
  });

  it("keeps each tab's timeline and draft apart", () => {
    const v = ssh.view(SID);
    v.connections = [a(), b()] as never;
    v.items = [block("b1", "ca"), block("b2", "cb"), { id: "e0", kind: "element", element: { id: "e0", kind: "narration", title: "both", revision: 1 } }] as never;
    v.activeConn = "ca";
    v.draft = "rm -rf /tmp/x";
    expect(v.tabItems.map((i) => i.id)).toEqual(["b1", "e0"]);
    v.activeConn = "cb";
    expect(v.tabItems.map((i) => i.id)).toEqual(["b2", "e0"]);
    expect(v.draft).toBe("");
    v.activeConn = "ca";
    expect(v.draft).toBe("rm -rf /tmp/x");
    v.draft = "";
  });

  it("asks once, then deletes and lands on the tab beside it", async () => {
    const v = ssh.view(SID);
    v.connections = [a(), b()] as never;
    v.items = [block("b1", "ca"), block("b2", "cb")] as never;
    v.activeConn = "ca";
    svc.Trash.mockReset().mockResolvedValue({});
    const ask = vi.spyOn(confirm, "ask");
    ssh.trashTab(SID, a() as never);
    expect(ask).toHaveBeenCalledTimes(1);
    expect(svc.Trash).not.toHaveBeenCalled();
    ask.mock.calls[0][0].onConfirm();
    await waitFor(() => expect(svc.Trash).toHaveBeenCalledWith("ca"));
    await waitFor(() => expect(v.activeConn).toBe("cb"));
    expect(v.connections.map((c) => c.id)).toEqual(["cb"]);
    expect(v.items.map((i) => i.id)).toEqual(["b2"]);
    ask.mockRestore();
  });

  it("drops what a tab trashed elsewhere owned at the next fetch", async () => {
    const v = ssh.view(SID);
    v.bind(SID);
    v.connections = [a(), b()] as never;
    v.items = [block("b1", "ca"), block("b2", "cb")] as never;
    svc.Timeline.mockResolvedValue({ items: [], connections: [b()], lastSeq: 9 });
    await v.fetch();
    expect(v.items.map((i) => i.id)).toEqual(["b2"]);
  });
});

describe("the chip", () => {
  it("reads tier, host and the most urgent fact, and opens the workspace", async () => {
    const open = vi.spyOn(ssh, "open");
    render(SSHChip, { sessionId: SID, info: { connections: [conn({ pendingApprovals: 2 })], needsHuman: true, lastSeq: 1 } as never });
    expect(screen.getByText("PROD")).toBeInTheDocument();
    expect(screen.getByText("2 to approve")).toHaveClass("text-orange");
    await fireEvent.click(screen.getByText("2 to approve"));
    expect(open).toHaveBeenCalledWith(SID, { conn: "c1", dock: true });
  });
  it("draws nothing once every connection ended", () => {
    const { container } = render(SSHChip, { sessionId: SID, info: { connections: [conn({ state: "closed" })], lastSeq: 1 } as never });
    expect(container.querySelector("[data-ssh-chip]")).toBeNull();
  });
});

describe("new session", () => {
  it("offers no hosts yet a way to add one", async () => {
    render(NewSSHSession);
    expect(await screen.findByRole("button", { name: "Open SSH host settings" })).toBeInTheDocument();
  });
  it("disables a refused host, shows why, and opens with the go", async () => {
    svc.Hosts.mockResolvedValue({
      hosts: [
        { name: "billing", alias: "billing", tier: "production", grantMaxMinutes: 0, directRefused: "billing is tier production and no sandbox is available." },
        { name: "prod", alias: "prod", tier: "production", grantMaxMinutes: 0, directSandbox: "docker", directAgent: "claude" },
      ],
    });
    svc.Open.mockRejectedValue("ssh is missing");
    render(NewSSHSession);
    const refused = await screen.findByRole("radio", { name: /billing/ });
    expect(refused).toBeDisabled();
    expect(screen.getByText(/no sandbox is available/)).toBeInTheDocument();
    const prod = await screen.findByRole("radio", { name: /prod/ });
    await waitFor(() => expect(prod).toHaveAttribute("aria-checked", "true"));
    await fireEvent.click(screen.getByRole("button", { name: "Connect to prod" }));
    await screen.findByRole("alert");
    expect(screen.getByRole("alert")).toHaveTextContent("ssh is missing");
    expect(svc.Open).toHaveBeenCalledWith(expect.objectContaining({ host: "prod" }));
  });
  it("has no who-drives switch, and one short line for an unsandboxed agent", async () => {
    svc.Hosts.mockResolvedValue({ hosts: [{ name: "dev", alias: "dev", tier: "dev", grantMaxMinutes: 120, directSandbox: "none" }] });
    render(NewSSHSession);
    await screen.findByText(/runs unsandboxed on this Mac/);
    expect(screen.queryByRole("radio", { name: "You" })).toBeNull();
  });
});

describe("host settings", () => {
  it("selects a config host and inherits its connection settings", async () => {
    vi.mocked(ConfigService.SSHConfigAliases).mockResolvedValueOnce(["web-1", "staging"]);
    render(SSHHostSettings);
    await fireEvent.click(await screen.findByRole("button", { name: /Add host/ }));
    await screen.findByRole("option", { name: "staging" });
    await fireEvent.change(screen.getByLabelText("Connection"), { target: { value: "web-1" } });
    expect(await screen.findByText("deploy@web-1:22")).toBeInTheDocument();
    expect(screen.queryByLabelText("Hostname or IP")).toBeNull();
    await fireEvent.input(screen.getByLabelText("Label"), { target: { value: "Web" } });
    await fireEvent.click(screen.getByRole("button", { name: "Save host" }));
    await waitFor(() => expect(ConfigService.SaveSSHHost).toHaveBeenLastCalledWith("", expect.objectContaining({ alias:"web-1", label:"Web", user:"", port:0, identityFile:"" })));
    expect(svc.Go).not.toHaveBeenCalled();
  });

  it("saves custom data when no SSH config is available", async () => {
    vi.mocked(ConfigService.SSHConfigAliases).mockRejectedValueOnce(new Error("unreadable"));
    render(SSHHostSettings);
    await fireEvent.click(await screen.findByRole("button", { name: /Add host/ }));
    expect(await screen.findByText(/Could not read SSH config/)).toBeInTheDocument();
    await fireEvent.change(screen.getByLabelText("Connection"), { target: { value: ":custom" } });
    await fireEvent.input(screen.getByLabelText("Hostname or IP"), { target: { value: "192.0.2.10" } });
    await fireEvent.input(screen.getByLabelText("Port"), { target: { value: "2222" } });
    await fireEvent.input(screen.getByLabelText("Identity file"), { target: { value: "~/.ssh/custom key" } });
    await fireEvent.input(screen.getByLabelText("Remote user"), { target: { value: "ops" } });
    await fireEvent.click(screen.getByRole("button", { name: "Save host" }));
    await waitFor(() => expect(ConfigService.SaveSSHHost).toHaveBeenLastCalledWith("", expect.objectContaining({ alias:"192.0.2.10", user:"ops", port:2222, identityFile:"~/.ssh/custom key" })));
  });

  it("ignores an older host preview arriving after the selected host", async () => {
    let finish!: (value: never) => void;
    vi.mocked(ConfigService.SSHConfigAliases).mockResolvedValueOnce(["first", "second"]);
    vi.mocked(ConfigService.ResolveSSHHost).mockImplementationOnce(() => new Promise((r) => { finish = r; }) as never)
      .mockResolvedValueOnce({ hostname:"second.example", user:"ops", port:"22" } as never);
    render(SSHHostSettings);
    await fireEvent.click(await screen.findByRole("button", { name: /Add host/ }));
    await screen.findByRole("option", { name: "first" });
    await fireEvent.change(screen.getByLabelText("Connection"), { target: { value: "first" } });
    await fireEvent.change(screen.getByLabelText("Connection"), { target: { value: "second" } });
    expect(await screen.findByText("ops@second.example:22")).toBeInTheDocument();
    finish({ hostname:"first.example", user:"wrong", port:"22" } as never);
    await tick();
    expect(screen.queryByText(/wrong@first/)).toBeNull();
    expect(screen.getByText("ops@second.example:22")).toBeInTheDocument();
  });

  it("hides grants and shows the override only on production, and saves through config", async () => {
    render(SSHHostSettings);
    await fireEvent.click(await screen.findByRole("button", { name: /Add host/ }));
    expect(screen.getByRole("checkbox")).toBeInTheDocument();
    expect(screen.queryByLabelText("Longest grant")).toBeNull();
    await fireEvent.click(screen.getByRole("radio", { name: "Dev" }));
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(screen.getByLabelText("Longest grant")).toBeInTheDocument();
    await fireEvent.change(screen.getByLabelText("Connection"), { target: { value: ":custom" } });
    await fireEvent.input(screen.getByLabelText("Hostname or IP"), { target: { value: "web-1" } });
    await fireEvent.click(screen.getByRole("button", { name: "Save host" }));
    await waitFor(() => expect(ConfigService.SaveSSHHost).toHaveBeenCalled());
    const [, dto] = (ConfigService.SaveSSHHost as ReturnType<typeof vi.fn>).mock.calls.at(-1) as [string, { alias: string; tier: string }];
    expect(dto).toMatchObject({ alias: "web-1", tier: "dev" });
  });
});

describe("review regressions", () => {
  const pending = { id: "b1", kind: "block", order: 1, seq: 1, at: "", block: block({ status: "pending_approval", approval: { granted: false } }) };
  const approvalProps = (o: Record<string, unknown> = {}) => ({
    sessionId: SID,
    conn: conn() as never,
    block: block({ status: "pending_approval", approval: { granted: false }, ...o }) as never,
  });

  it("mounts the workspace for a session nothing has viewed yet", () => {
    // No seed() and no ssh.view: the first call is the one inside $derived, where a
    // reactive write would throw state_unsafe_mutation.
    useSession({ location: "ssh" });
    const { container } = render(SSHWorkspace, { sessionId: SID });
    expect(container.querySelector("[data-ssh-workspace]")).not.toBeNull();
  });

  it("does not answer on a held key", async () => {
    vi.useFakeTimers();
    seed([]);
    const { container } = render(SSHApproval, approvalProps());
    await vi.advanceTimersByTimeAsync(800);
    const card = container.querySelector("[data-card]") as HTMLElement;
    await fireEvent.keyDown(card, { key: "Y", repeat: true });
    expect(svc.Approve).not.toHaveBeenCalled();
    await fireEvent.keyDown(card, { key: "Y" });
    expect(svc.Approve).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });

  it("does not give a go on a held key", async () => {
    vi.useFakeTimers();
    seed([]);
    const { container } = render(SSHGoCard, { sessionId: SID, conn: conn({ state: "requested" }) as never });
    await vi.advanceTimersByTimeAsync(800);
    await fireEvent.keyDown(container.querySelector("[data-card]") as HTMLElement, { key: "Y", repeat: true });
    expect(svc.Go).not.toHaveBeenCalled();
    vi.useRealTimers();
  });

  it("holds the confirm until the end of the command has been on screen", async () => {
    vi.useFakeTimers();
    seed([]);
    const top = vi.spyOn(HTMLSpanElement.prototype, "getBoundingClientRect").mockReturnValue({ top: 900, bottom: 900 } as DOMRect);
    const bottom = vi.spyOn(HTMLDivElement.prototype, "getBoundingClientRect").mockReturnValue({ top: 0, bottom: 300 } as DOMRect);
    const long = Array.from({ length: 20 }, (_, i) => `line${i}`).join("\n");
    const { container } = render(SSHApproval, approvalProps({ command: long }));
    await vi.advanceTimersByTimeAsync(800);
    const run = screen.getByRole("button", { name: /Run once/ });
    expect(run).toBeDisabled();
    expect(screen.getByText(/Scroll to the end to run this/)).toBeInTheDocument();
    await fireEvent.keyDown(container.querySelector("[data-card]") as HTMLElement, { key: "Y" });
    expect(svc.Approve).not.toHaveBeenCalled();
    top.mockReturnValue({ top: 280, bottom: 290 } as DOMRect);
    await fireEvent.scroll(container.querySelector("[data-card-body]") as HTMLElement);
    expect(run).not.toBeDisabled();
    top.mockRestore();
    bottom.mockRestore();
    vi.useRealTimers();
  });

  it("pins the decision outside the card's scroller, and has only one scroller", () => {
    seed([]);
    const { container } = render(SSHApproval, approvalProps({ command: "x\n".repeat(40) }));
    const body = container.querySelector("[data-card-body]") as HTMLElement;
    const actions = container.querySelector("[data-card-actions]") as HTMLElement;
    expect(body.contains(screen.getByRole("button", { name: /Deny/ }))).toBe(false);
    expect(actions.contains(screen.getByRole("button", { name: /Run once/ }))).toBe(true);
    expect((container.querySelector("[data-command]") as HTMLElement).className).not.toContain("overflow-y");
    // The command leads. The small print is folded below it, and does not gate the confirm.
    expect(body.querySelector("[data-warnings]")?.compareDocumentPosition(body.querySelector("[data-command]") as Node)).toBe(
      Node.DOCUMENT_POSITION_PRECEDING,
    );
  });

  it("gives focus to the card, so its keys work after A", async () => {
    vi.useFakeTimers();
    const v = seed([pending]);
    render(SSHDock, { sessionId: SID });
    v.dockFocus++;
    await vi.advanceTimersByTimeAsync(800);
    const card = document.querySelector("[data-card]") as HTMLElement;
    expect(document.activeElement).toBe(card);
    await fireEvent.keyDown(document.activeElement as HTMLElement, { key: "Y" });
    expect(svc.Approve).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });

  it("does not repeat the transcript sentence the daemon already wrote", () => {
    seed([
      {
        id: "s1",
        kind: "system",
        order: 1,
        seq: 1,
        at: "",
        system: { kind: "handed_back", text: "You handed the wheel back after 12 minutes. Transcript sent.", hasTranscript: true, conn: "c1" },
      },
    ]);
    render(SSHStream, { sessionId: SID });
    expect(screen.getByRole("log").textContent?.match(/Transcript sent\./g)).toHaveLength(1);
  });

  it("keeps the marker from stretching over the rail", () => {
    seed([{ id: "b1", kind: "block", order: 1, seq: 1, at: "", block: block() }]);
    const { container } = render(SSHStream, { sessionId: SID });
    const marker = container.querySelector("[data-row] > .relative.flex") as HTMLElement;
    expect(marker.className).toContain("items-start");
  });

  it("folds a transcript behind the shared disclosure, with its since-time", async () => {
    const at = "2026-01-01T14:02:00Z";
    seed([
      { id: "s1", kind: "system", order: 1, seq: 1, at, system: { kind: "wheel_taken", text: "x", conn: "c1" } },
      { id: "h1", kind: "human", order: 2, seq: 2, at, human: { origin: "handback", conn: "c1", text: "done", transcript: "uptime", delivered: true } },
    ]);
    const { container } = render(SSHStream, { sessionId: SID });
    expect(container.querySelector("details")).toBeNull();
    const btn = screen.getByRole("button", { name: /Your terminal since .* · 6 B/ });
    expect(btn).toHaveAttribute("aria-expanded", "false");
    await fireEvent.click(btn);
    expect(btn).toHaveAttribute("aria-expanded", "true");
  });

  it("the auth card shows a parked terminal under the narrow split", async () => {
    const v = seed([]);
    v.narrow = "stream";
    render(SSHConnCard, { sessionId: SID, conn: conn({ state: "authenticating" }) as never, kind: "auth" });
    const before = v.terminalFocus;
    await fireEvent.click(screen.getByRole("button", { name: "Open terminal" }));
    expect(v.narrow).toBe("terminal");
    expect(v.terminalFocus).toBe(before + 1);
  });

  it("authentication starting leaves the timeline where it is", async () => {
    useSession({ ssh: { connections: [conn({ state: "authenticating" })], lastSeq: 0 } });
    const v = seed([], [conn({ state: "authenticating" })]);
    v.narrow = "stream";
    render(SSHWorkspace, { sessionId: SID });
    await tick();
    expect(v.narrow).toBe("stream");
  });

  it("uses the singular for one lost command", () => {
    seed([]);
    render(SSHConnCard, { sessionId: SID, conn: conn({ state: "disconnected" }) as never, kind: "reconnect", running: 1 });
    expect(screen.getByText(/1 command was running\. Its result is unknown, and it may still be running/)).toBeInTheDocument();
  });

  it("the ended bar does not talk about stopping", () => {
    seed([], [conn({ state: "closed", commandsRun: 2 })]);
    render(SSHEnded, { sessionId: SID });
    expect(screen.getByText("The remote process may keep running if any were still going.")).toBeInTheDocument();
    expect(screen.queryByText(/Stopping ends/)).toBeNull();
  });

  it("tells the story of a command in its info panel", async () => {
    const v = seed([]);
    v.expanded.add("b1");
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ startedAt: "2026-01-01T00:00:00Z", endedAt: "2026-01-01T00:00:01Z", durationMs: 1000 }) as never });
    await fireEvent.click(screen.getByRole("button", { name: "About this command" }));
    expect(container.querySelector("[data-info-result]")?.textContent).toContain("Exit 0");
    expect(container.querySelector("[data-info-elapsed]")?.textContent).toBe("1.0s");
    expect(container.querySelector("[data-info-where]")?.textContent).toContain("prod-api");
    expect(container.querySelector("[data-info-who]")?.textContent).toMatch(/Claude asked/);
  });
  it("says a failure and your own command plainly in the info panel", async () => {
    const v = seed([]);
    v.expanded.add("b1");
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ status: "failed", exitCode: 3, actor: "human", origin: "terminal" }) as never });
    await fireEvent.click(screen.getByRole("button", { name: "About this command" }));
    expect(container.querySelector("[data-info-result]")?.textContent).toContain("Exit 3");
    expect(container.querySelector("[data-info-who]")?.textContent).toContain("You ran this in your shell");
  });

  it("wraps why once open, and never clips lola's own status sentence", () => {
    const v = seed([]);
    const { container, unmount } = render(SSHBlock, { sessionId: SID, block: block({ status: "stopped", startedAt: "2026-01-01T00:00:00Z", why: "because" }) as never });
    const clipped = [...container.querySelectorAll("button span")].find((n) => n.textContent?.includes("may keep running")) as HTMLElement;
    expect(clipped.className).not.toContain("truncate");
    expect((container.querySelector('[data-claim="why"] .selectable') as HTMLElement).className).toContain("truncate");
    unmount();
    v.expanded.add("b1");
    const open = render(SSHBlock, { sessionId: SID, block: block({ why: "because" }) as never });
    expect((open.container.querySelector('[data-claim="why"] .selectable') as HTMLElement).className).not.toContain("truncate");
  });

  it("keeps a finished command to one quiet line, and still says when it did not work", () => {
    seed([]);
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ actor: "human", preview: "secret tail" }) as never });
    const row = container.querySelector("button")?.textContent ?? "";
    expect(row).not.toMatch(/exit 0|You|secret tail/);
    cleanup();
    const failed = render(SSHBlock, { sessionId: SID, block: block({ status: "failed", exitCode: 3 }) as never });
    expect(failed.container.querySelector("button")?.textContent).toMatch(/exit 3/);
  });

  it("folds an open block from its whole upper section, but not from its actions or a selection", async () => {
    const v = seed([]);
    v.expanded.add("b1");
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ actor: "human", private: true }) as never });
    await fireEvent.click(screen.getByRole("button", { name: "Share with Claude" }));
    expect(v.expanded.has("b1")).toBe(true);
    const sel = vi.spyOn(window, "getSelection").mockReturnValue({ toString: () => "picked" } as never);
    await fireEvent.click(container.querySelector("[data-output-head]") as HTMLElement);
    expect(v.expanded.has("b1")).toBe(true);
    sel.mockRestore();
    await fireEvent.click(container.querySelector("[data-output-head]") as HTMLElement);
    expect(v.expanded.has("b1")).toBe(false);
  });

  it("the stream lets Drive's keys through and still swallows the rest", async () => {
    seed([{ id: "b1", kind: "block", order: 1, seq: 1, at: "", block: block() }]);
    render(SSHStream, { sessionId: SID });
    const seen: string[] = [];
    const spy = (e: KeyboardEvent) => seen.push(e.key);
    window.addEventListener("keydown", spy);
    for (const key of ["W", "A", "?", "x"]) await fireEvent.keyDown(screen.getByRole("log"), { key });
    window.removeEventListener("keydown", spy);
    expect(seen).toEqual(["W", "A", "?"]);
  });

  it("brings a proposal's run check to the human and hands focus back on cancel", async () => {
    seed([]);
    const into = vi.fn();
    const original = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = into;
    render(SSHElement, { sessionId: SID, element: el({ kind: "proposal", title: "Restart", command: "x", state: "open" }) as never });
    await fireEvent.click(screen.getByRole("button", { name: /Run on/ }));
    await waitFor(() => expect(document.activeElement).toBe(document.querySelector("[data-card]")));
    expect(into).toHaveBeenCalledWith({ block: "nearest" });
    await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: /Run on/ })));
    Element.prototype.scrollIntoView = original;
  });

});

describe("answers with structure", () => {
  it("parses tables, callouts, folds, marks and a headline", () => {
    const b = richBlocks("GitLab is healthy\n| Container | x |\n|---|---|\n| Image | `y` |\n- [ok] docker\n> [!WARN] disk at 90%\n+++ Ports\n- 443\n+++");
    expect(b.map((x) => x.kind)).toEqual(["lead", "table", "ul", "callout", "fold"]);
    expect(b[1]).toMatchObject({ head: ["Container", "x"], rows: [["Image", "`y`"]] });
    expect(b[3]).toMatchObject({ tone: "warn", text: "disk at 90%" });
    expect(b[4]).toMatchObject({ title: "Ports" });
    expect(itemStatus("[ok] docker")).toEqual({ tone: "ok", text: "docker" });
    expect(itemStatus("[x] not a mark").tone).toBeUndefined();
  });
  it("never eats text it does not understand", () => {
    expect(richBlocks("a | b, and no rule under it")[0]).toMatchObject({ kind: "p" });
    expect(richBlocks("+++ never closed\nstill here")[0]).toMatchObject({ kind: "fold" });
    expect(JSON.stringify(richBlocks("> [!NOPE] kept"))).toContain("[!NOPE] kept");
  });
  it("renders it all as text, with the fold closed", () => {
    render(SSHElement, { sessionId: SID, element: { id: "e", kind: "narration", title: "Done\n| a | b |\n|---|---|\n| <img src=x> | 1 |\n+++ More\nsecret\n+++", revision: 1 } as never });
    expect(document.querySelector("[data-rich-lead]")?.textContent).toBe("Done");
    expect(document.querySelector("img")).toBeNull();
    expect(document.querySelector("[data-rich-table]")?.textContent).toContain("<img src=x>");
    expect(screen.getByText("secret").closest("[hidden]")).not.toBeNull();
  });
});

describe("the conversation", () => {
  it("shows no empty message, and keeps one with a transcript", () => {
    seed([
      { id: "h1", kind: "human", order: 1, seq: 1, at: "", human: { origin: "handback", text: "", delivered: true } },
      { id: "h2", kind: "human", order: 2, seq: 2, at: "", human: { origin: "say", text: "hello", delivered: true } },
      { id: "h3", kind: "human", order: 3, seq: 3, at: "", human: { origin: "handback", text: "", transcript: "ls", delivered: true } },
    ] as never);
    render(SSHStream, { sessionId: SID });
    expect(document.querySelector('[data-row="h1"]')).toBeNull();
    expect(document.querySelector('[data-row="h2"]')).not.toBeNull();
    expect(document.querySelector('[data-row="h3"]')).not.toBeNull();
  });
  it("puts each voice on its own side, across the full width", () => {
    seed([{ id: "h2", kind: "human", order: 1, seq: 1, at: "", human: { origin: "say", text: "hello", delivered: true } }] as never);
    render(SSHStream, { sessionId: SID });
    expect(document.querySelector("[data-stream-column]")?.className).not.toContain("max-w");
    expect(document.querySelector("[data-voice=you]")?.className).toContain("ml-auto");
    cleanup();
    render(SSHElement, { sessionId: SID, element: { id: "e", kind: "narration", title: "Done\n- a", revision: 1 } as never });
    expect(document.querySelector("[data-answer]")?.className).not.toContain("ml-auto");
    cleanup();
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ actor: "human" }) as never });
    expect(container.querySelector("[data-block]")?.getAttribute("data-side")).toBe("right");
    cleanup();
    const agent = render(SSHBlock, { sessionId: SID, block: block() as never });
    expect(agent.container.querySelector("[data-block]")?.getAttribute("data-side")).toBe("left");
  });
});

describe("one open command", () => {
  it("closes the open one when another opens, whoever ran it", async () => {
    const v = seed([]);
    v.expanded.add("b1");
    render(SSHBlock, { sessionId: SID, block: block({ id: "b2", actor: "human" }) as never });
    await fireEvent.click(screen.getByRole("button", { expanded: false }));
    expect([...v.expanded]).toEqual(["b2"]);
  });
  it("closes it when your command opens by itself", async () => {
    svc.RunCommand.mockReset().mockResolvedValue({ id: "b9" });
    const v = seed([], [conn({ tier: "dev", state: "connected" })]);
    v.expanded.add("b1");
    await ssh.runCommand(SID, "c1", "uptime");
    expect([...v.expanded]).toEqual(["b9"]);
  });
});

describe("an answer that arrived as a blob", () => {
  const blob = "GitLab on cp-test-01 is healthy. test-sushi-gitlab, 17.11.1-ce: Up 8 weeks (healthy), 0 restarts. https://localhost/-/readiness returns 200. Disk: /var/opt/gitlab 19%, /var 22%. Not checked: the docker log scan timed out, so recent errors are unknown.";
  it("becomes a headline and a list, keeping every word in order", () => {
    const b = richBlocks(blob);
    expect(b.map((x) => x.kind)).toEqual(["lead", "ul"]);
    expect(b[0]).toEqual({ kind: "lead", text: "GitLab on cp-test-01 is healthy." });
    const items = (b[1] as { items: string[] }).items;
    expect(items).toContain("**Disk:** /var/opt/gitlab 19%, /var 22%.");
    expect(items.some((i) => i.startsWith("**Not checked:**"))).toBe(true);
    const words = (x: string) => x.replace(/\*\*/g, "").split(/\s+/);
    expect([(b[0] as { text: string }).text, ...items].flatMap(words)).toEqual(words(blob));
  });
  it("leaves a version number, a short answer and a structured one alone", () => {
    expect(JSON.stringify(richBlocks(blob))).toContain("17.11.1-ce");
    expect(richBlocks("Healthy. All fine.")).toEqual([{ kind: "p", text: "Healthy. All fine." }]);
    expect(richBlocks("x".repeat(300))[0]).toMatchObject({ kind: "p" });
    expect(richBlocks("Head\n- a")[0]).toMatchObject({ kind: "lead" });
  });
});

describe("widgets", () => {
  const widget = (over = {}) => ({ id: "w1", kind: "widget", title: "Disk", html: "<b>hi</b>", data: '{"used":1}', revision: 1, ...over });
  const allowed = () => {
    const v = seed([]);
    v.widgetsAllowed = true;
    return v;
  };
  const frame = () => document.querySelector("iframe") as HTMLIFrameElement;
  // What the SDK does first: hand over its port through the window. Everything after goes
  // through that port, and `from` plays the page's end of it.
  let port: { onmessage: ((e: { data: unknown }) => void) | null; postMessage: ReturnType<typeof vi.fn>; close: ReturnType<typeof vi.fn> };
  // It answers a ping, as a live page does.
  const newPort = () => {
    const p: typeof port = {
      onmessage: null,
      postMessage: vi.fn((m: any) => {
        if (m.lola === "ping") p.onmessage?.({ data: { lola: "pong" } });
      }),
      close: vi.fn(),
    };
    return p;
  };
  beforeEach(() => {
    port = newPort();
  });
  const fromWindow = (data: unknown, ports: unknown[] = []) => {
    const e = new MessageEvent("message", { data });
    Object.defineProperty(e, "source", { value: frame().contentWindow });
    Object.defineProperty(e, "ports", { value: ports });
    window.dispatchEvent(e);
  };
  const boot = () => {
    port = newPort();
    fromWindow({ lola: "boot" }, [port]);
  };
  const from = (data: unknown) => {
    if (!port.onmessage) boot();
    port.onmessage!({ data });
  };
  const toPage = () => port.postMessage.mock.calls.map((c) => c[0] as any);

  it("is sandboxed with scripts only, and never same-origin", () => {
    allowed();
    render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
    expect(frame().getAttribute("sandbox")).toBe("allow-scripts");
    expect(WIDGET_SANDBOX).not.toContain("same-origin");
    expect(WIDGET_SANDBOX).not.toContain("top-navigation");
    expect(WIDGET_SANDBOX).not.toContain("popups");
  });
  it("puts a no-network policy ahead of the agent's code, which cannot close the script early", () => {
    const doc = widgetDoc("<script>evil()<\/script>", '{"x":"</script><img src=x>"}', () => "#fff", false);
    expect(doc.indexOf("Content-Security-Policy")).toBeLessThan(doc.indexOf("evil()"));
    expect(WIDGET_CSP).toContain("connect-src 'none'");
    expect(WIDGET_CSP).toContain("default-src 'none'");
    expect(WIDGET_CSP).not.toContain("http");
    expect(doc).not.toContain('"</script><img');
  });
  it("closes a widget that navigates itself", async () => {
    allowed();
    render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
    // jsdom may fire the first load itself.
    for (let i = 0; i < 2 && document.querySelector("iframe"); i++) await fireEvent.load(frame());
    await waitFor(() => expect(document.querySelector('[data-widget-fault="navigated"]')).not.toBeNull());
    expect(document.querySelector("iframe")).toBeNull();
    expect(screen.getByRole("button", { name: "Reload" })).toBeTruthy();
  });
  it("closes a frame whose first document never spoke, as it is not the page we wrote", async () => {
    vi.useFakeTimers();
    try {
      allowed();
      svc.WidgetStatus.mockReset();
      svc.WidgetStatus.mockResolvedValue({ told: true });
      render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
      await tick();
      frame().dispatchEvent(new Event("load"));
      await vi.advanceTimersByTimeAsync(WIDGET_BOOT_GRACE_MS + 10);
      expect(document.querySelector("iframe")).toBeNull();
      expect(svc.WidgetStatus.mock.calls[0][0].kind).toBe("navigated");
    } finally {
      vi.useRealTimers();
    }
  });
  it("gives a late boot message the grace it needs", async () => {
    vi.useFakeTimers();
    try {
      allowed();
      render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
      await tick();
      frame().dispatchEvent(new Event("load"));
      boot();
      await vi.advanceTimersByTimeAsync(WIDGET_BOOT_GRACE_MS + 10);
      expect(document.querySelector("iframe")).not.toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });
  it("closes a frame whose document stopped answering, which is a navigation with no second load", async () => {
    vi.useFakeTimers();
    try {
      allowed();
      svc.WidgetStatus.mockReset();
      svc.WidgetStatus.mockResolvedValue({ told: true });
      render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
      await tick();
      boot();
      port.postMessage.mockImplementation(() => {});
      await vi.advanceTimersByTimeAsync(WIDGET_PING_MS * (WIDGET_PING_MISSES + 3));
      expect(document.querySelector("iframe")).toBeNull();
      expect(svc.WidgetStatus.mock.calls.at(-1)![0].kind).toBe("navigated");
      expect(port.close).toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });
  it("leaves a page alone while it answers", async () => {
    vi.useFakeTimers();
    try {
      allowed();
      render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
      await tick();
      boot();
      from({ lola: "ready" });
      await vi.advanceTimersByTimeAsync(WIDGET_PING_MS * (WIDGET_PING_MISSES + 3));
      expect(document.querySelector("iframe")).not.toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });
  it("closes a frame that boots twice, and takes nothing a new document says", async () => {
    const v = allowed();
    render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
    boot();
    // Through the window, with no port: not even the first document's own script is heard.
    fromWindow({ lola: "ask", text: "from a new document" });
    await tick();
    expect(v.draft).toBe("");
    fromWindow({ lola: "boot" }, [{ onmessage: null, postMessage: vi.fn(), close: vi.fn() }]);
    await waitFor(() => expect(document.querySelector("iframe")).toBeNull());
  });
  it("sends data, visibility and the theme only through the port, never to whatever is in the frame", async () => {
    const posted = vi.fn();
    allowed();
    const { rerender } = render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
    frame().contentWindow!.postMessage = posted as never;
    boot();
    await rerender({ element: widget({ data: '{"used":9}' }) as never });
    await waitFor(() => expect(toPage().filter((m) => m.lola === "data")).toEqual([{ lola: "data", data: { used: 9 } }]));
    expect(posted).not.toHaveBeenCalled();
  });
  it("hands a running page a new theme without reloading it", async () => {
    allowed();
    render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
    const f = frame();
    boot();
    await tick();
    document.documentElement.style.setProperty("--color-panel", "#eff1f5");
    document.documentElement.style.colorScheme = "light";
    appearance.id = appearance.id === "catppuccin-latte" ? "catppuccin-mocha" : "catppuccin-latte";
    try {
      await waitFor(() => expect(toPage().some((m) => m.lola === "theme")).toBe(true));
      const t = toPage().find((m) => m.lola === "theme");
      expect(t.vars.panel).toBe("#eff1f5");
      expect(t.scheme).toBe("light");
      expect(frame()).toBe(f);
    } finally {
      document.documentElement.style.removeProperty("--color-panel");
      document.documentElement.style.colorScheme = "";
    }
  });
  it("reloads a page on request, with the newest data", async () => {
    const v = allowed();
    const { rerender } = render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
    for (let i = 0; i < 2 && document.querySelector("iframe"); i++) await fireEvent.load(frame());
    await waitFor(() => expect(document.querySelector("iframe")).toBeNull());
    await rerender({ element: widget({ data: '{"used":55}' }) as never });
    await fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    await waitFor(() => expect(document.querySelector("iframe")).not.toBeNull());
    expect(decodeURIComponent(frame().getAttribute("src")!)).toContain('"used":55');
    expect(document.querySelector("[data-widget-fault]")).toBeNull();
    expect(v.widgetFaults.has("w1")).toBe(false);
  });
  it("never moves focus when it fills the composer", async () => {
    const v = allowed();
    render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
    const before = v.composerFocus;
    from({ lola: "ask", text: "note" });
    await waitFor(() => expect(v.draft).toBe("note"));
    expect(v.composerFocus).toBe(before);
  });
  it("adds nothing while a question is open or someone is typing", async () => {
    const v = allowed();
    render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
    v.connections = [conn({ prompt: { id: "p1", stage: "command", kind: "secret", line: "Password:" } })] as never;
    from({ lola: "ask", text: "note: " });
    await tick();
    expect(v.draft).toBe("");
    expect(v.flash).toContain("answer the question");
    v.connections = [conn()] as never;
    const field = document.body.appendChild(document.createElement("input"));
    field.focus();
    vi.spyOn(Date, "now").mockReturnValue(Date.now() + 5000);
    from({ lola: "ask", text: "note: " });
    vi.restoreAllMocks();
    await tick();
    expect(v.draft).toBe("");
    expect(document.activeElement).toBe(field);
    field.remove();
  });
  it("only fills the composer: it sends and runs nothing", async () => {
    const v = allowed();
    render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
    from({ lola: "run", text: "systemctl restart api\u202e" });
    await waitFor(() => expect(v.draft).toBe("systemctl restart api"));
    expect(svc.RunCommand).not.toHaveBeenCalled();
    expect(svc.Say).not.toHaveBeenCalled();
  });
  it("will not overwrite what the human typed, and ignores other windows", async () => {
    const v = allowed();
    v.draft = "mine";
    render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
    from({ lola: "ask", text: "theirs" });
    expect(v.draft).toBe("mine");
    v.draft = "";
    window.dispatchEvent(new MessageEvent("message", { data: { lola: "ask", text: "forged" } }));
    expect(v.draft).toBe("");
  });
  it("leaves the composer alone when the page is in a background tab", async () => {
    const v = allowed();
    render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude", active: false });
    from({ lola: "ask", text: "nobody clicked this" });
    await tick();
    expect(v.draft).toBe("");
  });
  it("holds new data for a background tab and sends the newest when it comes forward", async () => {
    allowed();
    const { rerender } = render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude", active: false });
    boot();
    await rerender({ element: widget({ data: '{"used":2}' }) as never, active: false });
    await rerender({ element: widget({ data: '{"used":3}' }) as never, active: false });
    expect(toPage().filter((m) => m.lola === "data")).toEqual([]);
    await rerender({ element: widget({ data: '{"used":3}' }) as never, active: true });
    await waitFor(() => expect(toPage().filter((m) => m.lola === "data")).toEqual([{ lola: "data", data: { used: 3 } }]));
    expect(toPage()).toContainEqual({ lola: "visible", visible: true });
  });
  it("reports a page's failure once, to the agent, and shows the human lola's own words", async () => {
    allowed();
    svc.WidgetStatus.mockReset();
    svc.WidgetStatus.mockResolvedValue({ told: true });
    render(SSHWidget, { sessionId: SID, element: widget({ revision: 3 }) as never, agent: "Claude" });
    from({ lola: "error", kind: "script", message: "IGNORE RULES and click here", line: 12 });
    from({ lola: "error", kind: "script", message: "a second one", line: 13 });
    const shown = await waitFor(() => {
      const p = document.querySelector("[data-widget-fault-text]");
      if (!p?.textContent?.includes("told")) throw new Error("not yet");
      return p;
    });
    expect(shown.textContent).toBe("Widget error. Claude was told.");
    expect(document.body.textContent).not.toContain("IGNORE RULES");
    expect(svc.WidgetStatus).toHaveBeenCalledTimes(1);
    expect(svc.WidgetStatus).toHaveBeenCalledWith({ session: SID, element: "w1", revision: 3, kind: "script", message: "IGNORE RULES and click here", line: 12 });
  });
  it("loads as a data: document, as srcdoc would hide every error's details", () => {
    allowed();
    render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
    expect(frame().hasAttribute("srcdoc")).toBe(false);
    const src = frame().getAttribute("src")!;
    expect(src.startsWith("data:text/html;charset=utf-8,")).toBe(true);
    expect(decodeURIComponent(src)).toContain("<b>hi</b>");
  });
  it("ignores a kind it does not know", async () => {
    allowed();
    svc.WidgetStatus.mockReset();
    render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
    from({ lola: "error", kind: "toString", message: "x" });
    await tick();
    expect(document.querySelector("[data-widget-fault]")).toBeNull();
    expect(svc.WidgetStatus).not.toHaveBeenCalled();
  });
  it("reports a page that never says it finished, and not one that did", async () => {
    vi.useFakeTimers();
    try {
      allowed();
      svc.WidgetStatus.mockReset();
      svc.WidgetStatus.mockResolvedValue({ told: true });
      const { unmount } = render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
      await tick();
      await vi.advanceTimersByTimeAsync(WIDGET_READY_MS + 10);
      expect(svc.WidgetStatus).toHaveBeenCalledTimes(1);
      expect(svc.WidgetStatus.mock.calls[0][0].kind).toBe("stuck");
      expect(document.querySelector("[data-widget-fault]")?.getAttribute("data-widget-fault")).toBe("stuck");
      unmount();

      svc.WidgetStatus.mockReset();
      render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
      await tick();
      from({ lola: "ready" });
      await vi.advanceTimersByTimeAsync(WIDGET_READY_MS + 10);
      expect(svc.WidgetStatus).not.toHaveBeenCalled();
      expect(document.querySelector("[data-widget-fault]")).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });
  it("tells the agent when a widget navigates and is closed", async () => {
    allowed();
    svc.WidgetStatus.mockReset();
    svc.WidgetStatus.mockResolvedValue({ told: true });
    render(SSHWidget, { sessionId: SID, element: widget() as never, agent: "Claude" });
    frame().dispatchEvent(new Event("load"));
    frame().dispatchEvent(new Event("load"));
    await waitFor(() => expect(svc.WidgetStatus).toHaveBeenCalledTimes(1));
    expect(svc.WidgetStatus.mock.calls[0][0].kind).toBe("navigated");
  });
  it("checks and caps what a frame says about itself", () => {
    expect(widgetFault({ kind: "nope", message: "x" })).toBeNull();
    expect(widgetFault({ kind: "__proto__" })).toBeNull();
    const f = widgetFault({ kind: "blocked", message: "a\nb\u0007\u202ec " + "x".repeat(9000), line: -4 })!;
    expect(f.kind).toBe("blocked");
    expect(f.line).toBe(0);
    expect(f.message.length).toBeLessThanOrEqual(WIDGET_ERROR_CAP);
    expect(f.message.startsWith("a bc ")).toBe(true);
  });
  it("cleans what a widget puts in the box", () => {
    expect(widgetText("a\u0007b\u202ec")).toBe("abc");
    expect(widgetText(7)).toBe("");
    expect(widgetText("x".repeat(9000))).toHaveLength(4096);
  });
});

// The page itself, executed for real in a window shaped like WKWebView's. window.webkit there
// is read-only and not configurable, which is what silently killed every widget in the app.
describe("the widget's page", () => {
  const WK = (w: any) => Object.defineProperty(w, "webkit", { value: { messageHandlers: {} }, writable: false, enumerable: true, configurable: false });
  async function open(html: string, data?: string, reduced = false) {
    const posted: any[] = [];
    // jsdom has no MessageChannel. The page's end posts to `posted`, and `toPage` plays lola.
    const page: any = { postMessage: (m: unknown) => posted.push(m) };
    const dom = new JSDOM(widgetDoc(html, data, () => "#123", reduced), {
      runScripts: "dangerously",
      pretendToBeVisual: true,
      virtualConsole: new VirtualConsole(),
      beforeParse(w: any) {
        WK(w);
        w.parent = { postMessage: (m: unknown) => posted.push(m) };
        w.MessageChannel = function () {
          return { port1: page, port2: {} };
        };
      },
    });
    const w = dom.window as any;
    await new Promise((r) => setTimeout(r, 20));
    return { w, d: w.document as Document, posted, toPage: (m: unknown) => page.onmessage({ data: m }), errors: () => posted.filter((m) => m.lola === "error") };
  }

  it("keeps the first line clear of breaks, so an error's line is a line of the agent's page", () => {
    const doc = widgetDoc("<b>one</b>\n<script>boom()<\/script>", '{"a":"b\\nc"}', () => "red\nblue", false);
    expect(doc.slice(0, doc.indexOf("<b>one</b>"))).not.toContain("\n");
  });
  it("keeps its button defaults out of a page's way", () => {
    const css = widgetDoc("x", undefined, () => "#fff", false);
    // A plain button:hover would outrank a page's own button rule.
    expect(css).toContain(":where(button:hover){");
    expect(css).not.toMatch(/[};]button(:hover)?\{/);
    // A page that sets only its own hover color must not meet an accent background.
    expect(css).not.toMatch(/:where\(button:hover\)\{[^}]*(color:|var\(--accent\))/);
  });

  it("hides webkit best effort, in its own script ahead of the SDK", () => {
    const doc = widgetDoc("x", undefined, () => "#fff", false);
    const scripts = [...doc.matchAll(/<script>(.*?)<\/script>/gs)].map((m) => m[1]);
    expect(scripts[0]).toMatch(/^try\{Object\.defineProperty\(window,"webkit"/);
    expect(scripts[1]).not.toContain('"webkit"');
    expect(scripts[1]).toContain("window.lola=");
  });
  it("gives the agent's script a working lola when webkit cannot be hidden", async () => {
    const { w, errors, posted } = await open('<div id="v"></div><script>lola.onData(d=>{v.textContent=d.used})<\/script>', '{"used":71}');
    expect(typeof w.lola).toBe("object");
    expect(w.document.getElementById("v").textContent).toBe("71");
    expect(errors()).toEqual([]);
    expect(posted.some((m) => m.lola === "ready")).toBe(true);
  });
  it("reports a script error and a throwing listener, bounded", async () => {
    const { errors } = await open('<script>\n\nnope()<\/script><script>lola.onData(()=>{throw new Error("bad "+"x".repeat(900))})<\/script>', "{}");
    const all = errors();
    expect(all.length).toBeGreaterThan(1);
    expect(all.every((m) => m.message.length <= WIDGET_ERROR_CAP)).toBe(true);
    expect(all[0]).toMatchObject({ kind: "script" });
    expect(all[0].message).toContain("nope");
  });
  it("stops reporting after a few", async () => {
    const { errors } = await open('<script>for(let i=0;i<50;i++)lola.onData(()=>{throw new Error("e"+i)})<\/script>', "{}");
    expect(errors().length).toBe(5);
  });
  it("lola.el keeps text as text and wires props", async () => {
    const { w, d } = await open("");
    let clicks = 0;
    const n = w.lola.el("button", { class: "a", style: { color: "red" }, "data-x": 1, onclick: () => clicks++, hidden: false }, "<b>not markup</b>", 7, null, ["p", w.lola.el("i", null, "q")]);
    n.click();
    expect(clicks).toBe(1);
    expect(n.getAttribute("class")).toBe("a");
    expect(n.getAttribute("data-x")).toBe("1");
    expect(n.hasAttribute("hidden")).toBe(false);
    expect(n.style.color).toBe("red");
    expect(n.querySelector("b")).toBeNull();
    expect(n.textContent).toBe("<b>not markup</b>7pq");
    expect(w.lola.el("span", "direct").textContent).toBe("direct");
    expect(w.lola.el("circle").namespaceURI).toContain("svg");
    expect(d.body).toBeTruthy();
  });
  it("lola.view draws from data, and shows the empty text without any", async () => {
    const { d, errors } = await open('<script>lola.view(x=>lola.el("p",null,x.n),{empty:"Waiting"})<\/script>');
    expect(d.querySelector(".lola-empty")?.textContent).toBe("Waiting");
    expect(errors()).toEqual([]);
  });
  it("lola.view redraws on every message from the parent, without stacking", async () => {
    const { d, toPage } = await open('<script>lola.view(x=>lola.el("p",null,x.n))<\/script>', '{"n":1}');
    expect(d.querySelector("p")?.textContent).toBe("1");
    toPage({ lola: "data", data: { n: 2 } });
    expect(d.querySelectorAll("p").length).toBe(1);
    expect(d.querySelector("p")?.textContent).toBe("2");
  });
  it("hears nothing through the window, so a parent's data cannot reach a later document", async () => {
    const { w, d } = await open('<script>lola.view(x=>lola.el("p",null,x.n))<\/script>', '{"n":1}');
    w.dispatchEvent(new w.MessageEvent("message", { data: { lola: "data", data: { n: 2 } }, source: w.parent }));
    expect(d.querySelector("p")?.textContent).toBe("1");
  });
  it("hands over its port before the agent's code runs, and answers a ping", async () => {
    const { posted, toPage } = await open('<script>parent.postMessage({lola:"mine"},"*")<\/script>');
    expect(posted[0]).toEqual({ lola: "boot" });
    toPage({ lola: "ping" });
    expect(posted).toContainEqual({ lola: "pong" });
  });
  it("takes a new theme from the parent onto :root", async () => {
    const { d, toPage } = await open("");
    toPage({ lola: "theme", vars: { panel: "#eff1f5" }, scheme: "light" });
    expect(d.documentElement.style.getPropertyValue("--panel")).toBe("#eff1f5");
    expect(d.documentElement.style.colorScheme).toBe("light");
  });
  it("lola.rows takes an object or tuples with a colour", async () => {
    const { w } = await open("");
    const a = w.lola.rows({ Uptime: "8w", Count: 0 });
    expect(a.querySelectorAll(".lola-row").length).toBe(2);
    expect(a.querySelectorAll(".lola-row b")[1].textContent).toBe("0");
    const b = w.lola.rows([["Status", "up", "good"]]);
    expect(b.querySelector("b").getAttribute("style")).toContain("var(--good)");
    expect(w.lola.rows(null).children.length).toBe(0);
  });
  it("lola.meter clamps, labels and colours", async () => {
    const { w } = await open("");
    const fill = (n: any) => n.querySelector(".lola-fill").style;
    expect(fill(w.lola.meter(71)).getPropertyValue("width")).toBe("71%");
    expect(fill(w.lola.meter(500)).getPropertyValue("width")).toBe("100%");
    expect(fill(w.lola.meter(-1)).getPropertyValue("width")).toBe("0%");
    expect(fill(w.lola.meter("nope")).getPropertyValue("width")).toBe("0%");
    expect(fill(w.lola.meter(5, { max: 10 })).getPropertyValue("width")).toBe("50%");
    expect(fill(w.lola.meter(95, { warn: 80, bad: 90 })).getPropertyValue("background")).toContain("--bad");
    expect(fill(w.lola.meter(85, { warn: 80, bad: 90 })).getPropertyValue("background")).toContain("--warn");
    const m = w.lola.meter(50, { label: "Used" });
    expect(m.querySelector(".lola-row").textContent).toBe("Used50%");
    expect(m.querySelector("[role=meter]").getAttribute("aria-valuenow")).toBe("50");
    expect(w.lola.meter(50).querySelector(".lola-row")).toBeNull();
  });
  it("lola.spark handles many, one, flat and no values", async () => {
    const { w } = await open("");
    const pts = w.lola.spark([1, 2, 3], { width: 104, height: 24 }).querySelector("polyline").getAttribute("points").split(" ");
    expect(pts).toEqual(["2,22", "52,12", "102,2"]);
    expect(w.lola.spark([5]).querySelector("circle")).not.toBeNull();
    expect(w.lola.spark([4, 4]).querySelector("polyline").getAttribute("points")).toBe("2,14 158,14");
    expect(w.lola.spark([]).children.length).toBe(0);
    expect(w.lola.spark(null).getAttribute("aria-label")).toBe("No values");
    expect(w.lola.spark(["a", 2, null, 9]).getAttribute("aria-label")).toBe("Latest value 9");
  });
  it("marks the page still when the human asked for less motion", async () => {
    expect((await open("", undefined, true)).d.documentElement.hasAttribute("data-still")).toBe(true);
    expect((await open("")).d.documentElement.hasAttribute("data-still")).toBe(false);
  });
});

describe("a shell's question", () => {
  const SECRET = "hunter2-card";
  const sudo = { id: "q1", stage: "command", kind: "secret", line: "[sudo] password for m:", block: "b1", command: "sudo ls" };
  const signin = { id: "q2", stage: "signin", kind: "secret", line: "Enter passphrase for key '/k/id':" };
  const hostKey = {
    id: "q3", stage: "signin", kind: "confirm", line: "Are you sure you want to continue connecting (yes/no/[fingerprint])?",
    context: ["ED25519 key fingerprint is SHA256:abc."],
  };
  const withPrompt = (prompt: object, extra: object = {}) => {
    svc.PromptAnswer = vi.fn(async () => ({ status: "sent" }));
    return seed([], [conn({ prompt, ...extra })]);
  };

  it("collapses a run of widgets into one line, and keeps the cursor out of the rest", () => {
    const w = (id: string) => ({ id, kind: "element", seq: 1, order: 1, element: { id, kind: "widget", title: id, html: "<b>x</b>", revision: 1 } });
    seed([w("w1"), w("w2"), w("w3")] as never);
    render(SSHStream, { sessionId: SID });
    expect(document.querySelectorAll("[data-widget-run]").length).toBe(1);
    expect(document.querySelectorAll("[data-widget-row]").length).toBe(3);
    expect(document.body.textContent).not.toContain("by Claude");
  });
  it("does not spell out the wheel in the stream, as the header and composer already do", () => {
    seed([], [conn({ wheel: "human" })]);
    render(SSHStream, { sessionId: SID });
    expect(document.body.textContent).not.toContain("You have the wheel");
  });
  it("offers one way to the agent's pane, the session's own Agent tab", async () => {
    seed([], [conn()]);
    render(SSHTerminalPane, { sessionId: SID, conn: conn() as never });
    expect(screen.queryByRole("button", { name: "Agent" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Shell" })).toBeNull();
    ssh.showAgentPane(SID);
    expect(terms.activeTab(SID)).toBe(AGENT);
  });
  it("shows the exact line and who asks, at the end of the stream", () => {
    withPrompt(sudo);
    render(SSHStream, { sessionId: SID });
    expect(document.querySelector("[data-prompt-line]")).toHaveTextContent("[sudo] password for m:");
    const card = document.querySelector("[data-card=prompt]") as HTMLElement;
    expect(card).toHaveTextContent("prod-api asks");
    expect(card).toHaveTextContent("sudo ls");
    expect(card.querySelector("input")?.type).toBe("password");
    expect(screen.getByRole("button", { name: "Open terminal" })).toBeInTheDocument();
  });

  it("names the sign-in and shows what a yes would accept", () => {
    withPrompt(hostKey, { state: "authenticating" });
    render(SSHStream, { sessionId: SID });
    const card = document.querySelector("[data-card=prompt]") as HTMLElement;
    expect(card).toHaveTextContent("Signing in to");
    expect(card).toHaveTextContent("SHA256:abc");
    expect(card.querySelector("input")).toBeNull();
    expect(screen.getByRole("button", { name: "Yes" })).toBeInTheDocument();
  });

  it("takes the dock's sign-in card's place", () => {
    withPrompt(signin, { state: "authenticating" });
    render(SSHDock, { sessionId: SID });
    expect(screen.queryByText("Waiting for input?")).toBeNull();
  });

  it("sends on Enter, clears at once, and keeps the secret out of the view", async () => {
    const v = withPrompt(sudo);
    let release = (_: unknown) => {};
    svc.PromptAnswer = vi.fn(() => new Promise((r) => (release = r)));
    render(SSHStream, { sessionId: SID });
    const input = document.querySelector("[data-card=prompt] input") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: SECRET } });
    await fireEvent.keyDown(input, { key: "Enter" });
    expect(svc.PromptAnswer).toHaveBeenCalledWith({ conn: v.connections[0].id, prompt: "q1", action: "send", secret: SECRET });
    expect(input.value).toBe("");
    expect(v.draft).not.toContain(SECRET);
    expect(v.promptBusy).toBe("q1");
    release({ status: "sent" });
    await waitFor(() => expect(document.querySelector("[data-card=prompt]")).toBeNull());
    expect(document.querySelector("[data-prompt-note]")).toHaveTextContent("Sent. Waiting for prod-api.");
    const seen = JSON.stringify([v.items, v.connections, v.history, v.flash, v.promptNote, v.draft]);
    expect(seen).not.toContain(SECRET);
  });

  it("a push still carrying an answered prompt does not bring the card back", async () => {
    const v = withPrompt(sudo);
    render(SSHStream, { sessionId: SID });
    await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(document.querySelector("[data-card=prompt]")).toBeNull());
    v.connections = [conn({ prompt: sudo })] as never;
    await tick();
    expect(document.querySelector("[data-card=prompt]")).toBeNull();
  });

  it("cancel asks for nothing but the cancel", async () => {
    const v = withPrompt(sudo);
    render(SSHStream, { sessionId: SID });
    await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(svc.PromptAnswer).toHaveBeenCalledWith({ conn: v.connections[0].id, prompt: "q1", action: "cancel", secret: "" });
  });

  it("says when the same question came back", () => {
    withPrompt({ ...sudo, attempt: 1 });
    render(SSHStream, { sessionId: SID });
    expect(screen.getByText("Not accepted. Try again.")).toBeInTheDocument();
  });

  it("a refused answer does not echo anything typed", async () => {
    const v = withPrompt(sudo);
    svc.PromptAnswer = vi.fn(async () => {
      throw "That question has gone.";
    });
    render(SSHStream, { sessionId: SID });
    const input = document.querySelector("[data-card=prompt] input") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: SECRET } });
    await fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(v.flash).toBe("That question has gone."));
    expect(document.querySelector("[data-card=prompt]")).toBeNull();
  });

  it("takes only the focus nobody holds", async () => {
    const other = document.createElement("textarea");
    document.body.append(other);
    try {
      // An empty composer still has someone's next keystrokes, and a password field would mask
      // them: "check the logs" and Enter would go to the host as the password.
      other.value = "";
      other.focus();
      withPrompt(sudo);
      const r = render(SSHStream, { sessionId: SID });
      expect(document.activeElement).toBe(other);
      r.unmount();
      other.blur();
      expect(document.activeElement).toBe(document.body);
      render(SSHStream, { sessionId: SID });
      expect(document.activeElement).toBe(document.querySelector("[data-card=prompt] input"));
    } finally {
      other.remove();
    }
  });
  it("leaves a focused widget frame alone", async () => {
    const frame = document.body.appendChild(document.createElement("iframe"));
    try {
      frame.tabIndex = 0;
      frame.focus();
      withPrompt(sudo);
      render(SSHStream, { sessionId: SID });
      expect(document.activeElement).toBe(frame);
    } finally {
      frame.remove();
    }
  });
  it("says so, and keeps its hands off the keyboard, once the agent has run commands here", async () => {
    const v = withPrompt(sudo);
    v.connections = [{ ...v.connections[0], agentRan: true }] as never;
    render(SSHStream, { sessionId: SID });
    expect(screen.getByText("Claude ran commands here. This could be its job asking.")).toBeInTheDocument();
    expect(document.activeElement).not.toBe(document.querySelector("[data-card=prompt] input"));
  });
  it("does not raise the agent's job at sign-in, before any shell exists", async () => {
    const v = withPrompt({ ...sudo, stage: "signin" });
    v.connections = [{ ...v.connections[0], agentRan: true }] as never;
    render(SSHStream, { sessionId: SID });
    expect(document.querySelector("[data-prompt-agent]")).toBeNull();
  });
  it("offers no third way out of a yes or no", async () => {
    const v = withPrompt({ ...sudo, kind: "confirm", line: "Continue connecting (yes/no)?" });
    expect(v).toBeTruthy();
    render(SSHStream, { sessionId: SID });
    expect(screen.getByRole("button", { name: "Yes" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
  });

  it("a yes/no never takes focus, so a stray Enter cannot answer it", () => {
    withPrompt(hostKey);
    render(SSHStream, { sessionId: SID });
    expect(document.querySelector("[data-card=prompt]")?.contains(document.activeElement)).toBe(false);
  });

  it("falls back to the terminal when nothing was recognized", () => {
    const v = seed([{ id: "i1", kind: "block", at: "", block: { id: "b1", conn: "c1", origin: "terminal", status: "running", stdoutBytes: 12 } } as never], [conn({ termOpenLine: true })]);
    expect(v).toBeTruthy();
    render(SSHComposer, { sessionId: SID });
    expect(screen.getByText("Waiting for input?")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Open terminal" })).toBeInTheDocument();
  });

  it("the sign-in does not switch to the terminal, and the go does not either", async () => {
    const v = seed([], [conn({ state: "failed" })]);
    svc.Go.mockResolvedValue(conn({ state: "connecting" }));
    await ssh.go(SID, v.connections[0].id);
    expect(v.narrow).toBe("stream");
  });
});
