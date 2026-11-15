// Every forwarded route's exact request, checked against what the daemon keeps.
// `internal/remote/fields.go` drops a field its command does not list without an
// error, so a misplaced field only shows as a handler acting on a zero value.
import { beforeEach, describe, expect, it, vi } from "vitest";

const native = vi.hoisted(() => ({ native: { readClipboardImage: vi.fn() } }));
vi.mock("../bridge", () => ({ bridge: { request: vi.fn(), alive: vi.fn(() => true) } }));
vi.mock("../../native", () => native);

import { bridge } from "../bridge";
import * as Daemon from "./daemon";
import * as Hub from "./hub";
import type { Route } from "./routes";
import * as SSH from "./ssh";
import * as Talk from "./talk";
import { clearAttachmentCache } from "./talkimage";
import * as Term from "./term";
import { expectKeptByDaemon, remoteFieldRules } from "./testkit";

const request = vi.mocked(bridge.request);
const rules = remoteFieldRules();

type Fields = Record<string, unknown> | undefined;

interface Row {
  call: () => Promise<unknown>;
  /** The fields of the route's first request carrying its cmd. */
  sends: Fields;
  /** Answers the daemon gives, by cmd, for routes that read before they send. */
  answers?: Record<string, unknown>;
}

const SESSIONS = { sessions: [{ id: "s1", worktree: "/w/s1" }] };
const IMAGE = "/w/s1/.lola/images/a.png";

const SERVICES: Record<string, { mod: Record<string, unknown>; routes: Record<string, Route> }> = {
  DaemonService: { mod: Daemon, routes: Daemon.ROUTES },
  TalkService: { mod: Talk, routes: Talk.ROUTES },
  SSHService: { mod: SSH, routes: SSH.ROUTES },
  HubService: { mod: Hub, routes: Hub.ROUTES },
  TermService: { mod: Term, routes: Term.ROUTES },
};

const ROWS: Record<string, Row> = {
  "DaemonService.AddressPRFeedback": {
    call: () => Daemon.AddressPRFeedback("s1", 7),
    sends: { session: "s1", args: { number: 7 } },
  },
  "DaemonService.Answer": { call: () => Daemon.Answer("s1", "yes"), sends: { session: "s1", text: "yes" } },
  "DaemonService.BestOf": {
    call: () => Daemon.BestOf({ project: "lola", issue: "LOL-1", agents: ["claude", "codex"] } as never),
    sends: { args: { project: "lola", issue: "LOL-1", agents: ["claude", "codex"] } },
  },
  "DaemonService.BestOfCompare": { call: () => Daemon.BestOfCompare("s1"), sends: { args: { session: "s1" } } },
  "DaemonService.BestOfPick": { call: () => Daemon.BestOfPick("s1-b2"), sends: { args: { session: "s1-b2" } } },
  "DaemonService.Broadcast": {
    call: () => Daemon.Broadcast({ project: "lola", message: "rebase" }),
    sends: { args: { project: "lola", message: "rebase" } },
  },
  "DaemonService.CheckpointDiff": {
    call: () => Daemon.CheckpointDiff("s1", 3),
    sends: { args: { session: "s1", seq: 3 } },
  },
  "DaemonService.Checkpoints": { call: () => Daemon.Checkpoints("s1"), sends: { session: "s1" } },
  "DaemonService.CloseExitedTemporary": { call: () => Daemon.CloseExitedTemporary("t1"), sends: { session: "t1" } },
  "DaemonService.CodeRabbit": { call: () => Daemon.CodeRabbit("s1"), sends: { session: "s1" } },
  "DaemonService.Dev": { call: () => Daemon.Dev("s1", true), sends: { args: { session: "s1", on: true } } },
  "DaemonService.DevFreePort": {
    call: () => Daemon.DevFreePort("s1", 5173, 42),
    sends: { args: { session: "s1", port: 5173, pid: 42 } },
  },
  "DaemonService.Diff": { call: () => Daemon.Diff("s1"), sends: { session: "s1" } },
  "DaemonService.Disable": { call: () => Daemon.Disable("linear"), sends: { poll: "linear" } },
  "DaemonService.Enable": { call: () => Daemon.Enable("linear"), sends: { poll: "linear" } },
  "DaemonService.ForkCheckpoint": {
    call: () => Daemon.ForkCheckpoint("s1", 3, "codex"),
    sends: { args: { session: "s1", seq: 3, agent: "codex" } },
  },
  "DaemonService.Kill": { call: () => Daemon.Kill("s1", false, true), sends: { session: "s1", keepWorktree: true } },
  "DaemonService.MergePR": {
    call: () => Daemon.MergePR("s1", 7, "o/r", "abc", "squash", true),
    sends: { session: "s1", args: { number: 7, repo: "o/r", head: "abc", method: "squash", confirmUnready: true } },
  },
  "DaemonService.Open": { call: () => Daemon.Open("lola", "LOL-1"), sends: { project: "lola", ref: "LOL-1" } },
  "DaemonService.OpenManual": {
    call: () => Daemon.OpenManual({ project: "lola", name: "spike", prompt: "go" } as never),
    sends: { args: { project: "lola", name: "spike", prompt: "go" } },
  },
  "DaemonService.OpenPR": {
    call: () => Daemon.OpenPR({ project: "lola", number: 7, branch: "fix" } as never),
    sends: { args: { project: "lola", number: 7, branch: "fix" } },
  },
  "DaemonService.OpenTicket": {
    call: () => Daemon.OpenTicket({ project: "lola", identifier: "LOL-1", uuid: "u1" } as never),
    sends: { args: { project: "lola", identifier: "LOL-1", uuid: "u1" } },
  },
  "DaemonService.PRReadiness": {
    call: () => Daemon.PRReadiness("s1", 7),
    sends: { session: "s1", args: { number: 7 } },
  },
  "DaemonService.PRs": { call: () => Daemon.PRs("lola", true), sends: { args: { project: "lola", refresh: true } } },
  "DaemonService.Pane": { call: () => Daemon.Pane("s1", 60), sends: { session: "s1", lines: 60 } },
  "DaemonService.PollOnce": { call: () => Daemon.PollOnce("linear", true), sends: { poll: "linear", dryRun: true } },
  "DaemonService.Projects": { call: () => Daemon.Projects(), sends: undefined },
  "DaemonService.RenameSession": {
    call: () => Daemon.RenameSession("s1", "New name"),
    sends: { session: "s1", text: "New name" },
  },
  "DaemonService.ResolveConflict": { call: () => Daemon.ResolveConflict("s1"), sends: { session: "s1" } },
  "DaemonService.RestoreCheckpoint": {
    call: () => Daemon.RestoreCheckpoint("s1", 3),
    sends: { args: { session: "s1", seq: 3 } },
  },
  "DaemonService.Review": { call: () => Daemon.Review("s1", "codex"), sends: { session: "s1", provider: "codex" } },
  "DaemonService.ReviewPackageCapture": {
    call: () => Daemon.ReviewPackageCapture("s1", { package: "p1", url: "http://127.0.0.1:5173" } as never),
    sends: { session: "s1", args: { package: "p1", url: "http://127.0.0.1:5173" } },
  },
  "DaemonService.ReviewPackageChecks": {
    call: () => Daemon.ReviewPackageChecks("s1", "p1"),
    sends: { session: "s1", args: { package: "p1" } },
  },
  "DaemonService.ReviewPackageDiff": {
    call: () => Daemon.ReviewPackageDiff("s1", "a", "b"),
    sends: { session: "s1", args: { from: "a", to: "b" } },
  },
  "DaemonService.ReviewPackageImage": {
    call: () => Daemon.ReviewPackageImage("s1", "p1", "shot"),
    sends: { session: "s1", args: { package: "p1", artifact: "shot" } },
  },
  "DaemonService.ReviewPackageMark": {
    call: () => Daemon.ReviewPackageMark("s1", "p1", "p0"),
    sends: { session: "s1", args: { package: "p1", previous: "p0" } },
  },
  "DaemonService.ReviewPackageRecover": {
    call: () => Daemon.ReviewPackageRecover("s1", "p1", "c1"),
    sends: { session: "s1", args: { package: "p1", id: "c1" } },
  },
  "DaemonService.ReviewPackageVerify": {
    call: () => Daemon.ReviewPackageVerify("s1", { package: "p1", id: "c1" } as never),
    sends: { session: "s1", args: { package: "p1", id: "c1" } },
  },
  "DaemonService.ReviewPackages": { call: () => Daemon.ReviewPackages("s1"), sends: { session: "s1" } },
  "DaemonService.Revive": { call: () => Daemon.Revive("s1"), sends: { session: "s1" } },
  "DaemonService.SendFeedback": {
    call: () => Daemon.SendFeedback({ session: "s1", note: "tidy", comments: [] } as never),
    sends: { args: { session: "s1", note: "tidy", comments: [] } },
  },
  "DaemonService.SessionTask": {
    call: () => Daemon.SessionTask("s1", ["lola", "report"]),
    sends: { session: "s1", args: { argv: ["lola", "report"] } },
  },
  "DaemonService.Sessions": { call: () => Daemon.Sessions(), sends: undefined },
  "DaemonService.Status": { call: () => Daemon.Status(), sends: undefined },
  "DaemonService.SwitchAgent": {
    call: () => Daemon.SwitchAgent({ session: "s1", agent: "codex" }),
    sends: { args: { session: "s1", agent: "codex" } },
  },
  "DaemonService.Tickets": {
    call: () => Daemon.Tickets("lola", "mine"),
    sends: { args: { project: "lola", scope: "mine" } },
  },
  "DaemonService.Panes": { call: () => Daemon.Panes("s1"), sends: { session: "s1" } },
  "DaemonService.ShellCreate": {
    call: () => Daemon.ShellCreate("s1", 80, 24),
    sends: { session: "s1", cols: 80, rows: 24 },
  },
  "DaemonService.PaneClose": {
    call: () => Daemon.PaneClose("s1", "s1-shell-1"),
    sends: { args: { session: "s1", pane: "s1-shell-1" } },
  },
  "DaemonService.PaneResize": {
    call: () => Daemon.PaneResize("s1", "s1-shell-1", 80, 24),
    sends: { args: { session: "s1", pane: "s1-shell-1", cols: 80, rows: 24 } },
  },

  "TalkService.AttachClipboardImage": {
    call: () => Talk.AttachClipboardImage("/w/s1"),
    answers: { sessions: SESSIONS, endedSessions: { sessions: [] }, attachImage: { received: 3, path: IMAGE } },
    sends: {
      args: { session: "s1", name: expect.any(String), mime: "image/png", data: "AAAA", part: 0, last: true },
    },
  },
  "TalkService.Choose": {
    call: () => Talk.Choose({ session: "s1", key: "1", hash: "h" }),
    sends: { args: { session: "s1", key: "1", hash: "h" } },
  },
  "TalkService.Ended": { call: () => Talk.Ended(), sends: undefined },
  "TalkService.Feed": {
    call: () => Talk.Feed({ session: "s1", limit: 50 } as never),
    sends: { args: { session: "s1", limit: 50 } },
  },
  "TalkService.FeedOutput": {
    call: () => Talk.FeedOutput({ session: "s1", item: "i1" }),
    sends: { args: { session: "s1", item: "i1" } },
  },
  "TalkService.QueueRelease": { call: () => Talk.QueueRelease({ session: "s1" }), sends: { args: { session: "s1" } } },
  "TalkService.ReadAttachmentImage": {
    call: () => Talk.ReadAttachmentImage("/w/s1", IMAGE),
    answers: {
      sessions: SESSIONS,
      endedSessions: { sessions: [] },
      attachmentImage: { mime: "image/png", size: 3, offset: 0, data: "AAAA", eof: true },
    },
    sends: { args: { session: "s1", path: IMAGE } },
  },
  "TalkService.Say": {
    call: () => Talk.Say({ session: "s1", text: "hi", stopFirst: true } as never),
    sends: { args: { session: "s1", text: "hi", stopFirst: true } },
  },
  "TalkService.Setting": {
    call: () => Talk.Setting({ session: "s1", setting: "model", value: "opus" } as never),
    sends: { args: { session: "s1", setting: "model", value: "opus" } },
  },
  "TalkService.State": { call: () => Talk.State({ session: "s1" }), sends: { args: { session: "s1" } } },
  "TalkService.Stop": { call: () => Talk.Stop({ session: "s1" }), sends: { args: { session: "s1" } } },

  "SSHService.Answer": {
    call: () => SSH.Answer({ session: "s1", element: "e1", revision: 2, text: "ok" } as never),
    sends: { args: { session: "s1", element: "e1", revision: 2, text: "ok" } },
  },
  "SSHService.Recall": { call: () => SSH.Recall("s1", "i1"), sends: { args: { session: "s1", item: "i1" } } },
  "SSHService.Timeline": {
    call: () => SSH.Timeline({ session: "s1", after: 4 } as never),
    sends: { args: { session: "s1", after: 4 } },
  },

  "HubService.Status": { call: () => Hub.Status(), sends: undefined },

  "TermService.Capture": { call: () => Term.Capture("s1", 40), sends: { session: "s1", lines: 40 } },
};

const forwarded = Object.entries(SERVICES).flatMap(([service, { routes }]) =>
  Object.entries(routes).flatMap(([name, route]) => (route.kind === "fwd" ? [{ key: `${service}.${name}`, cmd: route.cmd }] : [])),
);

beforeEach(() => {
  request.mockReset();
  clearAttachmentCache();
  native.native.readClipboardImage.mockReset();
  native.native.readClipboardImage.mockResolvedValue({ present: true, mime: "image/png", data: "AAAA" });
});

describe("forwarded field placement", () => {
  it("reads the daemon's field rules", () => {
    expect(rules.flat.get("review")).toEqual(new Set(["session", "provider"]));
    expect(rules.flat.get("dev")).toEqual(new Set(["args"]));
    expect(rules.flat.get("reviewPackageMark")).toEqual(new Set(["session", "args"]));
    expect(rules.args.get("dev")).toEqual(new Set(["on", "session"]));
    expect([...rules.flat.keys()].some((cmd) => cmd.startsWith("?"))).toBe(false);
  });

  it("has exactly one row per forwarded route", () => {
    expect(Object.keys(ROWS).sort()).toEqual(forwarded.map((f) => f.key).sort());
  });

  for (const { key, cmd } of forwarded) {
    it(`${key} sends cmd=${cmd} with every field where the daemon keeps it`, async () => {
      const row = ROWS[key];
      request.mockImplementation(async (_m, c) => row.answers?.[c] ?? {});
      await row.call().catch(() => {});
      const calls = request.mock.calls.filter((c) => c[1] === cmd);
      expect(calls.length, key).toBeGreaterThan(0);
      expect(calls[0][0]).toBe(key);
      expect(calls[0][2]).toEqual(row.sends);
      for (const c of calls) expectKeptByDaemon(rules, cmd, c[2]);
    });
  }
});
