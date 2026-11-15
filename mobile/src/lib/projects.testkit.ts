// A scripted daemon behind the real bridge, transport and correlator, so a test
// of the Projects tab proves which command and arguments go out. Only the bytes
// are faked. Not a suite: vitest does not collect it.

import type { ProjectInfo } from "@bindings/internal/protocol";
import { bridge } from "@mobile/wailsshim/bridge";
import { ChannelTransport } from "@mobile/wailsshim/channeltransport";
import { FRAME_RESP, FakeChannel, type Frame } from "@mobile/wire";

export type Reply = { ok: true; data: unknown } | { ok: false; error: string };

export class ScriptedDaemon {
  ch = new FakeChannel();
  replies: Record<string, Reply> = {};

  async install(): Promise<void> {
    this.ch = new FakeChannel();
    this.ch.onSend = (f: Frame) => {
      if (f.type !== "req") return;
      const r = this.replies[String(f.cmd)] ?? { ok: true, data: {} };
      this.ch.deliver({ v: 2, type: FRAME_RESP, id: f.id, payload: r });
    };
    const t = new ChannelTransport({ open: async () => this.ch });
    await t.connect({ daemonId: "d" });
    bridge.installTransport(t);
  }

  uninstall(): void {
    bridge.installTransport(null);
  }

  /** The payload of the first request for a command. */
  payloadOf(cmd: string): Record<string, unknown> | undefined {
    return this.ch.sent.find((f) => f.type === "req" && f.cmd === cmd)?.payload as
      | Record<string, unknown>
      | undefined;
  }

  count(cmd: string): number {
    return this.ch.sent.filter((f) => f.type === "req" && f.cmd === cmd).length;
  }
}

export function project(over: Partial<ProjectInfo> = {}): ProjectInfo {
  return {
    name: "nori-app",
    label: "Nori",
    group: "",
    path: "/Volumes/Git/nori",
    repo: "sushidev-team/nori",
    defaultBranch: "main",
    agent: "claude",
    agentBin: "claude",
    agentOk: true,
    pathOk: true,
    repoConfigured: true,
    pollCount: 1,
    pollsEnabled: 1,
    lastRun: "",
    sessions: 0,
    liveCounted: 2,
    needsYou: 0,
    ciRed: 0,
    openPrs: 1,
    ...over,
  } as unknown as ProjectInfo;
}
