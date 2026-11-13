// The conversation's client state: what the daemon last said about the session being
// viewed, and the one place the app calls TalkService. feed.svelte.ts reads through this file.
// store.svelte.ts must not import the service, because the phone reaches it.
//
// Nothing here decides policy. The daemon re-reads the pane behind every action, and a
// refusal comes back as a sentence to show.

import { untrack } from "svelte";
import { TalkService } from "@bindings/desktop";
import type {
  AgentChooseData,
  AgentFeedArgs,
  AgentFeedData,
  AgentFeedOutputData,
  AgentSayArgs,
  AgentSayData,
  AgentSettingArgs,
  AgentStopData,
  EndedSession,
  TalkStateData,
} from "@bindings/internal/protocol";

/** How often a mounted session view asks. Each ask costs the daemon up to 3 tmux execs. */
export const TALK_POLL_MS = 1000;

/**
 * A daemon older than the app answers "unknown cmd", and one built before the handler
 * was written answers "not yet". Both mean the same thing to a client: no reader, no
 * press-now send, no cards. The session stays on its terminal and shows why.
 */
const UNSUPPORTED_RE = /unknown cmd|^(error: )?not yet$/i;

export function isTalkUnsupported(err: unknown): boolean {
  return UNSUPPORTED_RE.test(errText(err));
}

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err ?? "");
}

/** What a session reads as before an answer, and when its daemon has no such command. */
export function unsupportedState(id: string): TalkStateData {
  return {
    session: id,
    caps: { feed: false, send: false, stop: false, cards: false },
    composer: "unknown",
    feedSeq: 0,
    health: "unsupported",
  } as TalkStateData;
}

class SessionTalk {
  /** The last answer. It stays while the daemon is unreachable: a view keeps its story. */
  data: TalkStateData | null = null;
  /** The daemon has no such command, as opposed to not having answered yet. */
  unsupported = false;
  /** Why the last ask failed, "" when it answered. Not an unsupported daemon. */
  error = "";
  inflight = false;
  /** Moves on every press. An answer to an ask made before it describes a pane now gone. */
  presses = 0;
  /** How many asks have come back, answered or not. A request that waits on a fresh look counts these. */
  answered = 0;
  pollers = 0;
  timer: ReturnType<typeof setInterval> | undefined;
}

class TalkStore {
  // A plain Map on purpose, as in ssh.svelte.ts: state(id) creates on first read and is
  // called inside $derived, where writing to reactive state throws. Nothing subscribes
  // to the map itself, only to each entry's own $state.
  //
  // The answers are plain too, and this one counter is what readers subscribe to. Svelte does not
  // track a $state created inside the reaction reading it, so an entry's own $state, created by
  // the first $derived to ask, would leave that derived stale for good.
  private sessions = new Map<string, SessionTalk>();
  private endedRows: EndedSession[] = [];
  private endedAsks = 0;
  private answers = $state(0);
  // Untracked, because the first refresh runs inside the effect that starts the poll, and an effect
  // that read this while writing it would run itself forever.
  private changed() {
    untrack(() => this.answers++);
  }

  private entry(id: string): SessionTalk {
    let t = this.sessions.get(id);
    if (!t) {
      t = new SessionTalk();
      this.sessions.set(id, t);
    }
    return t;
  }

  /** The last thing the daemon said about this session, null before its first answer. */
  state(id: string): TalkStateData | null {
    void this.answers;
    return this.entry(id).data;
  }

  /** The daemon does not know these commands, so nothing of this can be offered. */
  unsupported(id: string): boolean {
    void this.answers;
    return this.entry(id).unsupported;
  }

  /** Why the last ask failed, in the daemon's own words, or "". */
  error(id: string): string {
    void this.answers;
    return this.entry(id).error;
  }

  /** Asks once. A second ask while one is out is dropped: its answer is about to arrive. */
  async refresh(id: string): Promise<void> {
    const t = this.entry(id);
    if (t.inflight) return;
    t.inflight = true;
    const asked = t.presses;
    try {
      const d = await TalkService.State({ session: id });
      if (asked !== t.presses) return;
      t.data = d;
      t.unsupported = false;
      t.error = "";
    } catch (err) {
      if (isTalkUnsupported(err)) {
        t.data = unsupportedState(id);
        t.unsupported = true;
        t.error = "";
      } else {
        t.error = errText(err);
      }
    } finally {
      t.inflight = false;
      t.answered++;
      this.changed();
    }
  }

  /** How many asks about this session have come back. See SessionTalk.answered. */
  answerCount(id: string): number {
    void this.answers;
    return this.entry(id).answered;
  }

  /**
   * Polls while a view is mounted and the window has focus, and answers the function that
   * ends it, so a component can return it from an effect. Two views of one session share
   * one poll. An unfocused window asks nothing: the cost is tmux execs nobody is reading.
   */
  poll(id: string): () => void {
    const t = this.entry(id);
    t.pollers++;
    if (t.pollers === 1) {
      void this.refresh(id);
      t.timer = setInterval(() => {
        if (document.hasFocus()) void this.refresh(id);
      }, TALK_POLL_MS);
    }
    let ended = false;
    return () => {
      if (ended) return;
      ended = true;
      t.pollers--;
      if (t.pollers === 0) {
        clearInterval(t.timer);
        t.timer = undefined;
      }
    };
  }

  /**
   * Stops this turn. A refusal rejects with the daemon's sentence, for the caller to show.
   * The daemon answers most refusals as data, `stopped: false` with a `why`, and only a few
   * as an error, so both have to end up in the same place or ⌘. does nothing visible. The
   * data case rejects with the bare string because callers flash String(err), and an Error
   * would read "Error: Already stopped".
   */
  async stop(id: string): Promise<AgentStopData> {
    const d = await TalkService.Stop({ session: id });
    if (!d.stopped) throw d.why || "Already stopped";
    return d;
  }

  /** Shared across remounts so a slow background press cannot be sent twice. */
  backgroundPending = $state<Record<string, boolean>>({});
  backgroundErrors = $state<Record<string, string>>({});

  settingPending = $state<Record<string, boolean>>({});
  settingErrors = $state<Record<string, string>>({});
  settingNotices = $state<Record<string, string>>({});
  settingTerminal = $state<Record<string, boolean>>({});

  async setting(a: AgentSettingArgs): Promise<void> {
    const id = a.session;
    if (this.settingPending[id]) return;
    this.settingPending[id] = true;
    this.settingErrors[id] = "";
    this.settingNotices[id] = "";
    const t = this.entry(id);
    t.presses++;
    try {
      const d = await TalkService.Setting(a);
      if (!d.sent) throw new Error("The setting was not sent. Try again at an idle prompt.");
      this.settingNotices[id] = a.agent === "opencode"
        ? `Choose the ${a.setting} in the inline terminal.`
        : `Requested ${a.setting}: ${a.value}. Check the terminal for confirmation.`;
    } catch (err) {
      this.settingErrors[id] = errText(err);
    } finally {
      t.presses++;
      this.settingPending[id] = false;
      void this.refresh(id);
    }
  }

  async background(id: string, hash: string): Promise<void> {
    if (this.backgroundPending[id]) return;
    this.backgroundPending[id] = true;
    this.backgroundErrors[id] = "";
    try {
      const d = await this.choose(id, hash, "background");
      if (!d.sent) this.backgroundErrors[id] = d.why || "The command changed. Try again.";
    } catch (err) {
      this.backgroundErrors[id] = errText(err);
    } finally {
      this.backgroundPending[id] = false;
    }
  }

  say(a: AgentSayArgs): Promise<AgentSayData> {
    return TalkService.Say(a);
  }

  /**
   * Presses a key on a card. The card follows the reply at once: a refusal carries the pane's
   * prompt as it is now, and a press that landed clears this one, since a denied prompt fires no
   * hook and the next read is a second away. That read is what proves it.
   */
  async choose(id: string, hash: string, key: string): Promise<AgentChooseData> {
    const t = this.entry(id);
    t.presses++;
    const d = await TalkService.Choose({ session: id, hash, key });
    t.presses++;
    if (t.data && (d.sent || d.prompt)) t.data = { ...t.data, prompt: d.prompt ?? undefined } as TalkStateData;
    this.changed();
    void this.refresh(id);
    return d;
  }

  queueRelease(id: string): Promise<AgentSayData> {
    return TalkService.QueueRelease({ session: id });
  }

  feed(a: AgentFeedArgs): Promise<AgentFeedData> {
    return TalkService.Feed(a);
  }

  feedOutput(id: string, item: string): Promise<AgentFeedOutputData> {
    return TalkService.FeedOutput({ session: id, item });
  }

  // --- sessions that ended -----------------------------------------------------

  /** What the daemon last listed, newest first. The roster's `Ended N` and the ended view read it. */
  endedList(): EndedSession[] {
    void this.answers;
    return this.endedRows;
  }

  /** The record of a session that left the list, if its story is still readable. */
  ended(id: string): EndedSession | undefined {
    void this.answers;
    return this.endedRows.find((e) => e.id === id);
  }

  /** A new session took this id, so its old record is not what opening it should show. */
  forgetEnded(id: string) {
    this.endedRows = this.endedRows.filter((e) => e.id !== id);
    this.changed();
  }

  /**
   * Asks again. A failed ask keeps the last list, and a daemon that predates the command simply
   * has nothing ended. Answers land in order of asking, so a slow old reply cannot bring back a
   * row a newer one dropped.
   */
  async loadEnded(): Promise<void> {
    const my = ++this.endedAsks;
    try {
      const d = await TalkService.Ended();
      if (my !== this.endedAsks) return;
      this.endedRows = d?.sessions ?? [];
      this.changed();
    } catch (err) {
      if (my === this.endedAsks && isTalkUnsupported(err)) {
        this.endedRows = [];
        this.changed();
      }
    }
  }
}

export const talk = new TalkStore();
