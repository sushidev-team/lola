// Builders for fixture data: every row is complete, so a screen never meets a
// field the daemon always sends and a fixture forgot.

import type { PairedDaemon } from "lola-transport";
import type {
  AgentFeedItem,
  AgentPrompt,
  DiffData,
  DiffFile,
  Event as ActivityEvent,
  ProjectInfo,
  SessionInfo,
  StatusData,
  TalkStateData,
} from "@bindings/internal/protocol";
import type { Fixture } from "./types";

const MIN = 60_000;

/** An ISO timestamp this many minutes before now. */
export function ago(minutes: number): string {
  return new Date(Date.now() - minutes * MIN).toISOString();
}

/** The phone-facing age the daemon would print for a minute count. */
export function ageText(minutes: number): string {
  if (minutes < 60) return `${minutes}m`;
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  return m === 0 ? `${h}h` : `${h}h${String(m).padStart(2, "0")}m`;
}

/** The legacy rollup word for a pair; display-only, the pair is what screens read. */
function rollup(agentState: string, delivery: string): string {
  if (agentState === "waiting_input") return "needs_input";
  if (agentState === "dead" || agentState === "exited") return delivery === "merged" ? "merged" : "dead";
  return delivery && delivery !== "none" ? delivery : agentState;
}

export interface SessionSeed {
  id: string;
  title: string;
  issue?: string;
  project?: string;
  agentState?: string;
  delivery?: string;
  inputReason?: string;
  headline?: string;
  minutes?: number;
  pr?: number;
  checks?: string;
  review?: string;
  reacting?: string;
  /** Agent-reported local checks: [name, state] with state pass | fail. */
  board?: ReadonlyArray<readonly [string, string]>;
}

export function session(s: SessionSeed): SessionInfo {
  const project = s.project ?? "nori-app";
  const agentState = s.agentState ?? "working";
  const delivery = s.delivery ?? (s.pr ? "ci_pending" : "none");
  const minutes = s.minutes ?? 12;
  return {
    id: s.id,
    project,
    issue: s.issue ?? "",
    title: s.title,
    branch: s.issue ? `lola/${s.issue.toLowerCase()}` : `lola/${s.id}`,
    agent: "claude",
    status: rollup(agentState, delivery),
    prUrl: s.pr ? `https://github.com/sushidev-team/${project}/pull/${s.pr}` : "",
    prNumber: s.pr ?? 0,
    checks: s.checks ?? "",
    review: s.review ?? "",
    tmuxName: `lola-${s.id}`,
    source: "linear",
    worktree: `/Users/dev/.lola/worktrees/${project}/${s.id}`,
    age: ageText(minutes),
    firstSeen: ago(minutes),
    agentState,
    delivery,
    agentStateSince: ago(minutes),
    inputReason: s.inputReason,
    lastNotification: s.headline,
    headline: s.headline,
    atPrompt: agentState === "idle" || agentState === "waiting_input",
    ciRetries: 0,
    escalated: false,
    reacting: s.reacting ?? "",
    board: s.board
      ? {
          percent: 0,
          done: s.board.filter(([, state]) => state === "pass").length,
          total: s.board.length,
          checks: s.board.map(([name, state]) => ({ name, state })),
          updatedAt: ago(Math.min(minutes, 3)),
        }
      : undefined,
  };
}

export function project(name: string, label: string, over: Partial<ProjectInfo> = {}): ProjectInfo {
  return {
    name,
    label,
    path: `/Users/dev/code/${name}`,
    repo: `sushidev-team/${name}`,
    defaultBranch: "main",
    agent: "claude",
    agentBin: "/usr/local/bin/claude",
    agentOk: true,
    pathOk: true,
    repoConfigured: true,
    pollCount: 1,
    pollsEnabled: 1,
    lastRun: "2m",
    sessions: 0,
    liveCounted: 0,
    needsYou: 0,
    ciRed: 0,
    openPrs: 0,
    ...over,
  };
}

export const PROJECTS: ProjectInfo[] = [
  project("nori-app", "Nori App"),
  project("okane", "Okane"),
];

/** Project counters follow the sessions, as the daemon computes them. */
export function withCounts(projects: readonly ProjectInfo[], sessions: readonly SessionInfo[]): ProjectInfo[] {
  return projects.map((p) => {
    const mine = sessions.filter((s) => s.project === p.name);
    return {
      ...p,
      sessions: mine.length,
      liveCounted: mine.filter((s) => s.agentState !== "dead" && s.agentState !== "exited").length,
      needsYou: mine.filter((s) => s.agentState === "waiting_input").length,
      ciRed: mine.filter((s) => s.delivery === "ci_failed").length,
      openPrs: mine.filter((s) => s.prNumber > 0 && s.delivery !== "merged").length,
    };
  });
}

export const STATUS: StatusData = {
  runtimeOk: true,
  linearOk: true,
  polls: [{ name: "nori-app", enabled: true, lastRun: "2m", lastSpawn: "1h", running: false }],
  host: "mars.local",
};

export const MAC: PairedDaemon = {
  daemonId: "mars-daemon-id",
  name: "Mars Mac",
  pairedAt: Date.now() - 3 * 24 * 60 * MIN,
  relay: "ok",
  secureEnclave: true,
  lastPath: "direct",
};

/** Every tier a phone can be granted; the daemon's `never` is never granted. */
export const ALL_TIERS = ["read", "answer", "control", "destructive", "shell"] as const;

export function event(id: string, issue: string, from: string, to: string, minutes: number, title = ""): ActivityEvent {
  return { id, issue, title, from, to, ago: ageText(minutes), at: ago(minutes) };
}

/** The shape every fixture starts from: one paired, connected Mac with nothing on it. */
export function base(name: Fixture["name"], over: Partial<Fixture> = {}): Fixture {
  return {
    name,
    routes: [],
    daemons: [MAC],
    tiers: ALL_TIERS,
    path: { kind: "direct", host: "mars.local", port: 7717, interface: "wifi" },
    link: { kind: "up" },
    sessions: [],
    projects: withCounts(PROJECTS, over.sessions ?? []),
    status: STATUS,
    events: [],
    talk: {},
    feeds: {},
    timelines: {},
    diffs: {},
    checkpoints: {},
    packages: {},
    panes: {},
    screens: {},
    readiness: {},
    prs: { repo: "sushidev-team/nori-app", prs: [], ageSeconds: 0, stale: false },
    tickets: { team: "team-nori", teamName: "Nori", teamKey: "NOR", issues: [] },
    storage: {},
    ...over,
  };
}

// --- talk and feed ---------------------------------------------------------

export function talk(id: string, over: Partial<TalkStateData> = {}): TalkStateData {
  return {
    session: id,
    caps: { settings: true, feed: true, send: true, stop: true, cards: true },
    composer: "",
    feedSeq: 0,
    feedEpoch: "e1",
    health: "ok",
    ...over,
  };
}

export function permissionPrompt(lines: string[]): AgentPrompt {
  return {
    kind: "permission",
    hash: "perm-0001",
    lines,
    choices: [
      { key: "1", label: "Yes" },
      { key: "2", label: "Yes, and don't ask again" },
      { key: "3", label: "No" },
    ],
    since: ago(2),
  };
}

export function questionPrompt(): AgentPrompt {
  return {
    kind: "question",
    hash: "ques-0001",
    lines: ["Which period should the balance chart cover?"],
    choices: [
      { key: "1", label: "Current month", detail: "Matches the due-date reminders" },
      { key: "2", label: "Last 30 days" },
      { key: "3", label: "Current quarter" },
      { key: "4", label: "Let me type my own" },
    ],
    since: ago(1),
  };
}

function item(i: number, over: Partial<AgentFeedItem> & Pick<AgentFeedItem, "kind">): AgentFeedItem {
  return { id: `f${i}`, order: i, seq: i + 1, at: ago(300 - i), ...over };
}

export function userItem(i: number, text: string, turn: string): AgentFeedItem {
  return item(i, { kind: "user", text, turn });
}

export function textItem(i: number, text: string, turn: string): AgentFeedItem {
  return item(i, { kind: "text", text, turn });
}

export function toolItem(
  i: number,
  turn: string,
  tool: { name: string; category: string; summary: string; status?: string; added?: number; removed?: number; subagent?: string },
  parent?: string,
): AgentFeedItem {
  return item(i, {
    kind: "tool",
    turn,
    parent,
    tool: { status: "ok", durationMs: 400 + (i % 7) * 310, ...tool },
  });
}

const FILES = ["src/Period.svelte", "src/lib/balance.ts", "src/lib/due.ts", "src/Reminders.svelte", "tests/balance.test.ts"];

/** A conversation of `count` items: user turns, replies, tool rows and one subagent run. */
export function longStory(count: number): AgentFeedItem[] {
  const out: AgentFeedItem[] = [];
  let turn = "";
  while (out.length < count) {
    const i = out.length;
    if (i % 10 === 0) {
      turn = `t${i / 10}`;
      out.push(userItem(i, `Step ${i / 10 + 1}: tighten the accounting period view and keep the tests green.`, turn));
    } else if (i % 10 === 9) {
      out.push(textItem(i, `Done with step ${turn.slice(1)}. The period view reads from one place now and the suite passes.`, turn));
    } else if (i === 25) {
      out.push(toolItem(i, turn, { name: "Task", category: "agent", summary: "Audit reminder rules", subagent: "reviewer" }));
    } else if (i > 25 && i < 33) {
      out.push(toolItem(i, turn, { name: "Read", category: "read", summary: FILES[i % FILES.length] }, "f25"));
    } else if (i % 10 === 4) {
      out.push(textItem(i, "Reading the call sites first, then changing the shared helper.", turn));
    } else {
      const edit = i % 3 === 0;
      out.push(
        edit
          ? toolItem(i, turn, { name: "Edit", category: "edit", summary: FILES[i % FILES.length], added: 4 + (i % 9), removed: i % 5 })
          : toolItem(i, turn, { name: i % 4 === 1 ? "Bash" : "Read", category: i % 4 === 1 ? "run" : "read", summary: i % 4 === 1 ? "npm test -- --run" : FILES[i % FILES.length] }),
      );
    }
  }
  return out;
}

/** A short conversation that ends where the agent is waiting on the person. */
export function shortStory(last: string): AgentFeedItem[] {
  return [
    userItem(0, "Remove the chart from the accounting period view and keep the due dates.", "t0"),
    textItem(1, "I'll start with the chart component and its imports.", "t0"),
    toolItem(2, "t0", { name: "Read", category: "read", summary: "src/Period.svelte" }),
    toolItem(3, "t0", { name: "Edit", category: "edit", summary: "src/Period.svelte", added: 3, removed: 41 }),
    toolItem(4, "t0", { name: "Bash", category: "run", summary: "npm test -- --run", status: "ok" }),
    textItem(5, last, "t0"),
  ];
}

// --- diff ------------------------------------------------------------------

function patch(lines: number, seed: number): string {
  const body: string[] = [`@@ -1,${lines} +1,${lines + 2} @@`];
  for (let n = 0; n < lines; n++) {
    const k = (n + seed) % 6;
    if (k === 2) body.push(`-  const total${n} = legacy(${n});`);
    else if (k === 3) body.push(`+  const total${n} = balance(${n}, period);`);
    else if (k === 4 && n % 12 === 4) body.push(`+  // due dates follow the period`);
    else body.push(`   const row${n} = rows[${n}];`);
  }
  return body.join("\n");
}

/** A diff of `files` files and about `lines` patch lines in all. */
export function diff(session: string, files: number, lines: number): DiffData {
  const per = Math.max(2, Math.floor(lines / files));
  const out: DiffFile[] = [];
  for (let f = 0; f < files; f++) {
    const p = patch(per, f);
    const rows = p.split("\n");
    out.push({
      path: `src/${f % 2 === 0 ? "lib" : "components"}/module${String(f).padStart(2, "0")}.ts`,
      status: f % 9 === 0 ? "added" : "modified",
      additions: rows.filter((r) => r.startsWith("+")).length,
      deletions: rows.filter((r) => r.startsWith("-")).length,
      patch: p,
    });
  }
  return { session, base: "main", mergeBase: "9f3c2ab", files: out };
}
