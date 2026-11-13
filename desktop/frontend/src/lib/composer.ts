// The composer's decisions: where a line goes, what its footer says, and which cards wait above
// it. Pure, with one test per row of the footer table, so what ↩ does can be read here and
// nowhere else. The components draw what this answers.

import type { AgentPrompt, TalkStateData } from "@bindings/internal/protocol";
import {
  dockCards,
  hostName,
  isEnded,
  runPrompt,
  type DockCard,
  type SSHConnInfo,
  type SSHElement,
  type SSHPromptInfo,
  type SSHTimelineItem,
} from "./ssh";
import { toMillis } from "./time";

// --- where a Run goes -----------------------------------------------------------

/** A host a command could go to now. One that has only asked, or has ended, is not live. */
export function isLiveHost(c: Pick<SSHConnInfo, "state">): boolean {
  return !isEnded(c) && c.state !== "requested" && c.state !== "denied";
}

export type RunTarget = {
  /** Where a Run goes, undefined when there is nowhere it may go. */
  conn?: SSHConnInfo;
  /** none: this session has no host. pick: several are live, and choosing is a human act. */
  why: "" | "none" | "pick";
};

/**
 * A host in scope, or the only live one. Under All with several live, a Run has no target,
 * because choosing a production target must be a person's act and never a default.
 */
export function runTarget(conns: readonly SSHConnInfo[], scope: string): RunTarget {
  const scoped = conns.find((c) => c.id === scope && c.state !== "denied");
  if (scoped) return { conn: scoped, why: "" };
  const live = conns.filter(isLiveHost);
  if (live.length === 1) return { conn: live[0], why: "" };
  return { why: live.length ? "pick" : "none" };
}

export const RUN_NEEDS_HOST = "Run needs a host. ⌘T opens a shell.";
export const RUN_PICK_HOST = "Pick a host above to run.";

/** The history a field walks. Keyed by destination, so ↑ cannot recall staging into production. */
export const AGENT_DEST = "agent";

// --- the state on the left -----------------------------------------------------

export type ComposerState =
  | "down"
  | "gone"
  | "modal"
  | "card"
  | "quota"
  | "held"
  | "working"
  | "draft"
  | "resting"
  | "pending"
  | "unproven";

export type StateInput = {
  alive: boolean;
  talk: Pick<TalkStateData, "caps" | "composer" | "prompt" | "queueHeld"> | null;
  agentState?: string;
  inputReason?: string;
  /** A tier 1 card waits in the tray. */
  cardWaiting: boolean;
  held: number;
};

/**
 * The fresh pane read decides, and the session's axis only fills in what that read cannot say:
 * before the first answer, and for an agent lola has no read for.
 */
export function composerState(i: StateInput): ComposerState {
  if (!i.alive) return "down";
  const word = i.talk?.composer;
  const axis = i.agentState ?? "";
  if (word === "gone" || axis === "exited" || axis === "dead") return "gone";
  if (word === "modal" || i.talk?.prompt?.kind === "modal") return "modal";
  if (i.cardWaiting) return "card";
  if (i.inputReason === "quota_limited") return "quota";
  if (i.talk?.queueHeld && i.held > 0) return "held";
  if (word === "working") return "working";
  if (word === "draft") return "draft";
  if (word === "resting" && i.talk?.caps?.send) return "resting";
  if (axis === "working" || axis === "starting") return "working";
  return i.talk ? "unproven" : "pending";
}

// --- the footer ----------------------------------------------------------------

export type FooterAct = "send" | "queue" | "run" | "ask" | "revive" | "stopSend" | "sendHeld";

export type FooterButton = {
  act: FooterAct;
  label: string;
  keys?: string;
  disabled?: boolean;
  variant: "primary" | "secondary";
};

/** A control beside the state, because it acts on what the state just said. */
export type FooterLeftAct = "stop" | "terminal" | "sendThem" | "handBack" | "switchAgent";

export type Footer = {
  state: ComposerState;
  glyph: string;
  glyphCls: string;
  text: string;
  textCls: string;
  /** The left is a machine fact, a user and a folder, rather than a sentence. */
  mono?: boolean;
  /** The turn timer goes after the text. */
  clock?: boolean;
  /** The left says something a person may need to act on, or a turn in progress. */
  pulse?: boolean;
  left: FooterLeftAct[];
  /** A faint line that is not a button: there is nothing to press until a host is picked. */
  hint?: string;
  secondary: FooterButton[];
  primary: FooterButton;
};

export type FooterInput = {
  state: ComposerState;
  agent: string;
  /** The field holds something a send could take. */
  hasText: boolean;
  /** The field is over the size limit, so nothing can send. */
  over?: boolean;
  canStop: boolean;
  canSend?: boolean;
  held?: number;
  /** "14:00", "" when lola does not know. */
  resets?: string;
  /** The field is in its run look. */
  running: boolean;
  /** ↩ runs and ⌘↩ asks, after a double ⌘. */
  runsByDefault?: boolean;
  target?: { name: string; prompt: string };
  /** Several hosts are live under All, so a Run has no target. */
  pick?: boolean;
  /** A host the human holds the wheel of. */
  driving?: string;
};

const STATE: Record<ComposerState, { glyph: string; glyphCls: string; textCls: string }> = {
  down: { glyph: "○", glyphCls: "text-faint", textCls: "text-ink-2" },
  gone: { glyph: "✕", glyphCls: "text-faint", textCls: "text-ink-2" },
  // The card above says it in orange. The glyph is enough here, or the footer repeats it in a sentence.
  modal: { glyph: "◉", glyphCls: "text-orange", textCls: "text-ink-2" },
  card: { glyph: "◉", glyphCls: "text-orange", textCls: "text-ink-2" },
  quota: { glyph: "◉", glyphCls: "text-orange", textCls: "text-orange" },
  held: { glyph: "○", glyphCls: "text-faint", textCls: "text-ink-2" },
  working: { glyph: "◐", glyphCls: "text-info", textCls: "text-ink-2" },
  draft: { glyph: "○", glyphCls: "text-faint", textCls: "text-ink-2" },
  resting: { glyph: "○", glyphCls: "text-faint", textCls: "text-ink-2" },
  pending: { glyph: "○", glyphCls: "text-faint", textCls: "text-ink-2" },
  unproven: { glyph: "○", glyphCls: "text-faint", textCls: "text-ink-2" },
};

export const DAEMON_DOWN_LINE = "Daemon down. Your draft is kept and nothing is queued.";

/** Exactly the table in section 2.6, so a row can be checked against it by eye. */
export function footerFor(i: FooterInput): Footer {
  const askKey = i.runsByDefault ? "⌘↩" : "↩";
  const runKey = i.runsByDefault ? "↩" : "⌘↩";
  const blocked = !i.hasText || !!i.over;

  const look = STATE[i.state];
  let text = "";
  let left: FooterLeftAct[] = [];
  let clock = false;
  let word: "Send" | "Queue" = "Queue";
  let primaryVariant: "primary" | "secondary" = "primary";

  switch (i.state) {
    case "down":
      text = DAEMON_DOWN_LINE;
      left = ["terminal"];
      word = "Send";
      break;
    case "gone":
      text = `${i.agent} exited`;
      break;
    case "modal":
      text = "A screen is open in the terminal";
      // The card above is the way there. A second button right under it says nothing new.
      primaryVariant = "secondary";
      break;
    case "card":
      text = "Waiting on the card";
      primaryVariant = "secondary";
      break;
    case "quota":
      text = `Usage limit${i.resets ? ` · resets ${i.resets}` : ""}`;
      left = ["switchAgent"];
      break;
    case "held":
      text = `Stopped · ${i.held ?? 0} held`;
      // Two sends at once is the one thing this row must not offer. An empty field has one thing
      // left to send, so that is the primary. With text in it, ↩ sends the text and this stays beside.
      if (i.hasText) left = ["sendThem"];
      word = "Send";
      break;
    case "working":
      text = "Working";
      word = i.canSend === false ? "Queue" : "Send";
      clock = true;
      if (i.canStop) left = ["stop"];
      break;
    case "draft":
      text = `${i.agent}'s own prompt has text`;
      left = ["terminal"];
      break;
    case "resting":
      text = "Resting";
      word = "Send";
      break;
    case "pending":
      text = "Resting";
      break;
    case "unproven":
      text = "lola can't confirm this agent's prompt is free";
      left = ["terminal"];
      primaryVariant = "secondary";
      break;
  }

  // Run mode takes the whole footer: the field is about to go to a machine.
  if (i.running && i.target && i.state !== "down") {
    return {
      state: i.state,
      glyph: "",
      glyphCls: "",
      text: i.target.prompt,
      textCls: "text-faint",
      mono: true,
      left: [],
      secondary: [{ act: "ask", label: "Ask", keys: askKey, disabled: blocked, variant: "secondary" }],
      primary: { act: "run", label: `Run on ${i.target.name}`, keys: runKey, disabled: blocked, variant: "primary" },
    };
  }

  if (i.driving && i.state !== "down" && i.state !== "gone") {
    text = `You're driving ${i.driving}`;
    left = ["handBack"];
    clock = false;
  }

  const secondary: FooterButton[] = [];
  if (i.state === "working" && i.canStop && i.hasText) secondary.push({ act: "stopSend", label: "Stop & send", disabled: !!i.over, variant: "secondary" });
  if (i.state === "gone") secondary.push({ act: "queue", label: "Queue", keys: askKey, disabled: blocked, variant: "secondary" });
  if (i.target && i.state !== "down") secondary.push({ act: "run", label: `Run on ${i.target.name}`, keys: runKey, disabled: blocked, variant: "secondary" });

  const primary: FooterButton =
    i.state === "gone"
      ? { act: "revive", label: "Revive", variant: "primary" }
      : i.state === "held" && !i.hasText
        ? { act: "sendHeld", label: "Send them", keys: askKey, variant: "primary" }
      : {
          act: word === "Send" ? "send" : "queue",
          label: word,
          keys: askKey,
          disabled: blocked || i.state === "down",
          variant: primaryVariant,
        };

  return {
    state: i.state,
    glyph: look.glyph,
    glyphCls: look.glyphCls,
    text,
    textCls: i.driving && i.state !== "down" && i.state !== "gone" ? "text-ink-2" : look.textCls,
    clock,
    pulse: i.state === "working" && !i.driving,
    left,
    hint: i.pick && i.state !== "down" ? "Pick a host above to run" : undefined,
    secondary,
    primary,
  };
}

/** The footer's words for a host, from its connection. */
export function targetLabel(c: SSHConnInfo | undefined): { name: string; prompt: string } | undefined {
  return c ? { name: hostName(c), prompt: runPrompt(c) } : undefined;
}

// --- the tray ------------------------------------------------------------------

export type TrayCard =
  | (DockCard & { tier: 1; at: number })
  | { kind: "auth"; id: string; tier: 1; at: number; conn: SSHConnInfo }
  | { kind: "prompt"; id: string; tier: 1; at: number; conn: SSHConnInfo; prompt: SSHPromptInfo }
  | { kind: "permission" | "question" | "modal"; id: string; tier: 1; at: number; prompt: AgentPrompt }
  | { kind: "background"; id: string; tier: 2; at: number; prompt: AgentPrompt }
  | { kind: "element"; id: string; tier: 2; at: number; element: SSHElement };

export type TrayInput = {
  connections: readonly SSHConnInfo[];
  items: readonly SSHTimelineItem[];
  activeConn: string;
  /** Prompts already answered, so a push still carrying one cannot bring its card back. */
  answered?: { has(id: string): boolean };
  agentPrompt?: AgentPrompt | null;
};

/** A card with no known time sorts after the dated ones, so it can never jump ahead of them. */
function when(...times: (string | undefined)[]): number {
  for (const t of times) {
    const ms = toMillis(t);
    if (Number.isFinite(ms) && ms > 0) return ms;
  }
  return Number.MAX_SAFE_INTEGER;
}

function agentPromptKind(p: AgentPrompt): "permission" | "question" | "modal" {
  if (p.kind === "modal") return "modal";
  return p.kind === "permission" ? "permission" : "question";
}

/**
 * Everything that needs a person, oldest first and stable. All hosts count, whatever the scope,
 * because a scope is a filter on the story and a card is not part of the story. Tier 2 follows
 * and is never counted, as it is the agent's own claim that it wants something.
 */
export function trayCards(i: TrayInput): TrayCard[] {
  const conns = i.connections.filter((c) => c.state !== "denied");
  const itemAt = new Map(i.items.map((it) => [it.block?.id ?? "", it.at as unknown as string | undefined]));
  const tier1: TrayCard[] = [];

  for (const d of dockCards(conns as SSHConnInfo[], i.items as SSHTimelineItem[], "")) {
    if (d.kind === "approval") tier1.push({ ...d, tier: 1, at: when(d.block.createdAt as unknown as string, itemAt.get(d.block.id)) });
    if (d.kind === "go") tier1.push({ ...d, tier: 1, at: when(d.conn.requestedAt as unknown as string) });
  }
  for (const c of conns) {
    const p = c.prompt && !i.answered?.has(c.prompt.id) ? c.prompt : undefined;
    if (p) tier1.push({ kind: "prompt", id: `prompt-${p.id}`, tier: 1, at: when(p.since as unknown as string), conn: c, prompt: p });
    else if (c.state === "authenticating") tier1.push({ kind: "auth", id: `auth-${c.id}`, tier: 1, at: when(c.requestedAt as unknown as string), conn: c });
    if (c.state === "disconnected") {
      const running = i.items.filter((it) => it.block?.conn === c.id && it.block?.status === "lost").length;
      tier1.push({ kind: "reconnect", id: `reconnect-${c.id}`, tier: 1, at: when(c.closedAt as unknown as string), conn: c, running });
    }
    if (c.state === "failed") tier1.push({ kind: "failed", id: `failed-${c.id}`, tier: 1, at: when(c.closedAt as unknown as string), conn: c });
  }
  if (i.agentPrompt && i.agentPrompt.kind !== "background") {
    const p = i.agentPrompt;
    tier1.push({ kind: agentPromptKind(p), id: `agent-${p.hash}`, tier: 1, at: when(p.since as unknown as string), prompt: p });
  }
  tier1.sort((a, b) => a.at - b.at || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));

  const tier2: TrayCard[] = [];
  if (i.agentPrompt?.kind === "background") {
    const p = i.agentPrompt;
    tier2.push({ kind: "background", id: `agent-${p.hash}`, tier: 2, at: when(p.since as unknown as string), prompt: p });
  }
  for (const it of i.items) {
    const e = it.element;
    if (!e) continue;
    const open = (e.kind === "question" && e.state !== "answered") || (e.kind === "proposal" && e.state !== "accepted" && e.state !== "declined");
    if (open) tier2.push({ kind: "element", id: `element-${e.id}`, tier: 2, at: when(it.at as unknown as string), element: e });
  }
  return [...tier1, ...tier2];
}

export function tierOneCount(cards: readonly TrayCard[]): number {
  return cards.filter((c) => c.tier === 1).length;
}

/** The one short line a status region says when the first card arrives. */
export function trayAnnouncement(cards: readonly TrayCard[], agent: string): string {
  const c = cards[0];
  if (!c) return "";
  switch (c.kind) {
    case "background":
      return `${agent} has a running command you can move to background.`;
    case "permission":
      return `${agent} is asking for permission.`;
    case "question":
      return `${agent} has a question.`;
    case "modal":
      return `${agent} opened a screen in its terminal.`;
    case "approval":
      return `${agent} wants to run a command on ${hostName(c.conn)}.`;
    case "go":
      return `${agent} is asking to connect to ${hostName(c.conn)}.`;
    case "prompt":
      return `${hostName(c.conn)} is asking for input.`;
    case "auth":
      return `Signing in to ${hostName(c.conn)}.`;
    case "reconnect":
    case "failed":
      return `The connection to ${hostName(c.conn)} needs you.`;
    case "element":
      return `${agent} is asking something.`;
  }
}

/** A label is the safe default only when it is exactly Yes and qualifies nothing. */
export function isPrimaryChoice(label: string): boolean {
  return label === "Yes";
}

/** The narrow card keeps Yes and No in view, and shows the qualified options in full under More. */
export function isPlainChoice(label: string): boolean {
  return label === "Yes" || label === "No";
}

/**
 * The dialog's own words around a command. They are still drawn, whole and in order, since the
 * card shows the pane verbatim. They just must not read as loudly as what would actually run.
 */
export function isFrameLine(line: string): boolean {
  return /^(Bash command|Do you want to .*)$/.test(line.trim());
}

/** The newest human message not yet delivered, for ↑ on an empty field. */
export function queuedItems(items: readonly SSHTimelineItem[]): SSHTimelineItem[] {
  return items.filter((i) => i.human && !i.human.delivered && !i.human.recalled && (i.human.text ?? "").trim());
}

/** The field's empty line says who it reaches, and when it will. */
export function placeholderFor(state: ComposerState, agent: string, driving: boolean): string {
  if (driving) return `Ask ${agent} to carry on…`;
  switch (state) {
    case "card":
    case "modal":
      return "Queue a note for after you answer…";
    case "working":
    case "gone":
    case "quota":
    case "unproven":
      return `Talk to ${agent}…`;
    default:
      return `Reply to ${agent}…`;
  }
}
