// What the timeline's tests share: the bindings, and items shaped like the wire's. Import this
// before anything that reaches a binding, as the mocks register when this module is evaluated.
import { vi } from "vitest";

const svc = vi.hoisted(() => ({
  Timeline: vi.fn(),
  Output: vi.fn(),
  Stop: vi.fn(),
  Feed: vi.fn(),
  FeedOutput: vi.fn(),
  State: vi.fn(),
  CaptureMany: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  SSHService: { Timeline: svc.Timeline, Output: svc.Output, Stop: svc.Stop, WidgetStatus: vi.fn() },
  TalkService: { Feed: svc.Feed, FeedOutput: svc.FeedOutput, State: svc.State },
  DaemonService: { Alive: vi.fn().mockResolvedValue(true), RestoreCheckpoint: vi.fn(), ForkCheckpoint: vi.fn() },
  ConfigService: {},
  TermService: { Shells: vi.fn().mockResolvedValue([]), CaptureMany: svc.CaptureMany },
}));

export { svc };

export const tool = (id: string, category: string, summary: string, more: Record<string, unknown> = {}, order = 1) => ({
  id,
  kind: "tool",
  order,
  seq: order,
  at: new Date(1_700_000_000_000 + order * 1000).toISOString(),
  tool: { name: category === "read" ? "Read" : category === "edit" ? "Edit" : category === "agent" ? "Task" : "Bash", category, summary, status: "ok", durationMs: 100, ...more },
});

export const text = (id: string, body: string, order = 1, more: Record<string, unknown> = {}) => ({
  id,
  kind: "text",
  order,
  seq: order,
  at: new Date(1_700_000_000_000 + order * 1000).toISOString(),
  text: body,
  ...more,
});

export const user = (id: string, body: string, order = 1, more: Record<string, unknown> = {}) => ({
  id,
  kind: "user",
  order,
  seq: order,
  at: new Date(1_700_000_000_000 + order * 1000).toISOString(),
  text: body,
  ...more,
});

export const human = (id: string, body: string, order = 1, more: Record<string, unknown> = {}) => ({
  id,
  kind: "human",
  order,
  seq: order,
  at: new Date(1_700_000_000_000 + order * 1000).toISOString(),
  human: { origin: "say", text: body, delivered: true, ...more },
});

export const system = (id: string, kind: string, words: string, order = 1, more: Record<string, unknown> = {}) => ({
  id,
  kind: "system",
  order,
  seq: order,
  at: new Date(1_700_000_000_000 + order * 1000).toISOString(),
  system: { kind, text: words, ...more },
});

export const plate = (id: string, order = 1, more: Record<string, unknown> = {}) => ({
  id,
  kind: "block",
  order,
  seq: order,
  at: new Date(1_700_000_000_000 + order * 1000).toISOString(),
  block: { id, conn: "c1", host: "prod-api", tier: "production", command: "systemctl status api", actor: "agent", status: "succeeded", exitCode: 0, durationMs: 400, approval: { granted: true }, ...more },
});
