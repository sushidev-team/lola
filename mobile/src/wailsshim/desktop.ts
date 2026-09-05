// The module vite.config.ts aliases `@bindings/desktop` at: the six Wails
// service namespaces the shared frontend calls, reimplemented over the daemon's
// remote protocol.
//
// The shape is copied from the generated bindings deliberately — same namespace
// names, same method names, same argument order, same return types — because
// that is what lets 26 components and `store.svelte.ts` compile and run with no
// edit at all. A method here is one of exactly three things, and which one it is
// is stated at every call site:
//
//   FORWARDED  one daemon command over the wire. The overwhelming majority.
//   PLATFORM   no mobile equivalent: rejects with UnsupportedOnMobileError,
//              naming the method and the reason. Never resolves silently — the
//              store flashes a rejection, and a resolve would report success
//              for something that did not happen.
//   ABSENT     deliberately not exported, because the shared code already
//              feature-probes for it and degrades correctly. Only GetTheme and
//              SetTheme are in this category; see ConfigService below.
//
// Every DTO type is re-exported from the real generated models, which are pure
// `export interface` declarations and therefore erased at build time. A DTO is
// a shape and the shape is identical on both platforms, because the same daemon
// produces it.

import type * as M from "@bindings/desktop/models";
import type * as P from "@bindings/internal/protocol";
import { bridge } from "./bridge";
import { unsupported } from "./errors";
import { WireError, normalizePanesData } from "../wire";
import type {
  PaneCloseData,
  PaneResizeData,
  PanesData,
  RawPanesData,
  ShellCreateData,
} from "../wire";

// ---------------------------------------------------------------------------
// DaemonService
// ---------------------------------------------------------------------------

export namespace DaemonService {
  // --- reads that drive the store ------------------------------------------

  /**
   * FORWARDED (locally derived). There is no `alive` command: on the desktop it
   * dials the unix socket, which a phone cannot do. The connection's own state
   * is the exact equivalent — a ready transport means the daemon answered a
   * handshake — and it is deliberately resolved rather than thrown so a
   * not-yet-connected app shows its offline state instead of an error.
   */
  export function Alive(): Promise<boolean> {
    return Promise.resolve(bridge.alive());
  }

  /** FORWARDED: cmd=sessions. */
  export function Sessions(): Promise<P.SessionsData> {
    return bridge.request<P.SessionsData>("DaemonService.Sessions", "sessions");
  }

  /** FORWARDED: cmd=projects. */
  export function Projects(): Promise<P.ProjectsData> {
    return bridge.request<P.ProjectsData>("DaemonService.Projects", "projects");
  }

  /** FORWARDED: cmd=status. */
  export function Status(): Promise<P.StatusData> {
    return bridge.request<P.StatusData>("DaemonService.Status", "status");
  }

  /** FORWARDED: cmd=pane. */
  export function Pane(session: string, lines: number): Promise<P.PaneData> {
    return bridge.request<P.PaneData>("DaemonService.Pane", "pane", {
      session,
      lines,
    });
  }

  /** FORWARDED: cmd=prs. */
  export function PRs(project: string, refresh: boolean): Promise<P.PrsData> {
    return bridge.request<P.PrsData>("DaemonService.PRs", "prs", {
      args: { project, refresh },
    });
  }

  /** FORWARDED: cmd=tickets. */
  export function Tickets(
    project: string,
    scope: string,
  ): Promise<P.TicketsData> {
    return bridge.request<P.TicketsData>("DaemonService.Tickets", "tickets", {
      args: { project, scope },
    });
  }

  // --- session actions ------------------------------------------------------

  /** FORWARDED: cmd=answer. Refused daemon-side unless the session is idle. */
  export function Answer(session: string, text: string): Promise<void> {
    return bridge.request<void>("DaemonService.Answer", "answer", {
      session,
      text,
    });
  }

  /**
   * FORWARDED: cmd=kill — but only WITHOUT force.
   *
   * The daemon rebuilds a fresh Request from an allowlist for every remote
   * command and clears `Force` unconditionally, so a forced kill is not
   * expressible over this protocol. Sending one anyway would produce the
   * dirty-worktree refusal again, and `store.kill` matches that refusal in
   * order to re-ask — so the user would confirm "remove it anyway", get the
   * same question back, and loop. Refusing here with a sentence is the honest
   * version of the same outcome.
   */
  export function Kill(session: string, force: boolean): Promise<P.KillData> {
    if (force) {
      return Promise.reject(
        new WireError(
          "a forced kill is refused for remote clients: the daemon clears `force` on every remote request, so a worktree with uncommitted changes can only be removed from the Mac",
        ),
      );
    }
    return bridge.request<P.KillData>("DaemonService.Kill", "kill", {
      session,
    });
  }

  /** FORWARDED: cmd=revive. */
  export function Revive(session: string): Promise<P.ReviveData> {
    return bridge.request<P.ReviveData>("DaemonService.Revive", "revive", {
      session,
    });
  }

  /** FORWARDED: cmd=review. */
  export function Review(
    session: string,
    provider: string,
  ): Promise<P.ReviewData> {
    return bridge.request<P.ReviewData>("DaemonService.Review", "review", {
      session,
      provider,
    });
  }

  /** FORWARDED: cmd=coderabbit. */
  export function CodeRabbit(session: string): Promise<P.CodeRabbitData> {
    return bridge.request<P.CodeRabbitData>(
      "DaemonService.CodeRabbit",
      "coderabbit",
      { session },
    );
  }

  /** FORWARDED: cmd=resolveConflict. */
  export function ResolveConflict(
    session: string,
  ): Promise<P.ResolveConflictData> {
    return bridge.request<P.ResolveConflictData>(
      "DaemonService.ResolveConflict",
      "resolveConflict",
      { session },
    );
  }

  /** FORWARDED: cmd=switchAgent. */
  export function SwitchAgent(
    a: P.SwitchAgentArgs,
  ): Promise<P.SwitchAgentData> {
    return bridge.request<P.SwitchAgentData>(
      "DaemonService.SwitchAgent",
      "switchAgent",
      {
        args: a,
      },
    );
  }

  /** FORWARDED: cmd=dev. */
  export function Dev(session: string, on: boolean): Promise<P.DevData> {
    return bridge.request<P.DevData>("DaemonService.Dev", "dev", {
      args: { session, on },
    });
  }

  /** FORWARDED: cmd=devFreePort. */
  export function DevFreePort(
    session: string,
    port: number,
    pid: number,
  ): Promise<P.DevFreePortData> {
    return bridge.request<P.DevFreePortData>(
      "DaemonService.DevFreePort",
      "devFreePort",
      {
        args: { session, port, pid },
      },
    );
  }

  // --- panes and shell tabs -------------------------------------------------
  //
  // These two have NO DESKTOP COUNTERPART, which makes them the only methods in
  // this file whose shape was not copied from a generated binding. The desktop
  // does both in-process through `desktop/termsvc.go` (TermService.Shells and
  // TermService.Shell), driving `tmux -L lola` on the machine it runs on. A
  // phone has no tmux and no filesystem, so the daemon grew `cmd=panes` and
  // `cmd=shellCreate` for it (internal/daemon/panes.go).
  //
  // They live on DaemonService rather than TermService on purpose: TermService
  // mirrors the desktop's Wails service, whose Shells/Shell have a different
  // shape and stay PLATFORM rejections below. A mobile tab strip calls THESE.

  /**
   * FORWARDED: cmd=panes — which panes exist for a session, in the order a tab
   * strip should draw them.
   *
   * Derived from tmux on every call, so it is the live truth rather than a
   * cache. The payload is normalized (`normalizePanesData`) because there is no
   * generated `createFrom` here to do what it does for every other Data type:
   * the daemon writes `"panes": null` for a session with no panes, and an absent
   * review pane as a zero-valued struct rather than as an omitted field. See
   * mobile/src/wire/panes.ts for both encoding details.
   */
  export async function Panes(session: string): Promise<PanesData> {
    const d = await bridge.request<RawPanesData>(
      "DaemonService.Panes",
      "panes",
      { session },
    );
    return normalizePanesData(session, d);
  }

  /**
   * FORWARDED: cmd=shellCreate — start a new shell tab in the session's
   * worktree and return the pane to subscribe to.
   *
   * The daemon allocates the index; a client must not invent a name, because
   * two phones and a desktop can be racing for "-shell-2" and only the daemon
   * sees all of them.
   *
   * A REFUSAL IS THE POINT OF THE ERROR PATH, so nothing here catches or
   * rewraps it. The daemon answers `ok:false` with a sentence naming the reason
   * — no worktree, a worktree that has been removed underneath the session, or
   * the shell cap — and the correlator turns that into a `DaemonError` whose
   * `message` IS that sentence. Show it. A generic "could not create shell"
   * throws away the only part a user can act on, and a tab strip whose "+"
   * stops working for no stated reason reads as a broken button.
   */
  export function ShellCreate(
    session: string,
    cols = 0,
    rows = 0,
  ): Promise<ShellCreateData> {
    // The SIZE is this phone's capacity, and it is what stops a new shell tab
    // reflowing itself in front of the user. tmux gives an unattached session
    // 157x37 or so, so a tab created without one was pinned a moment later and
    // visibly redrew line by line for several seconds. The daemon creates the
    // window at this size instead, and ignores anything <= 0 or out of range —
    // so an unmeasured screen simply gets the old behaviour.
    const size = cols > 0 && rows > 0 ? { cols, rows } : {};
    return bridge.request<ShellCreateData>(
      "DaemonService.ShellCreate",
      "shellCreate",
      {
        session,
        ...size,
      },
    );
  }

  /**
   * FORWARDED: cmd=paneClose -- end one auxiliary pane of a session.
   *
   * THE AGENT PANE IS REFUSED by the daemon, and that refusal is a rule rather
   * than a limitation: the agent pane IS the session, so closing it would end
   * the work and leave a record pointing at nothing. Teardown is `cmd=kill`,
   * which knows to take the worktree and the branch with it. A caller must not
   * offer this on the agent tab and then relay the refusal -- it must not offer
   * it at all.
   *
   * The daemon kills the session TREE, not the tmux session: a shell can be
   * holding a port through a process that ignores SIGHUP, and `kill-session`
   * alone would orphan it onto pid 1.
   *
   * A REFUSAL IS THE POINT OF THE ERROR PATH, exactly as it is for ShellCreate,
   * so nothing here catches or rewraps it. See wire/panes.ts for the three
   * refusals and why each one wants the daemon's own sentence.
   *
   * Note the ARGS ENVELOPE, described on PaneResize below.
   */
  export function PaneClose(
    session: string,
    pane: string,
  ): Promise<PaneCloseData> {
    return bridge.request<PaneCloseData>(
      "DaemonService.PaneClose",
      "paneClose",
      {
        args: { session, pane },
      },
    );
  }

  /**
   * FORWARDED: cmd=paneResize — pin this pane's tmux window to an explicit size,
   * or release it back to whatever clients remain attached.
   *
   * `cols <= 0` IS THE RELEASE, which is why `PaneResize(s, p, 0, 0)` is the
   * undo rather than a separate command. The daemon rejects anything over 500
   * in either dimension on the pin path and lets a nonsense release through, so
   * a client that pinned badly can always get back out.
   *
   * THIS RESHAPES SOMEBODY ELSE'S SCREEN. The pane-byte fan-out deliberately
   * cannot — `internal/panebus` attaches with `-f ignore-size` precisely so a
   * phone joining a stream never narrows the developer's tmux window — and this
   * command is the one sanctioned exception, for as long as a human is looking
   * at the pane on a phone and not one moment longer. Nothing may call it except
   * the pin lifecycle in `@mobile/lib/panepin`, which owns the release.
   *
   * Note the ARGS ENVELOPE. `panes` and `shellCreate` take flat fields; this one
   * and `paneClose` take a nested `args` object, because their Go handlers are
   * declared over `protocol.PaneResizeArgs` / `PaneCloseArgs` rather than over
   * the request's own top-level fields.
   */
  export function PaneResize(
    session: string,
    pane: string,
    cols: number,
    rows: number,
  ): Promise<PaneResizeData> {
    return bridge.request<PaneResizeData>(
      "DaemonService.PaneResize",
      "paneResize",
      {
        args: { session, pane, cols, rows },
      },
    );
  }

  // --- opening work ---------------------------------------------------------

  /** FORWARDED: cmd=open. */
  export function Open(project: string, ref: string): Promise<P.OpenData> {
    return bridge.request<P.OpenData>("DaemonService.Open", "open", {
      project,
      ref,
    });
  }

  /** FORWARDED: cmd=openManual. */
  export function OpenManual(a: P.OpenManualArgs): Promise<P.OpenData> {
    return bridge.request<P.OpenData>(
      "DaemonService.OpenManual",
      "openManual",
      { args: a },
    );
  }

  /** FORWARDED: cmd=openPr. */
  export function OpenPR(a: P.OpenPrArgs): Promise<P.OpenData> {
    return bridge.request<P.OpenData>("DaemonService.OpenPR", "openPr", {
      args: a,
    });
  }

  /** FORWARDED: cmd=openTicket. */
  export function OpenTicket(a: P.OpenTicketArgs): Promise<P.OpenData> {
    return bridge.request<P.OpenData>(
      "DaemonService.OpenTicket",
      "openTicket",
      { args: a },
    );
  }

  /**
   * FORWARDED: cmd=openURL.
   *
   * The URL opens on the MACHINE THAT RUNS THE DAEMON, not on the phone, and
   * that is the behaviour this milestone ships: the http(s)-only guard lives in
   * the daemon, the addresses in question are loopback dev servers that only
   * resolve there, and the terminal text these URLs are scraped out of is
   * untrusted. Opening them phone-side is a real question with a real answer —
   * it just needs a decision about the guard first, and `window.open` is not it
   * (in a WKWebView it replaces the app's own view).
   */
  export function OpenURL(url: string): Promise<void> {
    return bridge.request<void>("DaemonService.OpenURL", "openURL", {
      args: { url },
    });
  }

  // --- polls ----------------------------------------------------------------

  /** FORWARDED: cmd=enable. */
  export function Enable(poll: string): Promise<void> {
    return bridge.request<void>("DaemonService.Enable", "enable", { poll });
  }

  /** FORWARDED: cmd=disable. */
  export function Disable(poll: string): Promise<void> {
    return bridge.request<void>("DaemonService.Disable", "disable", { poll });
  }

  /** FORWARDED: cmd=pollOnce. */
  export function PollOnce(
    poll: string,
    dryRun: boolean,
  ): Promise<P.PollOnceData> {
    return bridge.request<P.PollOnceData>(
      "DaemonService.PollOnce",
      "pollOnce",
      { poll, dryRun },
    );
  }

  /**
   * PLATFORM. `reload` is on the daemon's own denial list for remote peers, and
   * a denied command is FATAL there — one err frame and the socket closes,
   * taking every live pane subscription with it. So this never reaches the wire.
   */
  export function Reload(): Promise<void> {
    return unsupported(
      "DaemonService.Reload",
      "the daemon refuses `reload` from a remote client, and a refused command closes the connection",
    );
  }

  /** PLATFORM: `renameProject` is on the same denial list, for the same reason. */
  export function RenameProject(
    _from: string,
    _to: string,
  ): Promise<P.RenameProjectData> {
    return unsupported(
      "DaemonService.RenameProject",
      "the daemon refuses `renameProject` from a remote client; rename from the Mac",
    );
  }

  // --- PLATFORM: local process control --------------------------------------

  /** PLATFORM: the daemon is a process on another machine. */
  export function StartDaemon(): Promise<void> {
    return unsupported(
      "DaemonService.StartDaemon",
      "the daemon runs on the Mac and cannot be started from a phone",
    );
  }

  /** PLATFORM. `stop` is also on the daemon's denial list. */
  export function StopDaemon(): Promise<void> {
    return unsupported(
      "DaemonService.StopDaemon",
      "the daemon refuses `stop` from a remote client",
    );
  }

  /** PLATFORM: a restart would need a daemon-side command that does not exist. */
  export function RestartDaemon(): Promise<void> {
    return unsupported(
      "DaemonService.RestartDaemon",
      "the daemon runs on the Mac and cannot be restarted from a phone",
    );
  }

  /** PLATFORM: reports the CLI binary on the local filesystem. */
  export function CLIInfo(): Promise<M.CLIInfoDTO> {
    return unsupported(
      "DaemonService.CLIInfo",
      "there is no lola CLI on a phone",
    );
  }

  /** PLATFORM: symlinks the bundled CLI onto the local PATH. */
  export function InstallCLI(): Promise<M.CLIInstallDTO> {
    return unsupported(
      "DaemonService.InstallCLI",
      "there is no PATH on a phone to install a CLI onto",
    );
  }
}

// ---------------------------------------------------------------------------
// TermService
// ---------------------------------------------------------------------------

export namespace TermService {
  /**
   * FORWARDED: a pane subscription, republished on `pty:<name>`.
   * Returns the stream id (the pane name), as the desktop's does.
   */
  export function Attach(
    name: string,
    cols: number,
    rows: number,
  ): Promise<string> {
    return bridge.attachPane(name, cols, rows);
  }

  /** FORWARDED: an `unsub` frame. Unacknowledged by design, so it never waits. */
  export function Detach(name: string): Promise<void> {
    return bridge.detachPane(name);
  }

  /** FORWARDED: a `pty` input frame with action `write`. */
  export function Write(name: string, data: string): Promise<void> {
    const sub = bridge.paneSubscription(name);
    if (!sub) return Promise.resolve(); // a write to a detached pane is a no-op
    return sub.write(data);
  }

  /**
   * FORWARDED: a `pty` input frame with action `resize`.
   *
   * The daemon records it and ignores it in M1 — the pane is attached at the
   * developer's tmux window size and a phone-sized viewport pans rather than
   * reflowing. Sent anyway so the daemon has the client's geometry the moment
   * it starts acting on it.
   */
  export function Resize(
    name: string,
    cols: number,
    rows: number,
  ): Promise<void> {
    const sub = bridge.paneSubscription(name);
    if (!sub) return Promise.resolve();
    return sub.resize(cols, rows);
  }

  /**
   * FORWARDED: a `pty` input frame with action `scroll`, clamped server-side.
   *
   * Note for whoever builds the mobile terminal view: LiveTerminal drives this
   * from `attachCustomWheelEventHandler`, which never fires on touch. A touch
   * scroll has to be converted to lines and routed here by the mobile wrapper.
   */
  export function Scroll(name: string, lines: number): Promise<void> {
    const sub = bridge.paneSubscription(name);
    if (!sub) return Promise.resolve();
    return sub.scroll(lines);
  }

  /** FORWARDED: cmd=pane, whose text is exactly what capture-pane returns. */
  export async function Capture(name: string, lines: number): Promise<string> {
    const d = await bridge.request<P.PaneData>("TermService.Capture", "pane", {
      session: name,
      lines,
    });
    return d?.text ?? "";
  }

  /**
   * FORWARDED: one cmd=pane per name.
   *
   * There is no batch command, so this is a fan-out. The correlator holds it
   * under the daemon's four-in-flight cap by queueing, which means a wide grid
   * is SLOW rather than refused — worth knowing before a mobile view asks for
   * thirty tiles at once. A name that fails is omitted rather than failing the
   * batch, matching the desktop's own map-returning behaviour.
   */
  export async function CaptureMany(
    names: string[] | null,
    lines: number,
  ): Promise<Record<string, string> | null> {
    if (!names || names.length === 0) return {};
    const out: Record<string, string> = {};
    await Promise.allSettled(
      names.map(async (n) => {
        try {
          out[n] = await Capture(n, lines);
        } catch {
          /* a tile that cannot be captured is simply absent */
        }
      }),
    );
    return out;
  }

  /**
   * PLATFORM, and it stays that way now that `cmd=panes` exists.
   *
   * The enumeration a phone needs is `DaemonService.Panes`, which answers with
   * the WHOLE inventory — agent, shells, dev tabs, review — in strip order, and
   * that is a different question from this one: `terms.svelte.ts` asks for a
   * session's shell tabs only, as bare names, having already decided the desktop
   * owns the naming. Answering it from `panes` would mean filtering the daemon's
   * classification back down to a list of strings and then re-deriving the
   * classification in the view, which is the thing `panes` exists to stop.
   *
   * `terms.svelte.ts`'s `refresh()` already swallows this rejection and keeps
   * its last-known list, so it costs an empty desktop-style tab bar rather than
   * an error. The mobile tab strip is a mobile view and calls Panes directly.
   */
  export function Shells(sessionID: string): Promise<string[] | null> {
    return unsupported(
      `TermService.Shells(${sessionID})`,
      "the remote protocol cannot enumerate a session's shell tabs; it would need a daemon-side `shells` command",
    );
  }

  /**
   * PLATFORM — because of its SIGNATURE, not its capability. A phone can start
   * a shell on the Mac now; `DaemonService.ShellCreate` does it.
   *
   * This method cannot be the way, because it takes the shell's NAME and its
   * worktree PATH from the caller: the desktop's frontend allocates
   * "<id>-shell-N" itself, which is safe when one process on one machine is the
   * only allocator and is a collision the moment two phones and a desktop are
   * asking. `cmd=shellCreate` therefore allocates daemon-side and takes only a
   * session id. Honouring this signature would mean sending a name the daemon
   * would have to trust.
   */
  export function Shell(shell: string, _worktree: string): Promise<string> {
    return unsupported(
      `TermService.Shell(${shell})`,
      "a shell name is allocated by the daemon, not by a client; use DaemonService.ShellCreate(session)",
    );
  }

  /** PLATFORM: killing a tmux session needs local process control. */
  export function CloseShell(shell: string): Promise<void> {
    return unsupported(
      `TermService.CloseShell(${shell})`,
      "a phone cannot kill a tmux session on the Mac",
    );
  }

  /**
   * PLATFORM, and safely so: `store.kill` calls this and swallows the
   * rejection, so a session teardown from the phone still proceeds — it simply
   * leaves the Mac's shell tabs alone, which the daemon's own teardown cleans
   * up anyway (runtime.Kill takes down every aux session).
   */
  export function CloseSessionShells(sessionID: string): Promise<void> {
    return unsupported(
      `TermService.CloseSessionShells(${sessionID})`,
      "a phone cannot kill tmux sessions on the Mac; the daemon's own teardown covers them",
    );
  }
}

// ---------------------------------------------------------------------------
// ConfigService
// ---------------------------------------------------------------------------

export namespace ConfigService {
  /**
   * Locally derived, and deliberately optimistic. It gates the first-run setup
   * wizard, and that wizard configures a Mac: a connected daemon by definition
   * has a config, and answering `false` would put a phone into a flow that
   * cannot complete. `store.checkConfig` makes the same choice on error ("on
   * doubt, don't force the setup screen").
   */
  export function ConfigExists(): Promise<boolean> {
    return Promise.resolve(true);
  }

  // GetTheme and SetTheme are ABSENT ON PURPOSE, not stubbed.
  //
  // `theme-runtime.svelte.ts` reaches them through a lazy import and FEATURE-
  // PROBES both (`if (typeof svc.GetTheme !== "function") return undefined`),
  // because they were added to the Go side after the module existed. Leaving
  // them off is therefore a supported state that the shared code already
  // handles correctly: the flavor comes from localStorage and the compiled
  // default, and an attempt to save one throws "this build cannot save a
  // theme". That is also the right product answer — the phone's theme is the
  // phone's, and there is no remote command to read the Mac's `[ui].theme`.

  /** PLATFORM: `[ui].theme` lives in config.toml on the Mac. */
  export function Themes(): Promise<string[] | null> {
    return unsupported(
      "ConfigService.Themes",
      "config.toml is not readable from a phone",
    );
  }

  const CONFIG_REASON =
    "editing config.toml from a phone is not part of M1: there is no remote config command, and the forms depend on a native folder picker";

  /** PLATFORM. */
  export function GetSettings(): Promise<M.SettingsDTO> {
    return unsupported("ConfigService.GetSettings", CONFIG_REASON);
  }
  /** PLATFORM. */
  export function SaveSettings(_dto: M.SettingsDTO): Promise<void> {
    return unsupported("ConfigService.SaveSettings", CONFIG_REASON);
  }
  /** PLATFORM. */
  export function GetProject(name: string): Promise<M.ProjectFormDTO> {
    return unsupported(`ConfigService.GetProject(${name})`, CONFIG_REASON);
  }
  /** PLATFORM. */
  export function SaveProject(_dto: M.ProjectFormDTO): Promise<void> {
    return unsupported("ConfigService.SaveProject", CONFIG_REASON);
  }
  /** PLATFORM. */
  export function RemoveProject(name: string): Promise<void> {
    return unsupported(`ConfigService.RemoveProject(${name})`, CONFIG_REASON);
  }
  /** PLATFORM. */
  export function AddGroup(_label: string): Promise<string> {
    return unsupported("ConfigService.AddGroup", CONFIG_REASON);
  }
  /** PLATFORM. */
  export function RenameGroup(_name: string, _label: string): Promise<void> {
    return unsupported("ConfigService.RenameGroup", CONFIG_REASON);
  }
  /** PLATFORM. */
  export function RemoveGroup(_name: string): Promise<void> {
    return unsupported("ConfigService.RemoveGroup", CONFIG_REASON);
  }
  /** PLATFORM. */
  export function SetGroupCollapsed(
    _name: string,
    _collapsed: boolean,
  ): Promise<void> {
    return unsupported("ConfigService.SetGroupCollapsed", CONFIG_REASON);
  }
  /**
   * PLATFORM, and the one worth being loud about: the project layout write
   * FAILS CLOSED daemon-side (the payload must be an exact permutation of the
   * configured projects and groups). A phone computing a drag against a poll
   * that may be two seconds stale is precisely the case that guard exists for.
   */
  export function SetProjectLayout(_dto: M.ProjectLayoutDTO): Promise<void> {
    return unsupported("ConfigService.SetProjectLayout", CONFIG_REASON);
  }
  /** PLATFORM. */
  export function InspectPath(path: string): Promise<M.PathInfoDTO> {
    return unsupported(
      `ConfigService.InspectPath(${path})`,
      "a phone cannot read the Mac's filesystem",
    );
  }
  /** PLATFORM. */
  export function PrioritySortKeys(): Promise<string[] | null> {
    return unsupported("ConfigService.PrioritySortKeys", CONFIG_REASON);
  }
  /** PLATFORM. */
  export function ReviewKinds(): Promise<M.ReviewKindDTO[] | null> {
    return unsupported("ConfigService.ReviewKinds", CONFIG_REASON);
  }
  /** PLATFORM. */
  export function ReviewProviderKinds(): Promise<string[] | null> {
    return unsupported("ConfigService.ReviewProviderKinds", CONFIG_REASON);
  }
  /** PLATFORM. */
  export function TransportTokens(): Promise<string[] | null> {
    return unsupported("ConfigService.TransportTokens", CONFIG_REASON);
  }
  /** PLATFORM. */
  export function MigrateReview(): Promise<void> {
    return unsupported("ConfigService.MigrateReview", CONFIG_REASON);
  }
  /** PLATFORM. */
  export function LinearKeyStatus(): Promise<M.LinearKeyStatusDTO> {
    return unsupported("ConfigService.LinearKeyStatus", CONFIG_REASON);
  }
  /**
   * PLATFORM, and it must stay that way. Validating a Linear key means SENDING
   * it, and the phone has no business holding the workspace's API key: it lives
   * in the Mac's keychain, write-only, and nothing reads it back.
   */
  export function ValidateLinearKey(_key: string): Promise<void> {
    return unsupported(
      "ConfigService.ValidateLinearKey",
      "the Linear API key lives in the Mac's keychain and is never handled on a phone",
    );
  }
  /** PLATFORM, for the same reason as ValidateLinearKey. */
  export function SetLinearKey(_key: string): Promise<string> {
    return unsupported(
      "ConfigService.SetLinearKey",
      "the Linear API key lives in the Mac's keychain and is never handled on a phone",
    );
  }
  /** PLATFORM: the first-run wizard configures a Mac. */
  export function Setup(_dto: M.SetupDTO): Promise<M.SetupResultDTO> {
    return unsupported("ConfigService.Setup", CONFIG_REASON);
  }
  /**
   * PLATFORM: it opens an NSOpenPanel over the Mac's filesystem. A phone cannot
   * browse a checkout that is not on it, and accepting a typed path instead
   * would let someone configure a project pointing at nothing.
   */
  export function PickFolder(start: string): Promise<string> {
    return unsupported(
      `ConfigService.PickFolder(${start})`,
      "a phone has no access to the Mac's filesystem and no native folder picker for it",
    );
  }
}

// ---------------------------------------------------------------------------
// LinearService — team metadata for the config forms, which are deferred.
// ---------------------------------------------------------------------------

const LINEAR_REASON =
  "Linear metadata is fetched by the daemon for the config forms, which are not part of M1";

export namespace LinearService {
  /** PLATFORM. */
  export function Teams(): Promise<M.LinearTeam[] | null> {
    return unsupported("LinearService.Teams", LINEAR_REASON);
  }
  /** PLATFORM. */
  export function TeamMeta(
    teamID: string,
    _refresh: boolean,
  ): Promise<M.LinearTeamMeta> {
    return unsupported(`LinearService.TeamMeta(${teamID})`, LINEAR_REASON);
  }
  /** PLATFORM. */
  export function WorkspaceLabels(): Promise<M.LinearOption[] | null> {
    return unsupported("LinearService.WorkspaceLabels", LINEAR_REASON);
  }
}

// ---------------------------------------------------------------------------
// DoctorService — health checks of the LOCAL machine.
// ---------------------------------------------------------------------------

export namespace DoctorService {
  /**
   * PLATFORM. Every check it runs (tmux, git, gh, the coding agent, the socket,
   * the worktree root) probes the machine the daemon is on, through local exec.
   * A remote doctor is a genuinely useful feature and a genuinely different
   * one: it needs a daemon-side command that reports its own health.
   */
  export function Run(): Promise<M.DoctorReportDTO> {
    return unsupported(
      "DoctorService.Run",
      "the doctor probes the local machine; a phone has nothing to probe",
    );
  }
}

// ---------------------------------------------------------------------------
// UpdateService — the macOS DMG self-updater.
// ---------------------------------------------------------------------------

const UPDATE_REASON =
  "a Capacitor app is updated through TestFlight or the App Store; there is no DMG to mount and no bundle to swap";

export namespace UpdateService {
  /** PLATFORM. */
  export function GetVersion(): Promise<string> {
    return unsupported("UpdateService.GetVersion", UPDATE_REASON);
  }
  /** PLATFORM. */
  export function CheckForUpdates(_manual: boolean): Promise<M.UpdateInfoDTO> {
    return unsupported("UpdateService.CheckForUpdates", UPDATE_REASON);
  }
  /** PLATFORM. */
  export function DownloadUpdate(_url: string): Promise<string> {
    return unsupported("UpdateService.DownloadUpdate", UPDATE_REASON);
  }
  /** PLATFORM. */
  export function InstallAndRestart(_dmg: string): Promise<void> {
    return unsupported("UpdateService.InstallAndRestart", UPDATE_REASON);
  }
  /** PLATFORM. */
  export function ShouldAutoCheck(): Promise<boolean> {
    return unsupported("UpdateService.ShouldAutoCheck", UPDATE_REASON);
  }
  /** PLATFORM. */
  export function IsVersionSkipped(_v: string): Promise<boolean> {
    return unsupported("UpdateService.IsVersionSkipped", UPDATE_REASON);
  }
  /** PLATFORM. */
  export function SkipVersion(_v: string): Promise<void> {
    return unsupported("UpdateService.SkipVersion", UPDATE_REASON);
  }
}

// ---------------------------------------------------------------------------
// The DTO types the generated barrel re-exports. Types only, erased at build.
// ---------------------------------------------------------------------------

// The three that are NOT generated: protocol.PanesData, protocol.PaneInfo and
// protocol.ShellCreateData exist in Go but not in the checked-in bindings, so
// they are mirrored by hand in mobile/src/wire/panes.ts. Re-exported here so a
// caller of DaemonService.Panes can name its return type from the same module
// it called, exactly as it can for every generated one.
// The pane-kind CONSTANTS are deliberately not re-exported with them: this
// module is the services module, and a mobile view that switches on a kind
// imports `@mobile/wire`, which is where the vocabulary and its narrowing guard
// live beside the comment explaining what to do with a kind this build has never
// heard of.
export type {
  PaneCloseData,
  PaneInfo,
  PaneKind,
  PanesData,
  ShellCreateData,
} from "../wire";

export type {
  CLIInfoDTO,
  CLIInstallDTO,
  DoctorReportDTO,
  DoctorResultDTO,
  GroupDTO,
  InheritsDTO,
  LinearKeyStatusDTO,
  LinearOption,
  LinearTeam,
  LinearTeamMeta,
  PathInfoDTO,
  ProjectFormDTO,
  ProjectLayoutDTO,
  ProjectPlacementDTO,
  PushErrDTO,
  ReleaseEntryDTO,
  ReviewKindDTO,
  ReviewProviderDTO,
  SettingsDTO,
  SetupDTO,
  SetupResultDTO,
  UpdateInfoDTO,
  UpdateProgressDTO,
  UpdateSettingsDTO,
} from "@bindings/desktop/models";
