import { describe, it, expect, vi, afterEach } from "vitest";
import { Connection } from "./connection.svelte";
import type { ConnectionStatus, Endpoint, Transport } from "@mobile/wire";
import { ChannelTransport } from "@mobile/wailsshim/channeltransport";
import { refusalFromPluginError } from "@mobile/wailsshim/pluginerror";

// THE BOOTSTRAP RACE, pinned.
//
// `main.ts` installs the Transport through a dynamic import it deliberately
// does not await before mounting the app. A human typing four values into the
// connect form never notices; a hand-off does, because the plugin retains a
// `dev-launch` link and replays it to the first listener that registers, which
// is `onMount`. On a cold-booted Simulator the connect attempt reliably won
// that race, `connect()` failed with "no transport", and then `useTransport`
// landed a tick later and `#adopt` overwrote `error` with the fresh transport's
// clean status — so the screen showed a filled form, no banner and no
// connection. Silent, and only on a cold boot.

const PIN = "C4td4uyeJMSyxfoAsB3i98Kd6JhkpOTf3Oxipiq+sxI=";
const KEY = "0123456789abcdef0123456789abcdef";
const draft = { host: "127.0.0.1", port: "7717", spkiPin: PIN };

function fakeTransport(): Transport & { connect: ReturnType<typeof vi.fn> } {
  const status: ConnectionStatus = { phase: "idle" };
  return {
    status,
    connect: vi.fn(async (_e: Endpoint) => {}),
    disconnect: vi.fn(async () => {}),
    onStatus: () => () => {},
    request: vi.fn(async () => ({}) as never),
    subscribePane: vi.fn(),
  } as unknown as Transport & { connect: ReturnType<typeof vi.fn> };
}

describe("connect before the bootstrap's transport arrives", () => {
  it("waits for a transport that is still being imported", async () => {
    const c = new Connection();
    const t = fakeTransport();

    // Exactly the launch ordering: the connect attempt is in flight before the
    // dynamic import resolves.
    const attempt = c.connect(draft, KEY, false);
    c.useTransport(t);

    expect(await attempt).toBe(true);
    expect(t.connect).toHaveBeenCalledTimes(1);
    expect(c.error).toBeNull();
  });

  it("still reports 'no transport' when one never arrives", async () => {
    // The bound is what keeps this honest: a browser `npm run dev` session has
    // no plugin and never will, and a device build whose plugin was not synced
    // is in the same position. Both must get the sentence, not a hung button.
    vi.useFakeTimers();
    try {
      const c = new Connection();
      const attempt = c.connect(draft, KEY, false);
      await vi.advanceTimersByTimeAsync(10_000);
      expect(await attempt).toBe(false);
      expect(c.error?.message).toContain("No transport");
      expect(c.busy).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not wait at all once a transport is installed", async () => {
    const c = new Connection();
    c.useTransport(fakeTransport());
    expect(await c.connect(draft, KEY, false)).toBe(true);
  });
});

describe("a wrong access key, end to end", () => {
  // THE FAILURE THIS PINS. Four correct values and one wrong key produced
  // "Not on 127.0.0.1's network / Nothing answered. The address may be wrong,
  // or the daemon is not running." — byte-identical to the screen for a dead
  // port, on a machine where the daemon was provably listening. The daemon does
  // send `denied` before closing, `diagnose` does have a branch that names the
  // field to retype, and the branch never fired because nothing between the
  // plugin's rejection and `status.refusal` carried the code.
  //
  // Driven from the shape Capacitor actually produces, through the real
  // transport, into the real diagnosis — not a synthetic ErrPayload handed
  // straight to diagnose(), which is what passed while the app was broken.

  function pluginRejects(err: unknown): ChannelTransport {
    return new ChannelTransport({
      // Exactly what capacitorchannel's catch does with a failed
      // LolaTransport.connect().
      open: async () => {
        throw refusalFromPluginError(err) ?? err;
      },
      handshake: "channel",
    });
  }

  it("names the key rather than the network", async () => {
    const c = new Connection();
    c.useTransport(
      pluginRejects(
        // The exact shape the bridge delivers: Capacitor nests a plugin's
        // rejection `data` under a `data` key rather than merging it.
        Object.assign(new Error("the daemon refused the connection (denied) [daemon: denied]"), {
          code: "rejected",
          data: { daemonCode: "denied" },
        }),
      ),
    );

    expect(await c.connect(draft, "deadbeefdeadbeefdeadbeef", false)).toBe(false);
    expect(c.diagnosis.kind).toBe("rejected");
    expect(c.diagnosis.title).toMatch(/refused this key/i);
    expect(c.diagnosis.title).not.toMatch(/network/i);
  });

  it("still says 'not on the network' when nothing answered", async () => {
    // The other side of the same coin: a genuine transport failure must not be
    // dressed up as a refusal now that refusals are recorded.
    const c = new Connection();
    c.useTransport(
      pluginRejects(Object.assign(new Error("the connection timed out"), { code: "timeout" })),
    );

    expect(await c.connect(draft, KEY, false)).toBe(false);
    expect(c.diagnosis.kind).toBe("unreachable");
  });

  it("names which side is behind on a version skew", async () => {
    const c = new Connection();
    c.useTransport(
      pluginRejects(
        Object.assign(new Error("refused"), {
          code: "protocol",
          data: { daemonCode: "unsupported_version", minV: 2, maxV: 3 },
        }),
      ),
    );

    expect(await c.connect(draft, KEY, false)).toBe(false);
    expect(c.diagnosis.kind).toBe("version");
    expect(c.diagnosis.detail).toContain("2");
  });
});

// THE HOST IS A GUESS AND THE DAEMON SAID SO.
//
// A Mac commonly has several private addresses at once — Wi-Fi, a wired dock, a
// VM bridge — and the daemon reports all of them because it cannot know which
// one the phone shares a network with. Committing to the first and reporting
// "unreachable" blames the network for what is really a guess, on a machine
// that already listed the alternatives.
describe("trying the addresses the daemon offered", () => {
  it("falls through to an address that routes when the first does not", async () => {
    const c = new Connection();
    const t = fakeTransport();
    t.connect.mockImplementation(async (e: Endpoint) => {
      if (e.host !== "192.168.0.196") throw new Error("host is down");
    });
    c.useTransport(t);

    const ok = await c.connect({ ...draft, host: "192.168.20.3" }, KEY, false, [
      "192.168.0.196",
      "127.0.0.1",
    ]);

    expect(ok).toBe(true);
    expect(t.connect).toHaveBeenCalledTimes(2);
    // The one that worked is the one the UI names, not the one first offered.
    expect(c.host).toBe("192.168.0.196");
  });

  it("reports failure once every address has been tried", async () => {
    const c = new Connection();
    const t = fakeTransport();
    t.connect.mockImplementation(async () => {
      throw new Error("host is down");
    });
    c.useTransport(t);

    const ok = await c.connect(draft, KEY, false, ["10.0.0.2", "10.0.0.3"]);

    expect(ok).toBe(false);
    expect(t.connect).toHaveBeenCalledTimes(3);
    expect(c.busy).toBe(false);
  });

  it("stops at a refusal instead of walking the rest of the list", async () => {
    // The daemon ANSWERED and said no — a wrong key, a pin that does not match
    // its certificate — and every other address reaches the same daemon and
    // gets the same answer. Continuing would turn one clear "rejected" into
    // several seconds of timeouts ending in "unreachable": slower, and wrong.
    //
    // The refusal is pushed through the real status seam rather than assigned
    // on the Connection, because that is how it actually arrives: the plugin
    // reports a status before the connect promise rejects.
    const c = new Connection();
    let emit: ((s: ConnectionStatus) => void) | null = null;
    const t = fakeTransport();
    (t as unknown as { onStatus: Transport["onStatus"] }).onStatus = (fn) => {
      emit = fn;
      return () => {};
    };
    t.connect.mockImplementation(async () => {
      emit?.({ phase: "closed", refusal: { code: "denied", message: "bad key" } });
      throw new Error("refused");
    });
    c.useTransport(t);

    const ok = await c.connect(draft, KEY, false, ["10.0.0.2", "10.0.0.3"]);

    expect(ok).toBe(false);
    expect(t.connect).toHaveBeenCalledTimes(1);
    expect(c.refusal).not.toBeNull();
  });

  it("does not dial the same address twice when it is also an alternate", async () => {
    const c = new Connection();
    const t = fakeTransport();
    t.connect.mockImplementation(async () => {
      throw new Error("host is down");
    });
    c.useTransport(t);

    await c.connect(draft, KEY, false, ["127.0.0.1", "10.0.0.2"]);

    expect(t.connect).toHaveBeenCalledTimes(2);
  });
});

// ---------------------------------------------------------------------------
// RECONNECT AFTER STANDBY.
//
// The defect these pin, end to end: the phone loses its connection while
// asleep, comes back, and says "not found, maybe on a different network" — with
// nothing wrong with the network. Two independent halves caused it. The plugin
// closes the socket on the way into the background on purpose and nothing ever
// reopened it; and once iOS reclaimed the process the bearer key was gone,
// because there was no Keychain and the key had been living in a Map. The
// second half is Swift. This is the first.
// ---------------------------------------------------------------------------

const STORED = { host: "10.0.0.7", port: 7717, spkiPin: PIN };

function rememberEndpoint() {
  globalThis.localStorage?.setItem("lola.mobile.endpoint", JSON.stringify(STORED));
}

/** A native secret store holding one key. See secretstore.test.ts. */
function rememberKey(key = KEY) {
  const values = new Map<string, string>([[`${STORED.host}:${STORED.port}`, key]]);
  (globalThis as { Capacitor?: unknown }).Capacitor = {
    Plugins: {
      LolaTransport: {
        secretSet: vi.fn(async () => {}),
        secretGet: vi.fn(async ({ key: k }: { key: string }) => ({ value: values.get(k) ?? null })),
        secretDelete: vi.fn(async () => {}),
      },
    },
    PluginHeaders: [
      {
        name: "LolaTransport",
        methods: [{ name: "secretSet" }, { name: "secretGet" }, { name: "secretDelete" }],
      },
    ],
  };
}

function forget() {
  globalThis.localStorage?.clear();
  delete (globalThis as { Capacitor?: unknown }).Capacitor;
}

describe("coming back from the background", () => {
  afterEach(forget);

  it("reconnects with the stored key and no human action", async () => {
    rememberEndpoint();
    rememberKey();
    const c = new Connection();
    const t = fakeTransport();
    c.useTransport(t);

    expect(await c.reconnect()).toBe(true);
    expect(t.connect).toHaveBeenCalledTimes(1);
    // The endpoint that was remembered, and the key that was never on screen.
    expect(t.connect.mock.calls[0][0]).toMatchObject({
      host: STORED.host,
      port: STORED.port,
      insecureKey: KEY,
    });
  });

  it("does nothing when the user disconnected on purpose", async () => {
    // `Sessions.svelte` disconnects and navigates to the connect screen. A
    // foreground event arriving there must not silently re-dial the daemon the
    // user has just chosen to leave — that is the difference between "the app
    // dropped" and "I left".
    rememberEndpoint();
    rememberKey();
    const c = new Connection();
    const t = fakeTransport();
    c.useTransport(t);
    await c.disconnect();

    expect(await c.reconnect()).toBe(false);
    expect(t.connect).not.toHaveBeenCalled();
  });

  it("re-arms once the user connects again", async () => {
    rememberEndpoint();
    rememberKey();
    const c = new Connection();
    const t = fakeTransport();
    c.useTransport(t);
    await c.disconnect();
    await c.connect(draft, KEY, false);
    t.connect.mockClear();

    // Phase is whatever the fake left it at; force the closed state a real
    // background teardown produces.
    c.phase = "closed";
    expect(await c.reconnect()).toBe(true);
  });

  it("does not dial while a connection is already up", async () => {
    rememberEndpoint();
    rememberKey();
    const c = new Connection();
    const t = fakeTransport();
    c.useTransport(t);
    c.phase = "ready";

    expect(await c.reconnect()).toBe(false);
    expect(t.connect).not.toHaveBeenCalled();
  });

  it("gives up quietly when no key was stored", async () => {
    // A device that has never paired, or one whose key was forgotten. Not a
    // failure to report and not something to retry: the connect screen is
    // already the right place, and it is where the app already is.
    rememberEndpoint();
    const c = new Connection();
    const t = fakeTransport();
    c.useTransport(t);

    expect(await c.reconnect()).toBe(false);
    expect(t.connect).not.toHaveBeenCalled();
    expect(c.error).toBeNull();
  });

  it("retries on a ladder when the daemon is unreachable, then stops", async () => {
    vi.useFakeTimers();
    try {
      rememberEndpoint();
      rememberKey();
      const c = new Connection();
      const t = fakeTransport();
      t.connect.mockRejectedValue(new Error("no route to host"));
      c.useTransport(t);

      await c.reconnect();
      expect(t.connect).toHaveBeenCalledTimes(1);

      // The whole ladder, and then silence: a phone off its network for a day
      // must not open a socket every few seconds against a daemon that allows
      // eight of them.
      for (const step of Connection.RETRY_DELAYS_MS) await vi.advanceTimersByTimeAsync(step + 1);
      expect(t.connect).toHaveBeenCalledTimes(1 + Connection.RETRY_DELAYS_MS.length);

      await vi.advanceTimersByTimeAsync(120_000);
      expect(t.connect).toHaveBeenCalledTimes(1 + Connection.RETRY_DELAYS_MS.length);
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not retry a refusal, which needs a human and a field", async () => {
    vi.useFakeTimers();
    try {
      rememberEndpoint();
      rememberKey();
      const c = new Connection();

      // A fake that mirrors what `ChannelTransport.doConnect` really does: it
      // records the refusal on `status` BEFORE it throws, which is the only
      // reason `diagnose` can name a wrong key rather than a wrong network.
      const refused = refusalFromPluginError({
        code: "rejected",
        reason: "refused",
        data: { daemonCode: "denied" },
      });
      if (!refused) throw new Error("a denied bearer key must classify as a refusal");
      const listeners: ((s: ConnectionStatus) => void)[] = [];
      const t = fakeTransport();
      t.onStatus = ((l: (s: ConnectionStatus) => void) => {
        listeners.push(l);
        return () => {};
      }) as Transport["onStatus"];
      t.connect.mockImplementation(async () => {
        const s: ConnectionStatus = {
          phase: "closed",
          error: refused,
          refusal: { code: refused.code, message: refused.message },
        };
        for (const l of listeners) l(s);
        throw refused;
      });
      c.useTransport(t);

      await c.reconnect();
      expect(t.connect).toHaveBeenCalledTimes(1);
      await vi.advanceTimersByTimeAsync(120_000);
      expect(t.connect).toHaveBeenCalledTimes(1);
      expect(c.diagnosis.kind).toBe("rejected");
    } finally {
      vi.useRealTimers();
    }
  });

  it("names the background as the reason rather than blaming the network", async () => {
    // The exact sentence the operator reported. `diagnose` has no branch for
    // the plugin's deliberate teardown, so it fell through to the catch-all —
    // "Not on <host>'s network", with a paragraph about WiFi and VPNs — on a
    // phone whose network was perfect and which had simply been in a pocket.
    const c = new Connection();
    c.host = STORED.host;
    c.phase = "closed";
    c.error = new Error("backgrounded: the app entered the background");

    expect(c.diagnosis.title).not.toContain("network");
    expect(c.diagnosis.title).toBe("Disconnected while in the background");
    expect(c.diagnosis.retryable).toBe(true);
  });

  it("says it is reconnecting while it is reconnecting", async () => {
    rememberEndpoint();
    rememberKey();
    const c = new Connection();
    const t = fakeTransport();
    let release: () => void = () => {};
    t.connect.mockImplementation(() => new Promise<void>((r) => (release = r)));
    c.useTransport(t);

    const attempt = c.reconnect();
    await vi.waitFor(() => expect(c.reconnecting).toBe(true));
    expect(c.diagnosis.title).toContain("Reconnecting");
    release();
    await attempt;
    expect(c.reconnecting).toBe(false);
  });
});
