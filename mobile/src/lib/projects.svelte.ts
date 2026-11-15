// The models behind the Projects tab: the project detail's actions, the PR
// picker and the ticket picker (spec §5.2).
//
// Invariants:
//  - Nothing here writes config.toml. The only commands are the ones the daemon
//    lets a phone send, and each is offered only when the device's tiers hold it.
//  - A refusal is shown as the daemon worded it. Classifying by message text
//    would mislabel the day the daemon rephrases it.
//  - One spawn at a time: the daemon answers once tmux, git and the agent have
//    started, long enough for a second tap to spawn a second session.
//  - A response that is no longer the newest request never writes.

import type { PrRow, PrsData, ProjectInfo, TicketRow, TicketsData } from "@bindings/internal/protocol";
import { store } from "$lib/store.svelte";
import { fluidAgo } from "$lib/time";
import { DaemonService } from "@mobile/wailsshim";
import { minuteClock } from "./clock.svelte";
import { copy, fill } from "./copy";
import { router } from "./router.svelte";
import { can } from "./tiers.svelte";

/** An Error's own words, else the fallback. Never "[object Object]". */
export function errorMessage(e: unknown, fallback: string): string {
  return e instanceof Error && e.message ? e.message : fallback;
}

/** A phone-rendered age: reads the shared minute clock, so it stays live. */
export function ageOf(at: number | undefined): string {
  return at === undefined ? "" : fluidAgo(at, minuteClock.now).text;
}

/** Characters kept at each end of a branch name that is too long for one line. */
export const BRANCH_KEEP = 12;

/** A long branch cut in the middle (first 12 and last 12 characters), so both its prefix and its tail stay readable. */
export function middleEllipsis(s: string, keep = BRANCH_KEEP): string {
  const chars = Array.from(s);
  return chars.length <= keep * 2 + 1 ? s : `${chars.slice(0, keep).join("")}…${chars.slice(-keep).join("")}`;
}

/** The project's sessions shown before "Show all". */
export const SESSIONS_SHOWN = 6;


export type ActionId = "prs" | "tickets" | "worktree" | "pollOnce" | "polling";

export interface ProjectAction {
  id: ActionId;
  label: string;
  enabled: boolean;
  /** Why it is off, said in the row. */
  hint?: string;
}

/**
 * The detail's action rows. A row whose command the device's tiers lack is not
 * drawn; a row the Mac's config rules out is drawn off with its reason.
 */
export function projectActions(
  p: ProjectInfo | undefined,
  allowed: (cmd: string) => boolean = can,
): ProjectAction[] {
  if (!p) return [];
  const polls = (p.pollCount ?? 0) > 0;
  const polling = (p.pollsEnabled ?? 0) > 0;
  const rows: (ProjectAction & { cmd: string })[] = [
    {
      id: "prs",
      cmd: "openPr",
      label: copy.projects.actions.pr,
      enabled: p.agentOk && p.repoConfigured,
      hint: !p.repoConfigured ? copy.projects.noRepo.body : copy.projects.agentDown.body,
    },
    {
      id: "tickets",
      cmd: "openTicket",
      label: copy.projects.actions.ticket,
      enabled: p.agentOk,
      hint: copy.projects.agentDown.body,
    },
    {
      id: "worktree",
      cmd: "openManual",
      label: copy.projects.actions.worktree,
      enabled: p.agentOk,
      hint: copy.projects.agentDown.body,
    },
    { id: "pollOnce", cmd: "pollOnce", label: copy.projects.actions.pollOnce, enabled: polls, hint: copy.projects.noFilter.body },
    {
      id: "polling",
      cmd: polling ? "disable" : "enable",
      label: polling ? copy.projects.actions.pollStop : copy.projects.actions.pollStart,
      enabled: polls,
      hint: copy.projects.noFilter.body,
    },
  ];
  return rows.filter((r) => allowed(r.cmd)).map(({ cmd: _cmd, ...row }) => row);
}

export type Notice = { text: string; tone: "good" | "warn" };

/** Runs one daemon action at a time and keeps its outcome as a notice. */
export class ActionRunner {
  busy = $state("");
  notice = $state<Notice | null>(null);

  async run(id: string, fn: () => Promise<string>): Promise<void> {
    if (this.busy !== "") return;
    this.busy = id;
    this.notice = null;
    try {
      this.notice = { text: await fn(), tone: "good" };
      await store.refresh();
    } catch (e) {
      this.notice = { text: errorMessage(e, copy.projects.refused.body), tone: "warn" };
    } finally {
      this.busy = "";
    }
  }

  clear(): void {
    this.notice = null;
  }
}

/** The poll commands, as the sentence each one earns. */
export async function pollNow(project: string): Promise<string> {
  const n = (await DaemonService.PollOnce(project, false))?.matches?.length ?? 0;
  return n === 1
    ? copy.projects.polledOne.body
    : fill(copy.projects.polledMany.body, { n });
}

/** `enable`/`disable` take a poll name, which for a project is the project's own. */
export async function setPolling(project: string, on: boolean): Promise<string> {
  if (on) {
    await DaemonService.Enable(project);
    return copy.projects.pollOn.body;
  }
  await DaemonService.Disable(project);
  return copy.projects.pollOff.body;
}

/** Leaves a picker for the session it just opened; the project stays under it. */
function showSession(project: string, id: string): void {
  router.replace({ name: "tab", tab: "projects", project });
  if (id) router.push({ name: "session", id, seg: "conversation" });
}

/** Open pull requests of one project's repository. */
export class PrPickerModel {
  data = $state<PrsData | null>(null);
  /** When the daemon's snapshot was taken, in epoch ms. */
  snapshotAt = $state<number | undefined>();
  loading = $state(true);
  error = $state("");
  refusal = $state("");
  /** The PR whose agent is launching, 0 for none. */
  opening = $state(0);

  #seq = 0;

  rows = $derived(this.data?.prs ?? []);

  /** `refresh` bypasses the daemon's TTL; the previous list stays on screen meanwhile. */
  async load(project: string, refresh = false): Promise<void> {
    const mine = ++this.#seq;
    this.loading = true;
    this.error = "";
    try {
      const d = await store.prs(project, refresh);
      if (mine !== this.#seq) return;
      this.data = d;
      this.snapshotAt = Date.now() - d.ageSeconds * 1000;
    } catch (e) {
      if (mine !== this.#seq) return;
      this.error = errorMessage(e, copy.projects.prs.failed.body);
    } finally {
      if (mine === this.#seq) this.loading = false;
    }
  }

  /**
   * Puts an agent on the PR's branch. The daemon is asked even when `alreadyOpen`
   * or `isFork` predicts a refusal: the flags decorate a cached list, and the
   * refusal names the session that holds the branch.
   */
  async open(project: string, p: PrRow): Promise<void> {
    if (this.opening !== 0) return;
    this.opening = p.number;
    this.refusal = "";
    try {
      const r = await DaemonService.OpenPR({
        project,
        branch: p.branch,
        number: p.number,
        isFork: p.isFork,
      });
      void store.refresh();
      showSession(project, r?.sessionId ?? "");
    } catch (e) {
      this.refusal = errorMessage(e, copy.projects.refused.body);
    } finally {
      this.opening = 0;
    }
  }
}

export type TicketScope = "mine" | "team";

/** Linear issues of one project's team, in the daemon's pick order. */
export class TicketPickerModel {
  scope = $state<TicketScope>("mine");
  data = $state<TicketsData | null>(null);
  loading = $state(true);
  error = $state("");
  refusal = $state("");
  /** The identifier whose spawn is in flight, or "". */
  starting = $state("");

  #seq = 0;

  rows = $derived(this.data?.issues ?? []);

  /** The team's name and key; the UUID is never a fallback. */
  team = $derived.by(() => {
    const d = this.data;
    if (!d) return "";
    if (d.teamName && d.teamKey) return `${d.teamName} (${d.teamKey})`;
    return d.teamName || d.teamKey || "";
  });

  async load(project: string): Promise<void> {
    if (project === "") return;
    const mine = ++this.#seq;
    this.loading = true;
    this.error = "";
    try {
      const d = await DaemonService.Tickets(project, this.scope);
      if (mine !== this.#seq) return;
      this.data = d;
    } catch (e) {
      if (mine !== this.#seq) return;
      this.error = errorMessage(e, copy.projects.tickets.failed.body);
      this.data = null;
    } finally {
      if (mine === this.#seq) this.loading = false;
    }
  }

  setScope(project: string, scope: TicketScope): void {
    if (scope === this.scope) return;
    this.refusal = "";
    this.scope = scope;
    void this.load(project);
  }

  /**
   * Turns one issue into a running session. A row the daemon already runs opens
   * that session instead of earning the refusal "already being worked on".
   */
  async start(project: string, t: TicketRow): Promise<void> {
    if (t.alreadyLive) {
      const live = store.sessions.find((s) => s.project === project && s.issue === t.identifier);
      if (live) showSession(project, live.id);
      return;
    }
    if (this.starting !== "") return;
    this.starting = t.identifier;
    this.refusal = "";
    try {
      const r = await DaemonService.OpenTicket({
        project,
        identifier: t.identifier,
        uuid: t.uuid,
        branch: t.branch,
        title: t.title,
      });
      void store.refresh();
      showSession(project, r?.sessionId ?? "");
    } catch (e) {
      this.refusal = errorMessage(e, copy.projects.refused.body);
    } finally {
      this.starting = "";
    }
  }
}
