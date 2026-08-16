// The single reactive store. It mirrors the daemon's world into runes state fed
// by the backend's push events (daemon:alive/sessions/projects/status), and wraps
// every daemon command as an action. Components read `store.sessions` etc. and
// call `store.kill(id)` — they never touch the bindings directly.

import { Events } from "@wailsio/runtime";
import { DaemonService, ConfigService, TermService } from "@bindings/desktop";
import type {
  SessionInfo,
  ProjectInfo,
  StatusData,
  Event as ActivityEvent,
  PaneData,
  PrsData,
  TicketsData,
  OpenManualArgs,
  OpenPrArgs,
  OpenTicketArgs,
} from "@bindings/internal/protocol";
import { sortRank } from "./theme";
import { displayName } from "./slug";
import { confirm } from "./confirm.svelte";

type Flash = { text: string; kind: "good" | "warn" | "bad" } | null;

/** Stable session sort: attention first (sortRank), then project, then issue. */
export function sortSessions(list: SessionInfo[]): SessionInfo[] {
  // Coalesce every field the comparator touches: an older daemon can omit a
  // field (→ undefined over the bridge), and a thrown comparator would leave the
  // whole list unsorted/blank.
  return [...list].sort((a, b) => {
    const r = sortRank(a.status ?? "") - sortRank(b.status ?? "");
    if (r !== 0) return r;
    const p = (a.project ?? "").localeCompare(b.project ?? "");
    if (p !== 0) return p;
    return (a.issue ?? "").localeCompare(b.issue ?? "");
  });
}

/**
 * The cockpit's visible session rows: sorted (attention-first) and, when the
 * cockpit is scoped to a project, filtered to it. Shared by Cockpit.svelte and
 * the global keyboard handler so arrow-key movement walks the SAME order the
 * table renders — a second copy of the sort/filter would drift.
 */
export function scopedSessions(list: SessionInfo[], scoped: boolean, project: string): SessionInfo[] {
  const sorted = sortSessions(list);
  return scoped ? sorted.filter((s) => s.project === project) : sorted;
}

/**
 * Recognise the daemon's dirty-worktree refusal in a failed kill.
 *
 * A kill without force keeps a worktree that has uncommitted changes and reports
 * it as an ERROR (internal/daemon/kill.go), so the KillData carrying the path is
 * dropped on the way over the bridge and its message string is all the frontend
 * gets. Matching it is what lets the store re-ask instead of flashing a CLI hint
 * ("rerun with --force") at someone who never typed a command.
 * `internal/daemon/kill_test.go` pins the wording matched here.
 *
 * Returns the kept worktree path (or "" when the message names none), and null
 * when the failure is anything else — those must stay plain errors.
 */
export function dirtyWorktreeRefusal(msg: string): string | null {
  if (!msg.includes("worktree kept (uncommitted changes)") || !msg.includes("--force")) return null;
  // Prefer the delimited capture: a home dir with a space would defeat \S+.
  const m =
    /uncommitted changes\) at (.+?) — rerun with --force/.exec(msg) ??
    /uncommitted changes\) at (\S+)/.exec(msg);
  return m ? m[1] : "";
}

class Store {
  alive = $state(false);
  connected = $state(false); // have we received a first push yet
  hasConfig = $state(true); // assume yes until checked, so no setup-screen flash
  configChecked = $state(false);
  sessions = $state<SessionInfo[]>([]);
  activity = $state<ActivityEvent[]>([]);
  projects = $state<ProjectInfo[]>([]);
  status = $state<StatusData | null>(null);
  flash = $state<Flash>(null);

  // Push-loop command failures keyed by command name. The 2s push path (main.go
  // pushLoop) swallowed per-command errors, so a daemon predating a command
  // (answering `unknown cmd`) silently blanked that read — e.g. Projects → an
  // empty sidebar with no reason. The backend now emits `daemon:pusherr` on a
  // change (a non-empty msg on failure, "" when it recovers), and this holds the
  // current set so a dismissible banner can explain it. Dismissing clears it;
  // a persistent failure is not re-emitted, so it stays dismissed.
  pushErrors = $state<Record<string, string>>({});

  // Sessions whose dev toggle is in flight, so the control can show it. This
  // lives in the STORE rather than in the button because the toggle has three
  // triggers (the session row's button, the context menu, the `D` shortcut) and
  // only one of them is the button — a local flag would leave the other two
  // looking like nothing happened. The call is genuinely slow: the daemon stops
  // the previous holder's tabs and reclaims any port still held in the
  // project's worktrees, each with its own SIGTERM grace, before it answers.
  devPending = $state<Record<string, boolean>>({});

  private flashTimer: ReturnType<typeof setTimeout> | undefined;
  private started = false;

  /** The first live push error, if any — drives the out-of-date banner. */
  pushError = $derived.by(() => {
    for (const cmd of Object.keys(this.pushErrors)) {
      const msg = this.pushErrors[cmd];
      if (msg) return { cmd, msg };
    }
    return null;
  });

  // WKWEBVIEW: `sessions` and `activity` both arrive on the SAME daemon push, but
  // writing them in the SAME synchronous flush corrupts the sessions signal for
  // sibling components in the production WKWebView — verified live: the sessions
  // list stayed empty on startup and the lower terminal never followed the
  // selection WHENEVER any component also read store.activity (the sidebar's
  // Activity feed). Deferring the activity write to its own task puts it in a
  // separate flush and the corruption disappears. A MACROTASK (setTimeout) is required — a
  // microtask still batches with Svelte's flush and does not fix it. The ~1-frame
  // lag on the activity feed is imperceptible. Route EVERY activity write through
  // here; never assign this.activity in the same statement block as this.sessions.
  private setActivity(events: ActivityEvent[]) {
    setTimeout(() => (this.activity = events), 0);
  }

  /** Subscribe to backend push events. Idempotent. */
  start() {
    if (this.started) return;
    this.started = true;
    // Param types are inferred from the registered Wails events; slice fields
    // arrive as T[] | null (Go nil slices), so every read coalesces to [].
    Events.On("daemon:alive", (e) => {
      this.alive = e.data;
      this.connected = true;
      if (!e.data) {
        this.sessions = [];
        this.setActivity([]);
        // A down daemon isn't "out of date"; the offline state covers it, so drop
        // any stale push-error banner. The backend resets its own dedup while
        // down, so a still-out-of-date daemon re-announces on the way back up.
        this.pushErrors = {};
      }
    });
    Events.On("daemon:pusherr", (e) => {
      const cmd = e.data?.cmd ?? "";
      if (!cmd) return;
      const next = { ...this.pushErrors };
      if (e.data?.msg) next[cmd] = e.data.msg;
      else delete next[cmd];
      this.pushErrors = next;
    });
    Events.On("daemon:sessions", (e) => {
      this.sessions = e.data?.sessions ?? [];
      this.connected = true;
      this.setActivity(e.data?.events ?? []);
    });
    Events.On("daemon:projects", (e) => {
      this.projects = e.data?.projects ?? [];
    });
    Events.On("daemon:status", (e) => {
      this.status = e.data;
    });
    // Kick an immediate fetch so the first paint isn't empty for 2s.
    void this.checkConfig();
    void this.refresh();
  }

  async checkConfig() {
    try {
      this.hasConfig = await ConfigService.ConfigExists();
    } catch {
      this.hasConfig = true; // on doubt, don't force the setup screen
    } finally {
      this.configChecked = true;
    }
  }

  projectByName(name: string): ProjectInfo | undefined {
    return this.projects.find((p) => p.name === name);
  }

  /**
   * The human-facing string for a project id: its label when set, else the id.
   *
   * Falls back to the id for an unknown project too, so a session whose
   * [[project]] was removed from config — or a view rendered before the first
   * Projects() response lands — still shows something meaningful.
   */
  displayNameFor(name: string): string {
    const p = this.projectByName(name);
    return p ? displayName(p) : name;
  }

  // Sort straight off `this.sessions` ($state), NOT via a chained class-$derived:
  // reading a derived-of-a-derived across the module boundary went stale in the
  // production WebView (the list stayed empty until a manual re-render forced a
  // flush), while a direct read of the $state field stays live. Sorting is cheap.
  sessionsForProject(name: string): SessionInfo[] {
    return sortSessions(this.sessions).filter((s) => s.project === name);
  }

  sessionById(id: string): SessionInfo | undefined {
    return this.sessions.find((s) => s.id === id);
  }

  setFlash(text: string, kind: "good" | "warn" | "bad" = "good") {
    this.flash = { text, kind };
    clearTimeout(this.flashTimer);
    this.flashTimer = setTimeout(() => (this.flash = null), 4000);
  }

  /** Dismiss the out-of-date banner. A persistent failure is not re-emitted (the
   *  backend dedups), so it stays dismissed until a new/different command fails. */
  dismissPushError() {
    this.pushErrors = {};
  }

  // --- reads ----------------------------------------------------------------

  async refresh() {
    let alive: boolean;
    try {
      alive = await DaemonService.Alive();
    } catch {
      this.alive = false;
      this.connected = true;
      return;
    }
    this.alive = alive;
    this.connected = true;
    if (!alive) return;

    // Settle independently: a daemon that lacks a newer command (e.g. an older
    // build without `projects`) must not blank the reads that DID succeed.
    const [sd, pd, st] = await Promise.allSettled([
      DaemonService.Sessions(),
      DaemonService.Projects(),
      DaemonService.Status(),
    ]);
    if (sd.status === "fulfilled") {
      this.sessions = sd.value.sessions ?? [];
      this.setActivity(sd.value.events ?? []); // separate flush — see setActivity
    }
    if (pd.status === "fulfilled") this.projects = pd.value.projects ?? [];
    if (st.status === "fulfilled") this.status = st.value;
    const rejected = [sd, pd, st].find((r) => r.status === "rejected");
    if (rejected) this.setFlash(String((rejected as PromiseRejectedResult).reason), "warn");
  }

  pane(session: string, lines = 0): Promise<PaneData> {
    return DaemonService.Pane(session, lines);
  }
  prs(project: string, refresh = false): Promise<PrsData> {
    return DaemonService.PRs(project, refresh);
  }
  tickets(project: string, scope = "mine"): Promise<TicketsData> {
    return DaemonService.Tickets(project, scope);
  }

  // --- actions (each flashes its outcome) -----------------------------------

  private async act<T>(fn: () => Promise<T>, ok: string): Promise<T | undefined> {
    try {
      const r = await fn();
      this.setFlash(ok, "good");
      void this.refresh();
      return r;
    } catch (err) {
      this.setFlash(String(err), "bad");
      return undefined;
    }
  }

  answer(session: string, text: string) {
    return this.act(() => DaemonService.Answer(session, text), "answer sent");
  }
  /**
   * Kill a session. A dirty worktree is refused unless force is set — and that
   * refusal is NOT a dead end: the agent is already terminated by then and the
   * worktree is all that survives, so the honest answer is a second question
   * (askForceKill), not a red flash telling a GUI user to rerun a CLI flag.
   */
  async kill(session: string, force = false) {
    // Reap the session's worktree shells too, so they don't linger as orphan tabs
    // once the daemon removes the worktree (best-effort, fire-and-forget).
    void TermService.CloseSessionShells(session).catch(() => {});
    try {
      const r = await DaemonService.Kill(session, force);
      this.setFlash(`killed ${session}`, "good");
      void this.refresh();
      return r;
    } catch (err) {
      const msg = String(err);
      const dir = force ? null : dirtyWorktreeRefusal(msg);
      if (dir === null) {
        this.setFlash(msg, "bad");
        return undefined;
      }
      // The agent IS gone and the daemon has flagged the session dead, so refresh
      // regardless of how the follow-up dialog is answered.
      void this.refresh();
      this.askForceKill(session, dir);
      return undefined;
    }
  }
  revive(session: string) {
    return this.act(() => DaemonService.Revive(session), `revived ${session}`);
  }
  // review forces a QA review PASS. provider optionally selects the pass
  // provider kind (coderabbit-cli | claude-session); "" forces the primary.
  review(session: string, provider = "") {
    return this.act(() => DaemonService.Review(session, provider), "review requested");
  }
  // coderabbit is kept as the back-compat alias forcing the watch kind.
  coderabbit(session: string) {
    return this.act(() => DaemonService.CodeRabbit(session), "coderabbit poll requested");
  }
  /**
   * Run the project's dev_commands here (on = true), or stop them. Only one
   * session per project may hold them, so activating MOVES them: the daemon
   * kills the previous holder's tabs first. The dev TABS follow on their own:
   * SessionEmbed rediscovers a session's tmux tabs every few seconds, and this
   * module deliberately does not import terms (which imports this one).
   */
  async dev(session: string, on: boolean) {
    if (this.devPending[session]) return undefined; // already travelling
    this.devPending[session] = true;
    try {
      return await this.act(
        () => DaemonService.Dev(session, on),
        on ? "dev processes started here" : "dev processes stopped",
      );
    } finally {
      delete this.devPending[session];
    }
  }
  open(project: string, ref: string) {
    return this.act(() => DaemonService.Open(project, ref), `opened ${ref}`);
  }
  openManual(a: OpenManualArgs) {
    return this.act(() => DaemonService.OpenManual(a), `started ${a.branch}`);
  }
  openPr(a: OpenPrArgs) {
    return this.act(() => DaemonService.OpenPR(a), `opened PR #${a.number}`);
  }
  openTicket(a: OpenTicketArgs) {
    return this.act(() => DaemonService.OpenTicket(a), `started ${a.identifier}`);
  }
  // openURL hands a URL to the daemon's opener (which refuses anything that is
  // not http(s)). A failure is FLASHED rather than thrown: the loudest caller is
  // a click on a link inside a terminal, where a rejected promise would be an
  // unhandled rejection and the click would look like it did nothing.
  async openURL(url: string) {
    try {
      await DaemonService.OpenURL(url);
    } catch (err) {
      this.setFlash(String(err), "bad");
    }
  }
  reload() {
    return this.act(() => DaemonService.Reload(), "config reloaded");
  }
  enablePoll(name: string) {
    return this.act(() => DaemonService.Enable(name), `enabled ${name}`);
  }
  disablePoll(name: string) {
    return this.act(() => DaemonService.Disable(name), `disabled ${name}`);
  }

  // --- daemon lifecycle -----------------------------------------------------

  startDaemon() {
    return this.act(() => DaemonService.StartDaemon(), "daemon started");
  }
  stopDaemon() {
    return this.act(() => DaemonService.StopDaemon(), "daemon stopped");
  }

  // --- confirmed (destructive) actions --------------------------------------
  //
  // Every irreversible action routes through `confirm` so it asks the same way,
  // whether it was triggered by a shortcut or a button.

  /** Ask, then kill. Used by the 'x' shortcut and the session panel's button. */
  askKill(id: string) {
    const s = this.sessionById(id);
    const label = s ? s.issue || s.id.slice(0, 8) : id;
    confirm.ask({
      title: "Kill session?",
      body: s?.title ? `Kill ${label} — ${s.title}?` : `Kill ${label}?`,
      detail: "This stops its agent and removes the worktree. Unpushed work is lost.",
      confirmLabel: "Kill",
      onConfirm: () => void this.kill(id),
    });
  }

  /**
   * Second stage of a kill the daemon refused: the agent is stopped but its
   * worktree still holds uncommitted changes. Asking again is the whole point —
   * force is the only way past it, and it destroys work, so it gets its own
   * question with the path spelled out rather than riding on the first "Kill?".
   */
  askForceKill(id: string, dir: string) {
    const s = this.sessionById(id);
    const label = s ? s.issue || s.id.slice(0, 8) : id;
    confirm.ask({
      title: "Worktree has uncommitted changes",
      body: `${label}'s agent is stopped, but its worktree still has uncommitted changes. Delete the worktree anyway?`,
      detail: dir ? `${dir} — the changes there are lost for good.` : "The changes there are lost for good.",
      confirmLabel: "Delete worktree",
      onConfirm: () => void this.kill(id, true),
    });
  }

  /** Ask, then stop the daemon — it halts every poll, so it is not a one-click. */
  askStopDaemon() {
    const live = this.sessions.length;
    confirm.ask({
      title: "Stop the daemon?",
      body: "Stop lola's daemon?",
      detail:
        `Polling stops and no new issues are picked up until it is started again.` +
        (live > 0 ? ` ${live} observed session${live === 1 ? "" : "s"} keep running in tmux.` : ""),
      confirmLabel: "Stop",
      onConfirm: () => void this.stopDaemon(),
    });
  }
  restartDaemon() {
    return this.act(() => DaemonService.RestartDaemon(), "daemon restarted");
  }
}

export const store = new Store();
export type { SessionInfo, ProjectInfo, StatusData, ActivityEvent };
