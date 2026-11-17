// The setup shared by the ssh component tests, which are one file per area so that
// separate pieces of work never share a file. Import this BEFORE anything that reaches
// the bindings, because the mocks below register when this module is evaluated, and call
// useSSHKit() once at the top of the test file.

import { beforeEach, afterEach, vi } from "vitest";
import { cleanup } from "@testing-library/svelte";

// A daemon that knows no talk commands, unless a test says otherwise: the composer falls back to
// ssh.say and the session has no capabilities, which is what these tests are about.
const talkSvc = vi.hoisted(() => {
  const unsupported = () => Promise.reject(new Error("unknown cmd"));
  return { State: vi.fn(unsupported), Say: vi.fn(unsupported), Stop: vi.fn(unsupported), Choose: vi.fn(unsupported), QueueRelease: vi.fn(unsupported), Feed: vi.fn(unsupported), FeedOutput: vi.fn(unsupported), Ended: vi.fn(unsupported) };
});
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
  TalkService: talkSvc,
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

export { svc, talkSvc };

export const { ssh } = await import("$lib/ssh.svelte");
export const { store } = await import("$lib/store.svelte");
export const { default: SSHBlock } = await import("./SSHBlock.svelte");
export const { default: SSHTerminalPane } = await import("./SSHTerminalPane.svelte");
export const { default: SSHElement } = await import("./SSHElement.svelte");
export const { default: SSHWidget } = await import("./SSHWidget.svelte");
export const { default: SSHEvidence } = await import("./SSHEvidence.svelte");
export const { default: SSHApproval } = await import("./SSHApproval.svelte");
export const { default: SSHGoCard } = await import("./SSHGoCard.svelte");
export const { default: SSHConnCard } = await import("./SSHConnCard.svelte");
export const { default: SSHEnded } = await import("./SSHEnded.svelte");
export const { default: SSHHandback } = await import("./SSHHandback.svelte");
export const { default: Timeline } = await import("../session/Timeline.svelte");
export const { default: SSHTierTag } = await import("./SSHTierTag.svelte");
export const { default: Tray } = await import("../session/Tray.svelte");
export const { default: HostBar } = await import("./HostBar.svelte");
export const { default: Composer } = await import("../session/Composer.svelte");
export const { default: Workspace } = await import("../session/Workspace.svelte");
export const { default: SSHChip } = await import("./SSHChip.svelte");
export const { default: SSHHostSettings } = await import("./SSHHostSettings.svelte");
export const { default: NewSSHSession } = await import("./NewSSHSession.svelte");
export const { ConfigService } = await import("@bindings/desktop");

let n = 0;
export let SID = "";

export const conn = (o: Record<string, unknown> = {}) => ({
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
export const block = (o: Record<string, unknown> = {}) => ({
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
export const el = (o: Record<string, unknown> = {}) => ({ id: "e1", kind: "narration", title: "hello", revision: 1, ...o });

export function useSession(s: Record<string, unknown> = {}) {
  store.sessions = [{ id: SID, title: "Debug", agent: "claude", agentState: "working", ...s }] as never;
}

export function seed(items: unknown[], conns: unknown[] = [conn()]) {
  const v = ssh.view(SID);
  v.items = items as never;
  v.connections = conns as never;
  v.loaded = true;
  return v;
}

/** Registers the per-test reset on the calling file. */
export function useSSHKit() {
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
}
