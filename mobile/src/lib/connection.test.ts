import { describe, it, expect, vi } from "vitest";
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
