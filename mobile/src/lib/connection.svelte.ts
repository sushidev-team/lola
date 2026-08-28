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
import { diagnose, type Diagnosis } from "./diagnose";
import { endpointId, parsePort, validateDraft, type EndpointDraft, type EndpointProblem } from "./endpoint";
import { loadEndpoint, loadKey, saveEndpoint, storeKey } from "./secretstore";

export { INSECURE_MIN_KEY_LEN };

class Connection {
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

  /** How the endpoint is named in a sentence: the host, or a placeholder. */
  label = $derived(this.host || "the daemon");

  /** The one sentence the connect screen shows. */
  diagnosis = $derived<Diagnosis>(
    diagnose({
      phase: this.phase,
      error: this.error,
      refusal: this.refusal,
      host: this.host,
      label: this.label,
    }),
  );

  ready = $derived(this.phase === "ready");

  #transport: Transport | undefined;
  #offStatus: Unsubscribe | undefined;

  /** The transport, for the terminal screen's pane subscriptions. */
  get transport(): Transport | undefined {
    return this.#transport;
  }

  /**
   * Adopt the app's one Transport. Called once from the bootstrap; safe to call
   * again with the same instance (the status subscription is replaced, not
   * duplicated).
   */
  useTransport(t: Transport): void {
    this.#offStatus?.();
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
  async restore(): Promise<{ draft: EndpointDraft; key: string } | null> {
    const saved = loadEndpoint();
    if (!saved) return null;
    const port = saved.port || 0;
    const key = await loadKey(endpointId(saved.host, port));
    this.hasStoredKey = key !== "";
    return {
      draft: { host: saved.host, port: port ? String(port) : "", spkiPin: saved.spkiPin },
      key,
    };
  }

  /**
   * Validate, remember, and connect.
   *
   * Returns true on success. On a validation failure it sets `problems` and
   * never touches the network — a form that dials before it checks turns a typo
   * into a ten-second timeout and a misleading network error.
   */
  async connect(draft: EndpointDraft, key: string, remember: boolean): Promise<boolean> {
    this.problems = validateDraft(draft, key, INSECURE_MIN_KEY_LEN);
    if (this.problems.length > 0) return false;

    const t = this.#transport;
    if (!t) {
      this.phase = "closed";
      this.error = new Error(
        "No transport is installed in this build. The native plugin provides it; a browser " +
          "dev session has none.",
      );
      return false;
    }

    const port = parsePort(draft.port) ?? 0;
    const endpoint: Endpoint = {
      host: draft.host.trim(),
      port,
      spkiPin: draft.spkiPin.trim(),
      insecureKey: key,
    };

    // Show the target while connecting, so the failure sentence can name it even
    // if the transport never reports a status at all.
    this.host = endpoint.host;
    this.port = port;
    this.busy = true;
    this.error = null;
    this.refusal = null;

    try {
      await t.connect(endpoint);
      if (remember) {
        saveEndpoint({ host: endpoint.host, port, spkiPin: endpoint.spkiPin });
        await storeKey(endpointId(endpoint.host, port), key);
        this.hasStoredKey = true;
      }
      return true;
    } catch (e) {
      // The status listener has usually already recorded the real reason; this
      // only fills in when the rejection came without one.
      if (!this.refusal && !this.error) {
        this.error = e instanceof Error ? e : new Error(String(e));
      }
      if (this.phase === "ready" || this.phase === "connecting" || this.phase === "handshaking") {
        this.phase = "closed";
      }
      return false;
    } finally {
      this.busy = false;
    }
  }

  /** Tear the connection down. Also the correct response to backgrounding. */
  async disconnect(reason = "user"): Promise<void> {
    try {
      await this.#transport?.disconnect(reason);
    } catch {
      /* a disconnect that fails has still stopped being usable */
    }
    this.phase = "closed";
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
  request<T = unknown>(cmd: string, fields?: Record<string, unknown>): Promise<T> {
    const t = this.#transport;
    if (!t) return Promise.reject(new Error("not connected"));
    return t.request<T>(cmd, fields as never);
  }
}

export const connection = new Connection();
