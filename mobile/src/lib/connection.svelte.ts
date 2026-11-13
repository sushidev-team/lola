// The connection, as the UI sees it.
//
// One object holds the whole story: which endpoint, which phase, and — the part
// that took the most care — a single sentence saying what is wrong in terms the
// person holding the phone can act on. Everything reactive is a rune; the
// classification itself lives in the pure `diagnose` module beside this one so
// it can be tested exhaustively.
//
// -------------------------------------------------------------------------
// THE ONE INTEGRATION POINT, and it is deliberately explicit.
//
// This module does NOT construct a Transport. It cannot: the only real one is
// the native plugin, and there is no honest way for a Svelte module to decide
// whether that plugin exists, which build it is, or what a browser dev session
// should get instead. So the app's bootstrap creates the Transport once and
// hands the SAME INSTANCE to two places:
//
//     const t = makeTransport();          // in main.ts
//     shim.useTransport(t);               // src/wailsshim — service calls
//     connection.useTransport(t);         // here — connect UI and pane streams
//
// They must be the same instance. The shim turns `DaemonService.Sessions()` into
// a `req` frame on it, and this module turns the connect screen into a
// `connect()` on it; two transports would authenticate twice, hold two sockets
// against a max-8 server, and show a connected UI over a dead pipe.
//
// Until `useTransport` is called, `connect()` reports "no transport" rather than
// throwing, so a browser `npm run dev` renders the UI and says plainly why it
// cannot reach anything.
// -------------------------------------------------------------------------

import type {
  ConnectionStatus,
  Endpoint,
  PaneSubscription,
  Transport,
  Unsubscribe,
  Viewport,
} from "@mobile/wire";
import { INSECURE_MIN_KEY_LEN } from "@mobile/wire/protocol";
import { canDiscover, candidates, discover, type Candidate } from "./discovery";
import {
  daemonLabel,
  forgetDaemonName,
  hasCustomName,
  learnDaemonName,
  renameDaemon,
} from "./daemonname";
import { diagnose, type Diagnosis } from "./diagnose";
import {
  endpointId,
  parsePort,
  validateDraft,
  type EndpointDraft,
  type EndpointProblem,
} from "./endpoint";
import {
  clearEndpoint,
  findKey,
  forgetKey,
  loadEndpoint,
  saveEndpoint,
  storeKey,
  type KeyStorage,
} from "./secretstore";

export { INSECURE_MIN_KEY_LEN };

/**
 * Exported for the bootstrap-race test only; the app uses the `connection`
 * singleton below. A second instance in production would authenticate twice and
 * hold two of the daemon's eight connection slots.
 */
export class Connection {
  /** The live transport phase, mirrored so the UI can read a rune. */
  phase = $state<ConnectionStatus["phase"]>("idle");
  /** The last involuntary failure, if any. */
  error = $state<Error | null>(null);
  /** A refusal the daemon sent before closing, if any. Outranks `error`. */
  refusal = $state<ConnectionStatus["refusal"] | null>(null);
  /** Where we are pointed. Host and port only; the key never lives here. */
  host = $state("");
  port = $state(0);
  /** Per-field form problems from the last connect attempt. */
  problems = $state<EndpointProblem[]>([]);
  /** True while a connect attempt is in flight, so the button can say so. */
  busy = $state(false);
  /** Whether a stored key was found for the remembered endpoint. */
  hasStoredKey = $state(false);
  /**
   * Where the key for the current endpoint actually ended up.
   *
   * Read by the connect screen's "Remember this Mac" caption, which used to be
   * driven by `isPersistent()` — a statement about what the plugin CAN do, not
   * about what this write DID. A refusing Keychain drops the key into an
   * in-memory map, and the caption went on promising it would survive a
   * relaunch. The fallback stays (losing the running session over a Keychain
   * failure would be worse); the claim does not.
   */
  keyStorage = $state<KeyStorage>("none");

  /**
   * True while an AUTOMATIC reconnect is in flight, as opposed to `busy`, which
   * is any connect including the one a human just tapped.
   *
   * The banner on the sessions list reads `diagnosis`, so without this a phone
   * returning from standby spends the reconnect window telling its owner it is
   * on the wrong network — the single most misleading sentence available, since
   * the app is at that moment successfully dialling.
   */
  reconnecting = $state(false);

  /**
   * Bumped whenever a stored name changes, so `label` re-derives.
   *
   * localStorage is not reactive and the names live there (per endpoint, beside
   * the Keychain entry's own id). A counter is the whole subscription.
   */
  nameEpoch = $state(0);

  /**
   * How the endpoint is named in a sentence.
   *
   * THE MACHINE, NOT THE ADDRESS. A phone that can browse for the daemon
   * reaches the same Mac on a different address at home and at the office, so
   * an address names a network rather than the machine — and after a move it
   * names a network that is not there. The chain is the user's own name, then
   * the one the daemon reported for itself, then the address, then a phrase;
   * every link can be absent and each fallback still names something true.
   */
  label = $derived.by(() => {
    void this.nameEpoch;
    return daemonLabel(endpointId(this.host, this.port), this.host);
  });

  /** Whether this daemon has been renamed here, so a form can offer to undo. */
  renamed = $derived.by(() => {
    void this.nameEpoch;
    return hasCustomName(endpointId(this.host, this.port));
  });

  /** Rename the daemon on this device; "" restores the name it reports itself. */
  rename(name: string): void {
    renameDaemon(endpointId(this.host, this.port), name);
    this.nameEpoch++;
  }

  /**
   * Record the name the daemon reports for itself (`cmd=status` → `host`).
   *
   * Called with whatever the status answer carried, including "", which is
   * ignored rather than stored — a daemon too old to send one must not erase a
   * name already learned.
   */
  learnName(host: string): void {
    const id = endpointId(this.host, this.port);
    const before = daemonLabel(id, this.host);
    learnDaemonName(id, host);
    if (daemonLabel(id, this.host) !== before) this.nameEpoch++;
  }

  /** The one sentence the connect screen shows. */
  diagnosis = $derived<Diagnosis>(
    this.reconnecting
      ? {
          kind: "unreachable",
          title: `Reconnecting to ${this.label}`,
          detail:
            "The connection was closed while the app was in the background.",
          retryable: true,
        }
      : (backgroundedDiagnosis(this.error, this.label) ??
          diagnose({
            phase: this.phase,
            error: this.error,
            refusal: this.refusal,
            host: this.host,
            label: this.label,
          })),
  );

  ready = $derived(this.phase === "ready");

  #transport: Transport | undefined;
  #offStatus: Unsubscribe | undefined;

  /**
   * Set when a HUMAN disconnected, cleared by any connect.
   *
   * Without it, foregrounding the app while it sits on the connect screen —
   * which is exactly where `disconnect()` leaves the user — would silently
   * re-dial the daemon they had just chosen to leave. The flag is the whole
   * difference between "the app dropped and came back" and "I left".
   */
  #userClosed = false;

  #retryTimer: ReturnType<typeof setTimeout> | undefined;
  #retryStep = 0;

  /**
   * The retry ladder, then silence.
   *
   * It is short and it ENDS, because the alternative is a phone that has been
   * off its network for a day quietly opening a socket every few seconds
   * against a daemon that allows eight of them. When it runs out the banner is
   * already saying what is wrong and the pull-to-refresh and the next
   * foreground both start a fresh ladder, so nothing is stuck — it just stops
   * costing anything.
   */
  static readonly RETRY_DELAYS_MS: readonly number[] = [2000, 5000, 15000];

  /**
   * Resolves the moment a Transport is adopted. See `#awaitTransport`.
   *
   * Built in the field initialiser rather than lazily, because the whole point
   * is that a caller can arrive BEFORE the bootstrap does and still have
   * something to wait on.
   */
  #transportArrived!: Promise<void>;
  #announceTransport!: () => void;

  /** The transport, for the terminal screen's pane subscriptions. */
  get transport(): Transport | undefined {
    return this.#transport;
  }

  constructor() {
    this.#transportArrived = new Promise<void>((resolve) => {
      this.#announceTransport = resolve;
    });
  }

  /**
   * Wait, briefly, for the bootstrap's Transport to arrive.
   *
   * THE RACE THIS EXISTS FOR. `main.ts` installs the transport through a
   * DYNAMIC import (`await import("./wailsshim/capacitorchannel")`) and
   * deliberately does not await it before mounting the app — a blocking import
   * there turns a possible one-repaint theme flash into a guaranteed blank
   * screen. That was harmless for as long as the only way to connect was a
   * human typing four values into a form, which takes seconds. The hand-off
   * paths are not that: a `dev-launch` link is retained by the plugin and
   * replayed to the very first listener that registers, which is `onMount` —
   * so the connect attempt can land while that dynamic import is still being
   * read off disk.
   *
   * The symptom was not an error, which is what made it worth a fix rather than
   * a comment. `connect()` failed with "no transport", and then `useTransport`
   * arrived a tick later and `#adopt` overwrote `error` with the fresh
   * transport's clean status — so the screen showed a filled form, no banner
   * and no connection, on every cold boot and never on a warm one.
   *
   * Bounded, because "no transport" is a REAL state that must still be
   * reportable: a browser `npm run dev` session has none and never will, and a
   * device build whose plugin was not synced is in the same position. Waiting
   * forever there would replace a clear sentence with a button that hangs.
   */
  async #awaitTransport(ms = 4000): Promise<Transport | undefined> {
    if (this.#transport) return this.#transport;
    let timer: ReturnType<typeof setTimeout> | undefined;
    await Promise.race([
      this.#transportArrived,
      new Promise<void>((resolve) => {
        timer = setTimeout(resolve, ms);
      }),
    ]);
    if (timer !== undefined) clearTimeout(timer);
    return this.#transport;
  }

  /**
   * Adopt the app's one Transport. Called once from the bootstrap; safe to call
   * again with the same instance (the status subscription is replaced, not
   * duplicated).
   */
  useTransport(t: Transport): void {
    this.#offStatus?.();
    this.#announceTransport();
    this.#transport = t;
    this.#adopt(t.status);
    this.#offStatus = t.onStatus((s) => this.#adopt(s));
  }

  #adopt(s: ConnectionStatus): void {
    this.phase = s.phase;
    this.error = s.error ?? null;
    this.refusal = s.refusal ?? null;
    if (s.endpoint) {
      this.host = s.endpoint.host;
      this.port = s.endpoint.port ?? 0;
    }
  }

  /**
   * Fill the form from what was remembered last time, and report whether a key
   * came with it. The endpoint (host, port, pin) is public and lives in
   * localStorage; the key is in the Keychain and is only ever read here, into a
   * local, on the way to `connect`.
   */
  async restore(): Promise<{
    draft: EndpointDraft;
    key: string;
    /** The Keychain account to read the key from, when it is not in `key`. */
    keyRef: string;
  } | null> {
    const saved = loadEndpoint();
    if (!saved) return null;
    const port = saved.port || 0;
    const id = endpointId(saved.host, port);
    const found = await findKey(id);
    this.hasStoredKey = found.where !== "none";
    this.keyStorage = found.where;
    return {
      draft: {
        host: saved.host,
        port: port ? String(port) : "",
        spkiPin: saved.spkiPin,
      },
      // Empty for a Keychain-held key, and deliberately so: the plaintext has
      // no way back across the bridge, because a resolved payload is logged.
      // `keyRef` is how it is used instead. See secretstore.ts.
      key: found.key,
      keyRef: found.where === "keychain" ? id : "",
    };
  }

  /**
   * Validate, remember, and connect.
   *
   * Returns true on success. On a validation failure it sets `problems` and
   * never touches the network — a form that dials before it checks turns a typo
   * into a ten-second timeout and a misleading network error.
   */
  async connect(
    draft: EndpointDraft,
    key: string,
    remember: boolean,
    alternates: readonly string[] = [],
    /**
     * The Keychain account holding the key, when the caller does not have the
     * key itself. Supplied by `restore()` on every automatic reconnect; the
     * plugin reads it natively. See secretstore.ts for why the plaintext is
     * not passed back through the WebView.
     */
    keyRef = "",
  ): Promise<boolean> {
    // A key held natively is a key that IS there; validating a string the
    // caller was deliberately not given would refuse every reconnect.
    this.problems = validateDraft(
      draft,
      key,
      INSECURE_MIN_KEY_LEN,
      keyRef !== "",
    );
    if (this.problems.length > 0) return false;

    // Any connect is a statement of intent to be connected, so it revokes an
    // earlier "I left". Placed after validation so a rejected form does not
    // quietly re-arm the automatic reconnect.
    this.#userClosed = false;

    // Not `this.#transport` directly: a hand-off connects at launch, which can
    // be before the bootstrap's dynamic import has resolved. See #awaitTransport.
    const t = await this.#awaitTransport();
    if (!t) {
      this.phase = "closed";
      this.error = new Error(
        "No transport is installed in this build. The native plugin provides it; a browser " +
          "dev session has none.",
      );
      return false;
    }

    const port = parsePort(draft.port) ?? 0;
    const pin = draft.spkiPin.trim();

    // THE HOST IS A GUESS; THE ALTERNATES ARE THE REST OF THE ANSWER.
    //
    // A Mac commonly has several private addresses at once — Wi-Fi, a wired
    // dock, a VM bridge — and the daemon reports all of them because it cannot
    // know which one the phone shares a network with. Committing to the first
    // and reporting "unreachable" blames the network for what is really a
    // guess, on a machine that already listed the alternatives.
    const hosts = [
      draft.host.trim(),
      ...alternates.map((a) => a.trim()),
    ].filter((h, i, all) => h !== "" && all.indexOf(h) === i);

    this.busy = true;
    try {
      // The addresses that were OFFERED, then the ones this network can be
      // ASKED for. See #discovered: browsing costs a couple of seconds and is
      // only worth paying once everything already known has failed.
      let attempts: { host: string; port: number }[] = hosts.map((h) => ({
        host: h,
        port,
      }));
      let browsed = false;

      for (let i = 0; i < attempts.length; i++) {
        const { host, port: hostPort } = attempts[i];
        if (i === attempts.length - 1 && !browsed) {
          browsed = true;
          const extra = await this.#discovered(pin, hosts);
          attempts = [...attempts, ...extra];
        }
        const endpoint: Endpoint = {
          host,
          port: hostPort,
          spkiPin: pin,
          insecureKey: key,
          keyRef,
        };

        // Show the target while connecting, so the failure sentence can name it
        // even if the transport never reports a status at all.
        this.host = endpoint.host;
        this.port = hostPort;
        this.error = null;
        this.refusal = null;

        try {
          await t.connect(endpoint);
          if (remember && key !== "") {
            // Remember the one that WORKED, not the one offered first —
            // otherwise the next launch repeats the same failed guess and pays
            // its timeout again.
            saveEndpoint({
              host: endpoint.host,
              port: hostPort,
              spkiPin: endpoint.spkiPin,
            });
            // The OUTCOME, not the attempt: a Keychain that refused leaves the
            // key in memory for this run only, and the connect screen has to be
            // able to say so rather than promising a persistence that did not
            // happen.
            this.keyStorage = await storeKey(
              endpointId(endpoint.host, hostPort),
              key,
            );
            this.hasStoredKey = true;
          }
          return true;
        } catch (e) {
          // The status listener has usually already recorded the real reason;
          // this only fills in when the rejection came without one.
          if (!this.refusal && !this.error) {
            this.error = e instanceof Error ? e : new Error(String(e));
          }
          if (
            this.phase === "ready" ||
            this.phase === "connecting" ||
            this.phase === "handshaking"
          ) {
            this.phase = "closed";
          }

          // A REFUSAL ENDS IT. The daemon answered and said no — a wrong key, a
          // pin that does not match its certificate — and every other address
          // reaches the SAME daemon and gets the same answer. Walking the rest
          // of the list would turn one clear "rejected" into several seconds of
          // timeouts ending in "unreachable": slower, and wrong.
          if (this.refusal) return false;
        }
      }
      return false;
    } finally {
      this.busy = false;
    }
  }

  /**
   * Ask the network where this daemon is, once every known address has failed.
   *
   * WHY IT IS LAST RATHER THAN FIRST. A remembered address that still works is
   * the common case and costs one connect; browsing costs a fixed couple of
   * seconds whatever the answer. So discovery is what happens when the answer
   * this phone already had turns out to be stale — a Mac at the office instead
   * of at home, a new DHCP lease, a hotspot — which is exactly when a couple of
   * seconds is cheaper than the alternative of a human retyping an address.
   *
   * A CANDIDATE IS NOT TRUSTED FOR BEING FOUND. Anything on a network can
   * advertise the service; the pin decides, in the same handshake that decides
   * for a typed address. `candidates` only drops a service whose ADVERTISED pin
   * already disagrees, which saves a doomed socket rather than providing any
   * security of its own.
   *
   * Failure is silent by construction: no plugin, a declined local-network
   * permission and a network without multicast all mean "no candidates", and
   * the caller has already tried everything else.
   */
  async #discovered(
    pin: string,
    known: readonly string[],
  ): Promise<Candidate[]> {
    if (!canDiscover()) return [];
    try {
      return candidates(await discover(), pin, known);
    } catch {
      return [];
    }
  }

  /**
   * Forget this Mac: the stored key AND the remembered address.
   *
   * The counterpart to `remember`, and for a long time there was none — the
   * key had no deletion path reachable from any screen, so a credential, once
   * stored, stayed on the device for the life of the install, one item per
   * address that had ever worked. Disconnecting did not do it either: that
   * sets a process-local flag which dies with the app, while the credential
   * does not, so a cold launch after an explicit disconnect went straight back
   * to an authenticated session list.
   *
   * Both halves go, and in that order: a key with no endpoint is unreachable
   * clutter, and an endpoint with no key is what an unpaired-but-remembered
   * Mac is supposed to look like.
   */
  async forget(): Promise<void> {
    const id = endpointId(this.host, this.port);
    await forgetKey(id);
    // The names go with it: a name for a Mac this phone is no longer paired
    // with is clutter, and a stale one would greet the next pairing of the same
    // address with somebody else's label.
    forgetDaemonName(id);
    this.nameEpoch++;
    clearEndpoint();
    this.hasStoredKey = false;
    this.keyStorage = "none";
  }

  /** Tear the connection down. Also the correct response to backgrounding. */
  async disconnect(reason = "user"): Promise<void> {
    // Deliberate, so nothing automatic may undo it. See `#userClosed`.
    this.#userClosed = true;
    this.#cancelRetry();
    try {
      await this.#transport?.disconnect(reason);
    } catch {
      /* a disconnect that fails has still stopped being usable */
    }
    this.phase = "closed";
  }

  /**
   * Re-establish the connection with no human action, using what was
   * remembered. Returns true when the connection came back.
   *
   * WHAT THIS FIXES. Backgrounding closes the socket on purpose — the plugin's
   * header explains why an `NWConnection` cannot survive suspension and why a
   * socket that lies about being usable is worse than one that is honestly gone
   * — and that was always meant to be paid back by the app reopening it. Until
   * now nothing did, so a phone that went to sleep came back saying "not on
   * this network" and, once iOS had reclaimed the process, on the pairing
   * screen with the one field nobody can guess left empty.
   *
   * WHAT IT DELIBERATELY DOES NOT DO. It never walks the daemon's alternate
   * addresses and never re-saves the endpoint: a remembered address that worked
   * once is a fact, not a guess, and re-running the guessing walk on every
   * foreground would spend a timeout per address before landing where it
   * started. It never navigates, either — an off-network phone belongs on the
   * last snapshot behind a banner, and the pairing screen is what REVOCATION
   * looks like.
   *
   * SINGLE-FLIGHT. A foreground event and a `visibilitychange` describe the
   * same moment and both call this; the phase gate makes the second one free.
   */
  async reconnect(): Promise<boolean> {
    // A fresh trigger restarts the ladder rather than joining it partway.
    this.#cancelRetry();
    this.#retryStep = 0;
    return this.#attemptReconnect();
  }

  /**
   * Arm the retry ladder WITHOUT dialling now.
   *
   * For the caller that has just finished a failed attempt of its own — boot —
   * and would otherwise pay a second full connect timeout immediately.
   */
  retryLater(): void {
    if (this.#userClosed) return;
    this.#cancelRetry();
    this.#retryStep = 0;
    this.#scheduleRetry();
  }

  /**
   * True while a connection is up or on its way up.
   *
   * A method rather than an inline test at each call site so that the check
   * reads `this.phase` fresh both times. TypeScript narrows a property across
   * an `await`, so the second, post-restore copy of the same three comparisons
   * was reported as unreachable — which is exactly the analysis that would make
   * someone delete the guard that closes the race.
   */
  #inFlight(): boolean {
    const p = this.phase;
    return p === "ready" || p === "connecting" || p === "handshaking";
  }

  async #attemptReconnect(): Promise<boolean> {
    if (this.#userClosed) return false;
    if (this.busy || this.reconnecting) return false;
    if (this.#inFlight()) return false;

    const prev = await this.restore();
    // No stored key is not a failure to report and not something to retry: it
    // is the ordinary state of a device that has never paired, or one whose key
    // was forgotten on purpose. The connect screen is already the right place.
    if (!prev || (prev.key === "" && prev.keyRef === "")) return false;

    // THE GATES AGAIN, because `restore()` awaited — it reads the Keychain
    // across the bridge — and the window it opens is a real one. iOS posts
    // `willEnterForeground` during a COLD LAUNCH as well as on a resume (the
    // device log shows it on every start), so this can be running beside
    // App.svelte's boot dial rather than instead of it. `ChannelTransport`
    // dedupes concurrent connects and would make that merely untidy rather than
    // two sockets, but the untidiness is a second set of `busy`/host/port
    // writes racing the first, and re-reading three booleans is cheaper than
    // reasoning about it.
    if (this.#userClosed || this.busy || this.#inFlight()) return false;

    this.reconnecting = true;
    let ok = false;
    try {
      ok = await this.connect(prev.draft, prev.key, false, [], prev.keyRef);
    } finally {
      this.reconnecting = false;
    }

    // A KEY THE DAEMON REFUSES IS WORSE THAN NO KEY, so it does not survive
    // the refusal. `regenerateRemoteKey` on the Mac rolls the secret and
    // un-pairs every phone; without this the device kept a dead credential
    // that no screen could reach, the ladder correctly stopped retrying, and
    // the connect form came up prefilled with an address whose stored key was
    // silently worthless. Only a refusal that NAMES the key does this — an
    // unreachable daemon and a pin mismatch must not cost a pairing.
    if (!ok && this.refusal?.code === "denied") await this.forget();

    if (ok) {
      this.#retryStep = 0;
      return true;
    }

    // Only keep trying for a failure that retrying could plausibly fix. A
    // refused key, a mismatched pin and a version skew all need a human, and
    // dialling them again on a ladder just burns the daemon's connection slots
    // while the banner already says what to do.
    if (this.diagnosis.retryable) this.#scheduleRetry();
    return false;
  }

  #scheduleRetry(): void {
    const delay = Connection.RETRY_DELAYS_MS[this.#retryStep];
    if (delay === undefined) return; // ladder exhausted; the banner stands
    this.#retryStep += 1;
    this.#retryTimer = setTimeout(() => {
      this.#retryTimer = undefined;
      void this.#attemptReconnect();
    }, delay);
  }

  #cancelRetry(): void {
    if (this.#retryTimer !== undefined) {
      clearTimeout(this.#retryTimer);
      this.#retryTimer = undefined;
    }
  }

  /**
   * Subscribe to a pane.
   *
   * Kept here rather than reached for directly by the terminal component so
   * there is exactly one place that knows a transport can be absent.
   */
  subscribe(pane: string, viewport?: Viewport): Promise<PaneSubscription> {
    const t = this.#transport;
    if (!t) return Promise.reject(new Error("not connected"));
    return t.subscribe(pane, viewport);
  }

  /** Send one command. The store normally does this through the shim; the
   *  terminal screen uses it for the pane classification it needs. */
  request<T = unknown>(
    cmd: string,
    fields?: Record<string, unknown>,
  ): Promise<T> {
    const t = this.#transport;
    if (!t) return Promise.reject(new Error("not connected"));
    return t.request<T>(cmd, fields as never);
  }
}

/**
 * The one failure `diagnose` cannot classify, because it is not a failure.
 *
 * The plugin closes the socket when the app is backgrounded and reports
 * `code: 'backgrounded'`, which reaches here as an Error whose message begins
 * with that word (see `stateError`). It matches none of `diagnose`'s TLS or
 * refusal cues, so it fell through to the catch-all — "Not on <host>'s
 * network", with a paragraph about WiFi, VPNs and a sleeping Mac. That sentence
 * was shown to people whose network was perfect and whose phone had simply been
 * in a pocket, and it is the reason this fix reads as a networking bug rather
 * than a lifecycle one.
 *
 * Returns null for everything else, so `diagnose` keeps every branch it owns.
 */
function backgroundedDiagnosis(
  error: Error | null,
  label: string,
): Diagnosis | null {
  if (!error || !/^backgrounded\b/.test(error.message)) return null;
  return {
    kind: "unreachable",
    title: "Disconnected while in the background",
    detail: `The connection to ${label} was closed when the app was suspended.`,
    hint: "It reconnects on its own when the app comes back to the foreground.",
    retryable: true,
  };
}

export const connection = new Connection();
