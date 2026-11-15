// Pinning a pane to this phone's size, and — the half that actually matters —
// letting it go again.
//
// WHAT THE FEATURE IS. `cmd=paneResize` sets the tmux window behind a pane to an
// explicit size, or releases it back to whatever clients remain attached. The
// phone uses it so a 200-column agent pane is redrawn at the ~50 columns a phone
// can show, instead of being panned over. That is the deliberate opposite of
// everything else the pane path does: `internal/panebus` attaches with
// `-f ignore-size` precisely so a phone joining a stream never reshapes the
// developer's window, and this command is the one sanctioned exception.
//
// WHICH IS WHY THIS MODULE IS MOSTLY ABOUT THE RELEASE. A pin is somebody
// else's screen. It is correct for exactly as long as a human is looking at the
// pane on a phone, and a pin that outlives that leaves a developer's tmux window
// squashed to phone width with nothing on the Mac saying why or by whom. So the
// posture throughout is: prefer releasing twice over releasing zero times, make
// every release idempotent, and never let a failure pass in silence.
//
// THE DAEMON DOES NOT CLEAN UP AFTER US, and that is the fact that shapes the
// design. `SetWindowSize` has exactly one caller (`handlePaneResize`); nothing
// releases a pin when a subscription ends, a socket drops, or a device
// disappears. A phone that is force-quit while pinned would otherwise leave that
// window squashed forever. Hence the BREADCRUMB: the pane being pinned is
// written to storage BEFORE the request goes out and cleared only when a release
// has actually resolved, so the next run can find an unfinished pin and undo it.
// If a daemon-side release on subscription end ever lands, the breadcrumb
// becomes belt-and-braces rather than the only safety net; until then it is the
// only safety net.
//
// The breadcrumb's storage is `panepinstore.ts`; it is written and cleared in
// lockstep with the wire calls below, never from anywhere else.
//
// NO DOM, NO RUNES, NO TRANSPORT. The resize call arrives as a seam, so the
// whole lifecycle — including every release path — is exercisable in Node.

import { DaemonError, type PaneResizeData } from "@mobile/wire";
import { copy } from "./copy";
import {
  addPinnedPane,
  dropPinnedPane,
  loadPinnedPanes,
  type PinRecord,
} from "@mobile/lib/panepinstore";

// ---------------------------------------------------------------------------
// Bounds and vocabulary
// ---------------------------------------------------------------------------

/**
 * The largest dimension the daemon accepts, mirroring `maxPaneDim` in
 * internal/daemon/panes.go.
 *
 * A phone is nowhere near it. It is mirrored so a nonsense measurement is
 * CLAMPED here rather than REJECTED there: the daemon refuses an out-of-range
 * pin outright, which would leave the pane unpinned and hand the user an error
 * about a number they never chose.
 */
export const PIN_MAX_DIM = 500;

/**
 * The trailing debounce on a size-only re-pin.
 *
 * The size genuinely moves under a reader: raising the soft keyboard halves the
 * frame, so the row capacity halves with it, and every change reflows the
 * agent's TUI on the Mac. A pane whose IDENTITY changes is not debounced —
 * switching tabs must release the old pane at once — and neither is any release.
 */
export const PIN_SETTLE_MS = 400;

/** A pane and the size to hold it at. */
export interface PinTarget extends PinRecord {
  cols: number;
  rows: number;
}

/**
 * What a stuck pin (a release that could not be sent) says; every reconnect and
 * every return to a terminal retries it. Withdrawn with "" once a later request
 * succeeds, so the banner never outlives the condition.
 */
export const PIN_STUCK_MESSAGE = copy.terminal.pinStuck.body;

// ---------------------------------------------------------------------------
// The lifecycle
// ---------------------------------------------------------------------------

/** The wire call, as a seam. `cols <= 0` is the release. */
export type PinResize = (
  session: string,
  pane: string,
  cols: number,
  rows: number,
) => Promise<PaneResizeData>;

export interface PanePinOptions {
  resize: PinResize;
  /**
   * Somewhere to say a release failed, and — with `""` — that it no longer has.
   * Default: nowhere, for tests that only care about the wire.
   */
  report?: (message: string) => void;
  /** Override the size-only debounce. Tests set it to 0. */
  settleMs?: number;
}

function samePane(a: PinRecord, b: PinRecord): boolean {
  return a.session === b.session && a.pane === b.pane;
}

/**
 * Clamp a measurement into something the daemon will accept, or reject it.
 *
 * A zero is not a small pin, it is the RELEASE encoding, so an unmeasured box
 * must never reach the wire as a target: it would silently release instead of
 * pinning, and the toggle would look broken rather than wrong.
 */
function normalize(t: PinTarget | null): PinTarget | null {
  if (!t || t.session === "" || t.pane === "") return null;
  const cols = Math.min(PIN_MAX_DIM, Math.floor(t.cols));
  const rows = Math.min(PIN_MAX_DIM, Math.floor(t.rows));
  if (!(cols >= 1) || !(rows >= 1)) return null;
  return { session: t.session, pane: t.pane, cols, rows };
}

/**
 * One pane WANTED at a time, everything ever pinned remembered, and released on
 * the way out.
 *
 * Every request goes through one serialised chain, so a switch asked for while a
 * pin is still in flight cannot interleave with it.
 *
 * The class keeps two facts apart on purpose:
 *
 *   `#want`   what the screen currently asks for, which changes with the toggle,
 *             the focused pane, the measured size and the connection.
 *   `#held`   every pane the daemon is BELIEVED to be holding for this device.
 *
 * They are separate because a failed request leaves them disagreeing, and the
 * disagreement is the thing that has to be remembered. A pin whose request threw
 * is assumed to have LANDED (the pane is held anyway): if it did and we forgot,
 * the window stays squashed forever, whereas if it did not, the eventual release
 * is a harmless no-op on an unpinned window. That asymmetry is the whole rule.
 *
 * WHY `#held` IS A LIST, which is the correction this class most needed. It was
 * a single slot, and a single slot cannot describe the state a failed release
 * leaves behind: pane A's release fails, pane B's pin succeeds a moment later,
 * and the slot — and the breadcrumb under it — now name B. A is still squashed
 * on somebody's Mac, no longer believed held, no longer recorded, and no later
 * release, reconnect or relaunch will ever undo it. The list makes the sweep
 * true instead of nominal: EVERY held pane that is not the wanted one is
 * released on every apply, and stays both held and recorded until one of those
 * releases actually resolves.
 *
 * WHAT IS DELIBERATELY NOT DONE is refusing to pin B while A is stuck. It would
 * make "at most one window is ever squashed" airtight and it is the wrong trade:
 * the commonest stuck release by far is a release of a pane that no longer
 * EXISTS (see `forgetMissing`), where nothing is squashed at all, and blocking
 * would turn that harmless case into a pin toggle that is dead for the rest of
 * the run.
 */
export class PanePin {
  readonly #resize: PinResize;
  readonly #report: (message: string) => void;
  readonly #settleMs: number;

  #want: PinTarget | null = null;
  #held: PinRecord[] = [];
  /**
   * The panes each session was last known to have, from `forgetMissing`. A
   * session with no entry is one nothing has been reported about, which is
   * different from one reported as empty.
   */
  #live = new Map<string, Set<string>>();
  #sent: { cols: number; rows: number } = { cols: 0, rows: 0 };
  /** Set when the last request may not have landed, so the next apply re-sends. */
  #dirty = false;
  #timer: ReturnType<typeof setTimeout> | undefined;
  #chain: Promise<void> = Promise.resolve();

  constructor(o: PanePinOptions) {
    this.#resize = o.resize;
    this.#report = o.report ?? (() => {});
    this.#settleMs = o.settleMs ?? PIN_SETTLE_MS;
  }

  /**
   * Every pane the daemon is believed to be holding for this device. Empty when
   * nothing is pinned; longer than one only while a release is outstanding.
   */
  held(): readonly PinRecord[] {
    return this.#held;
  }

  /** Resolves once every queued request has settled. The tests' one wait. */
  settled(): Promise<void> {
    return this.#chain;
  }

  /**
   * Ask for a pin, or for none. Fire and forget; the screen's one entry point.
   *
   * A change of PANE, and any request for none, is applied at once. A change of
   * SIZE on the pane already pinned is debounced, because the soft keyboard
   * moves the row capacity and each move reflows the agent's screen.
   */
  want(t: PinTarget | null): void {
    const next = normalize(t);
    const sizeOnly = !!next && !!this.#want && samePane(next, this.#want);
    this.#want = next;
    clearTimeout(this.#timer);
    this.#timer = undefined;
    if (sizeOnly && !this.#dirty && this.#settleMs > 0) {
      this.#timer = setTimeout(() => void this.#apply(), this.#settleMs);
      return;
    }
    void this.#apply();
  }

  /** Ask for a pin and wait for it. The awaited half of `want`, without the debounce. */
  set(t: PinTarget | null): Promise<void> {
    clearTimeout(this.#timer);
    this.#timer = undefined;
    this.#want = normalize(t);
    return this.#apply();
  }

  /**
   * Let the pane go.
   *
   * IDEMPOTENT: with nothing pinned it sends nothing and resolves, so every exit
   * path can call it unconditionally and two paths firing for the same departure
   * — leaving the screen while the app also backgrounds — cost one request
   * between them.
   */
  release(): Promise<void> {
    return this.set(null);
  }

  /**
   * Re-send whatever is currently wanted, because the socket underneath changed.
   *
   * A reconnect gives us a new connection to a daemon that may or may not still
   * hold what we last sent. What `#held` believes is deliberately NOT cleared —
   * forgetting it would turn a later release into a no-op and leave a squashed
   * window — so instead the dedupe is bypassed once and the current intent is
   * asserted again. Both directions are idempotent on the daemon.
   */
  reassert(): Promise<void> {
    this.#dirty = true;
    return this.#apply();
  }

  /**
   * Take up a pin this app left behind, from an earlier run or an earlier
   * connection, and release it.
   *
   * This is the only thing that unsquashes a window after a force-quit, a crash
   * or a socket that died before the release got out, and it costs one storage
   * read when there is nothing to do.
   *
   * The breadcrumb is MERGED INTO `#held` rather than released directly, which
   * is what closes the race the direct version had: a pin that landed before
   * this ran had already overwritten the one-slot crumb, so the older pane was
   * never swept at all. Now the sweep is the ordinary one — `#apply` releases
   * every held pane that is not wanted — and a crumb that names something else
   * simply joins the list it belongs in.
   *
   * `keep` names a pane that is about to be pinned deliberately. It is still
   * believed held (the previous run really did pin it, and it must be released
   * on the way out), but it is exempted from THIS sweep, so reopening the same
   * pane does not hand the window back and take it again a moment later.
   */
  recover(keep?: PinRecord | null): Promise<void> {
    for (const crumb of loadPinnedPanes()) this.#hold(crumb);
    return this.#apply(keep ?? null);
  }

  /**
   * Tell the pin which panes of `session` the daemon actually lists.
   *
   * WHY A PIN NEEDS THIS AT ALL. A release names a pane whose tmux window may
   * already be gone — the shell was exited, the tab was closed from its own
   * menu, the session was cleaned up on the Mac — and the daemon REFUSES such a
   * release: it validates the pane by name convention and then asks tmux to
   * resize a window that is not there. Treated as an ordinary failure that is a
   * lie in the one direction that matters. The app would warn that a developer's
   * window is squashed when no window exists, retry forever, and never take the
   * warning down — training a reader to ignore the one sentence that is
   * sometimes true.
   *
   * IT IS REMEMBERED, NOT ACTED ON ONCE, and that is the whole shape of it. The
   * inventory arrives a beat BEFORE the pin stops wanting the pane it describes:
   * closing a tab reloads the list, and only then does the screen move to the
   * neighbour and stop wanting the closed one. A one-shot prune ran while the
   * dead pane was still the wanted one, exempted it, and was gone by the time
   * the release it should have cancelled was attempted. Held as a fact, the
   * release loop consults it at the moment it would otherwise send.
   *
   * Only `session`'s panes are described. The breadcrumb can hold a pane from
   * another session entirely — a pin left behind by an earlier run — and one
   * session's inventory says nothing about it.
   *
   * THE CALLER MUST NOT REPORT A STALE LIST. A list that wrongly omits a live
   * pinned pane retires its record without releasing it, which is the silent
   * squash this module exists to prevent — so PaneTabs discards an out-of-order
   * `cmd=panes` answer rather than reporting it, and reports nothing at all when
   * the call failed. "No panes exist" must never be inferred from a failure.
   */
  forgetMissing(session: string, names: readonly string[]): Promise<void> {
    if (session === "") return this.#chain;
    this.#live.set(session, new Set(names));
    return this.#apply();
  }

  /** Drop the pending debounce. Does NOT release; call `release` for that. */
  stop(): void {
    clearTimeout(this.#timer);
    this.#timer = undefined;
  }

  // --- internals -----------------------------------------------------------

  #queue(step: () => Promise<void>): Promise<void> {
    // Rejections are swallowed into the chain rather than propagated: a failed
    // request must not poison every later one, and `#send` has already decided
    // what a failure means and reported it.
    const run = this.#chain.then(step, () => {}).catch(() => {});
    this.#chain = run;
    return run;
  }

  /** Believe a pane is pinned, and record it. Idempotent. */
  #hold(r: PinRecord): void {
    addPinnedPane(r);
    if (this.#held.some((h) => samePane(h, r))) return;
    this.#held = [...this.#held, r];
  }

  /** Stop believing it, and stop recording it. */
  #drop(r: PinRecord): void {
    dropPinnedPane(r);
    this.#held = this.#held.filter((h) => !samePane(h, r));
  }

  /**
   * Say whether a window is squashed with no way to hand it back.
   *
   * DERIVED FROM THE STATE, not raised by the event that caused it. The
   * event-shaped version withdrew the warning on the next request that got
   * through, whatever pane it was about — so a failed release of pane A was
   * announced and then silently un-announced by a successful pin of pane B,
   * which is the moment the user most needed to keep reading it. A held pane
   * that is not the wanted one is exactly the condition the sentence describes,
   * and it is true for precisely as long as it is true.
   */
  #reportState(want: PinTarget | null): void {
    const stray = this.#held.some((h) => !want || !samePane(h, want));
    this.#report(stray ? PIN_STUCK_MESSAGE : "");
  }

  /**
   * Hand back every held pane that is not wanted, then take the wanted one.
   *
   * `exempt` is `recover`'s `keep`: a pane the screen is about to pin, skipped
   * for this sweep only.
   *
   * A FAILED RELEASE DOES NOT ABORT THE STEP. The pane stays held and recorded,
   * so every later apply retries it and the warning stays up, but the pane the
   * user is actually looking at is still pinned. See the class header for why
   * blocking was the wrong trade.
   */
  #apply(exempt: PinRecord | null = null): Promise<void> {
    return this.#queue(async () => {
      const want = this.#want;

      // RELEASE FIRST when the pane changes. This is the line that makes "the
      // old window is handed back before the new one is taken" a property
      // rather than a hope: it happens in one serialised step. Iterated over a
      // snapshot, because `#send` mutates `#held`.
      for (const h of [...this.#held]) {
        if (want && samePane(h, want)) continue;
        if (exempt && samePane(h, exempt)) continue;
        // A pane the daemon no longer lists holds nothing, so there is nothing
        // to hand back: retire the record instead of spending a request the
        // daemon will refuse and a warning the user cannot act on. Only a
        // session actually reported on can retire anything.
        const live = this.#live.get(h.session);
        if (live && !live.has(h.pane)) {
          this.#drop(h);
          continue;
        }
        await this.#send(h.session, h.pane, 0, 0);
      }

      if (!want) {
        // Nothing wanted and nothing believed held is a settled state, so a
        // `reassert` that found no work must not leave the dedupe bypassed for
        // every later call.
        if (this.#held.length === 0) this.#dirty = false;
        this.#reportState(null);
        return;
      }

      const settled =
        !this.#dirty &&
        this.#held.some((h) => samePane(h, want)) &&
        this.#sent.cols === want.cols &&
        this.#sent.rows === want.rows;
      if (!settled)
        await this.#send(want.session, want.pane, want.cols, want.rows);
      this.#reportState(want);
    });
  }

  async #send(
    session: string,
    pane: string,
    cols: number,
    rows: number,
  ): Promise<void> {
    const releasing = cols <= 0 || rows <= 0;

    // THE BREADCRUMB IS WRITTEN BEFORE THE REQUEST, never after. Between the two
    // is exactly where a phone gets killed, and a pin the daemon applied with
    // nothing on this device recording it is the unrecoverable case.
    if (!releasing) this.#hold({ session, pane });

    try {
      await this.#resize(
        session,
        pane,
        releasing ? 0 : cols,
        releasing ? 0 : rows,
      );
    } catch (err) {
      // A PIN THE DAEMON ITSELF REFUSED DID NOT LAND, and that is the one
      // failure this class can retire instead of remembering.
      //
      // `DaemonError` means a daemon answered `ok: false` — it received the
      // request, decided against it, and said so. Every way it can decide
      // against a PIN leaves the window untouched: an unknown command (a daemon
      // older than this feature), a session or pane it does not recognise, a
      // size out of range, or a tmux failure, which undoes its own half-applied
      // option before answering (see SetWindowSize in internal/tmux/client.go).
      // So holding the pane afterwards records a pin that does not exist.
      //
      // That was not academic. Against a daemon predating `cmd=paneResize`
      // every pin was refused and every release refused with it, so the pane
      // stayed held forever and the app told its user a developer's window was
      // squashed on a Mac where nothing had ever been resized — the stuck-pin
      // warning firing in the one situation where it could not be true, which
      // is exactly how a warning stops being read.
      //
      // EVERY OTHER FAILURE STILL HOLDS, and the asymmetry is deliberate. A
      // request that timed out, or died with the socket, may well have been
      // applied on the way; assuming it was costs a redundant release, while
      // assuming it was not costs somebody else's window. A failed RELEASE
      // holds for the same reason, DaemonError included: the daemon already
      // forgives a release of a pane that is genuinely gone, so a refusal here
      // is a pin that may still be in place.
      if (!releasing && err instanceof DaemonError) {
        this.#drop({ session, pane });
        this.#dirty = true;
        return;
      }
      this.#dirty = true;
      if (!releasing) this.#sent = { cols, rows };
      return;
    }

    if (releasing) {
      this.#drop({ session, pane });
      if (this.#held.length === 0) {
        this.#sent = { cols: 0, rows: 0 };
        this.#dirty = false;
      }
      return;
    }
    this.#sent = { cols, rows };
    this.#dirty = false;
  }
}
