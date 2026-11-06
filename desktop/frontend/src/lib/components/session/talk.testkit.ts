// The setup shared by the composer and tray tests. Import this BEFORE anything that reaches the
// bindings, because the mock registers when this module is evaluated.
import { vi } from "vitest";

const talkSvc = vi.hoisted(() => ({
  State: vi.fn(),
  Say: vi.fn(),
  Stop: vi.fn(),
  Choose: vi.fn(),
  QueueRelease: vi.fn(),
  Feed: vi.fn(),
  FeedOutput: vi.fn(),
  Ended: vi.fn(),
}));
const sshSvc = vi.hoisted(() => ({
  Timeline: vi.fn(),
  Say: vi.fn(),
  Recall: vi.fn(),
  RunCommand: vi.fn(),
  PromptAnswer: vi.fn(),
  Approve: vi.fn(),
  Go: vi.fn(),
  Deny: vi.fn(),
  TakeWheel: vi.fn(),
  Handback: vi.fn(),
  HandbackPreview: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  TalkService: talkSvc,
  SSHService: sshSvc,
  DaemonService: { Alive: vi.fn().mockResolvedValue(true) },
  ConfigService: {},
  TermService: { Shells: vi.fn().mockResolvedValue([]) },
}));

export { talkSvc, sshSvc };
export const { ssh } = await import("$lib/ssh.svelte");
export const { store } = await import("$lib/store.svelte");
export const { talk } = await import("$lib/talk.svelte");
export const { terms, AGENT } = await import("$lib/terms.svelte");

const caps = { feed: true, send: true, stop: true, cards: true };

let n = 0;
/** A fresh session id per test, because the stores keep one view per id for the whole run. */
export function freshSession(o: Record<string, unknown> = {}): string {
  const id = `talk${++n}`;
  store.alive = true;
  store.sessions = [{ id, agent: "claude", agentState: "idle", status: "idle", title: "T", ...o }] as never;
  talkSvc.State.mockResolvedValue({ session: id, caps, composer: "resting", feedSeq: 0, health: "ok" });
  talkSvc.Say.mockResolvedValue({ item: "h", delivered: true });
  talkSvc.Stop.mockResolvedValue({ stopped: true });
  talkSvc.Choose.mockResolvedValue({ sent: true });
  sshSvc.Timeline.mockResolvedValue({ session: id, connections: null, items: [], lastSeq: 0 });
  sshSvc.Recall.mockResolvedValue({ recalled: true });
  sshSvc.RunCommand.mockResolvedValue({ id: "b1" });
  return id;
}

/** Puts the daemon's answer in place, as the poll would. */
export async function setTalk(id: string, over: Record<string, unknown> = {}) {
  talkSvc.State.mockResolvedValue({ session: id, caps, composer: "resting", feedSeq: 0, health: "ok", ...over });
  await talk.refresh(id);
}

export const conn = (id: string, o: Record<string, unknown> = {}) =>
  ({ id, host: id, alias: id, tier: "dev", state: "connected", wheel: "agent", user: "deploy", cwd: "/srv", sandboxed: true, ...o }) as never;

export const permission = (o: Record<string, unknown> = {}) => ({
  kind: "permission",
  hash: "hash-1",
  lines: ["Bash command", 'psql "$TEST_DB" -f migrations/0042_tenants.sql', "Do you want to proceed?"],
  choices: [
    { key: "1", label: "Yes" },
    { key: "2", label: "Yes, and always allow access to migrations/ from this project" },
    { key: "3", label: "Yes, and switch to auto mode" },
    { key: "4", label: "No" },
  ],
  ...o,
});
