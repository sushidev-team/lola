// The single reactive store. It mirrors the daemon's world into runes state fed
// by the backend's push events (daemon:alive/sessions/projects/status), and wraps
// every daemon command as an action. Components read `store.sessions` etc. and
// call `store.kill(id)` — they never touch the bindings directly.

import { Events } from "@wailsio/runtime";
import { DaemonService, ConfigService } from "@bindings/desktop";
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

  private flashTimer: ReturnType<typeof setTimeout> | undefined;
  private started = false;

  /** Sessions in the canonical attention-first order. */
  sorted = $derived(sortSessions(this.sessions));

  /** Count of sessions parked on a human. */
  needsYou = $derived(
    this.sessions.filter((s) =>
      ["needs_input", "ci_failed", "changes_requested", "merge_conflict"].includes(s.status),
    ).length,
  );

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
        this.activity = [];
      }
    });
    Events.On("daemon:sessions", (e) => {
      this.sessions = e.data?.sessions ?? [];
      this.activity = e.data?.events ?? [];
      this.connected = true;
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

  sessionsForProject(name: string): SessionInfo[] {
    return this.sorted.filter((s) => s.project === name);
  }

  sessionById(id: string): SessionInfo | undefined {
    return this.sessions.find((s) => s.id === id);
  }

  setFlash(text: string, kind: "good" | "warn" | "bad" = "good") {
    this.flash = { text, kind };
    clearTimeout(this.flashTimer);
    this.flashTimer = setTimeout(() => (this.flash = null), 4000);
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
      this.activity = sd.value.events ?? [];
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
  kill(session: string, force = false) {
    return this.act(() => DaemonService.Kill(session, force), `killed ${session}`);
  }
  revive(session: string) {
    return this.act(() => DaemonService.Revive(session), `revived ${session}`);
  }
  review(session: string) {
    return this.act(() => DaemonService.Review(session), "review requested");
  }
  coderabbit(session: string) {
    return this.act(() => DaemonService.CodeRabbit(session), "coderabbit poll requested");
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
  openURL(url: string) {
    return DaemonService.OpenURL(url);
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
  restartDaemon() {
    return this.act(() => DaemonService.RestartDaemon(), "daemon restarted");
  }
}

export const store = new Store();
export type { SessionInfo, ProjectInfo, StatusData, ActivityEvent };
