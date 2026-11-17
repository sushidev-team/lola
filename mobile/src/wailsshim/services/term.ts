// TermService: one function per generated method, routed as ROUTES says.
import type * as Gen from "@bindings-generated/desktop";
import type * as P from "@bindings/internal/protocol";
import { bridge } from "../bridge";
import { unsupported } from "../errors";
import { MAC_ONLY } from "./reasons";
import type { Routes } from "./routes";

// Panes bound through Attach, so DetachAll can release exactly those.
const attached = new Set<string>();

/** FORWARDED: a pane subscription, republished on `pty:<name>`; resolves the stream id. */
export async function Attach(name: string, cols: number, rows: number): Promise<string> {
  const id = await bridge.attachPane(name, cols, rows);
  attached.add(name);
  return id;
}

/** FORWARDED: cmd=pane; the text is what capture-pane returns. */
export async function Capture(name: string, lines: number): Promise<string> {
  const d = await bridge.request<P.PaneData>("TermService.Capture", "pane", { session: name, lines });
  return d?.text ?? "";
}

/** LOCAL fan-out of Capture; a name that fails is omitted, not fatal. */
export async function CaptureMany(
  names: string[] | null,
  lines: number,
): Promise<{ [_ in string]?: string } | null> {
  const out: { [_ in string]?: string } = {};
  await Promise.allSettled(
    (names ?? []).map(async (n) => {
      out[n] = await Capture(n, lines);
    }),
  );
  return out;
}

/** PLATFORM. */
export function CloseSessionShells(_sessionID: string): Promise<void> {
  return unsupported("TermService.CloseSessionShells", MAC_ONLY);
}

/** PLATFORM. */
export function CloseShell(_shell: string): Promise<void> {
  return unsupported("TermService.CloseShell", MAC_ONLY);
}

/** FORWARDED: an `unsub` frame; never waits. */
export function Detach(name: string): Promise<void> {
  attached.delete(name);
  return bridge.detachPane(name);
}

/** LOCAL: detaches every pane this module bound. */
export async function DetachAll(): Promise<void> {
  const names = [...attached];
  attached.clear();
  await Promise.all(names.map((n) => bridge.detachPane(n)));
}

/** FORWARDED: a `pty` resize frame. */
export function Resize(name: string, cols: number, rows: number): Promise<void> {
  return bridge.resizePane(name, cols, rows);
}

/** FORWARDED: a `pty` scroll frame, clamped daemon-side. */
export function Scroll(name: string, lines: number): Promise<void> {
  return bridge.paneSubscription(name)?.scroll(lines) ?? Promise.resolve();
}

/** LOCAL: no-op; there is no Wails application. */
export function SetApp(_app: unknown): Promise<void> {
  return Promise.resolve();
}

/** PLATFORM. */
export function Shell(_shell: string, _worktree: string): Promise<string> {
  return unsupported("TermService.Shell", MAC_ONLY);
}

/** PLATFORM. */
export function Shells(_sessionID: string): Promise<string[] | null> {
  return unsupported("TermService.Shells", MAC_ONLY);
}

/** FORWARDED: a `pty` write frame; a detached pane makes it a no-op. */
export function Write(name: string, data: string): Promise<void> {
  return bridge.paneSubscription(name)?.write(data) ?? Promise.resolve();
}

export const ROUTES: Routes<typeof Gen.TermService> = {
  Attach: { kind: "local" },
  Capture: { kind: "fwd", cmd: "pane" },
  CaptureMany: { kind: "local" },
  CloseSessionShells: { kind: "platform", reason: MAC_ONLY },
  CloseShell: { kind: "platform", reason: MAC_ONLY },
  Detach: { kind: "local" },
  DetachAll: { kind: "local" },
  Resize: { kind: "local" },
  Scroll: { kind: "local" },
  SetApp: { kind: "local" },
  Shell: { kind: "platform", reason: MAC_ONLY },
  Shells: { kind: "platform", reason: MAC_ONLY },
  Write: { kind: "local" },
};
