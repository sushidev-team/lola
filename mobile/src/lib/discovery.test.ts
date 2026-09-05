import { afterEach, describe, expect, it } from "vitest";
import {
  DISCOVER_TIMEOUT_MS,
  canDiscover,
  candidates,
  discover,
  type Discovered,
} from "./discovery";

// Discovery, without a radio.
//
// What is worth testing here is not "does mDNS work" — that is Network
// framework's job and a device's — but what this module does with what comes
// back, all of which arrived over a NETWORK: an instance name and a TXT record
// are written by whoever is advertising.

interface FakePlugin {
  discover?: (o?: { timeoutMs?: number }) => Promise<{ services?: unknown }>;
}

function install(p: FakePlugin | undefined): void {
  (globalThis as { Capacitor?: unknown }).Capacitor = p
    ? { Plugins: { LolaTransport: p } }
    : {};
}

afterEach(() => {
  delete (globalThis as { Capacitor?: unknown }).Capacitor;
});

const found = (over: Partial<Discovered> = {}): Discovered => ({
  name: "lola on marvin",
  host: "192.168.10.160",
  port: 7717,
  pin: "PIN",
  ...over,
});

describe("the probe", () => {
  it("reports no discovery when the plugin is absent, which is a browser session", () => {
    install(undefined);
    expect(canDiscover()).toBe(false);
  });

  it("reports no discovery when the plugin predates the method", () => {
    install({});
    expect(canDiscover()).toBe(false);
  });

  it("finds the method when it is there", () => {
    install({ discover: async () => ({ services: [] }) });
    expect(canDiscover()).toBe(true);
  });
});

describe("browsing", () => {
  it("passes the timeout and returns what was advertised", async () => {
    let asked = -1;
    install({
      discover: async (o) => {
        asked = o?.timeoutMs ?? -1;
        return { services: [found()] };
      },
    });
    const got = await discover();
    expect(asked).toBe(DISCOVER_TIMEOUT_MS);
    expect(got).toEqual([found()]);
  });

  it("answers [] rather than throwing when there is no plugin", async () => {
    install(undefined);
    await expect(discover()).resolves.toEqual([]);
  });

  it("answers [] when the browse itself fails — a declined permission, no multicast", async () => {
    install({
      discover: async () => {
        throw new Error("local network denied");
      },
    });
    await expect(discover()).resolves.toEqual([]);
  });

  it("drops entries it cannot dial rather than repairing them", async () => {
    install({
      discover: async () => ({
        services: [
          found(),
          { host: "", port: 7717 },
          { host: "10.0.0.5" }, // no port
          { host: "10.0.0.6", port: 0 },
          { host: "10.0.0.7", port: 99999 },
          "not an object",
          null,
        ],
      }),
    });
    const got = await discover();
    expect(got.map((s) => s.host)).toEqual(["192.168.10.160"]);
  });

  it("tolerates a service with no name or pin, which is an older daemon", async () => {
    install({
      discover: async () => ({ services: [{ host: "10.0.0.5", port: 7717 }] }),
    });
    expect(await discover()).toEqual([
      { name: "", host: "10.0.0.5", port: 7717, pin: "" },
    ]);
  });

  it("answers [] for an answer that is not a list", async () => {
    install({ discover: async () => ({ services: { host: "10.0.0.5" } }) });
    expect(await discover()).toEqual([]);
  });
});

describe("choosing what to dial", () => {
  it("keeps a service whose advertised pin matches", () => {
    expect(candidates([found()], "PIN")).toEqual([
      { host: "192.168.10.160", port: 7717, name: "lola on marvin" },
    ]);
  });

  it("drops a service advertising a DIFFERENT pin, which is somebody else's daemon", () => {
    expect(candidates([found({ pin: "OTHER" })], "PIN")).toEqual([]);
  });

  it("keeps a service advertising NO pin: an older daemon is not an impostor", () => {
    // The handshake still judges it — this filter only saves a doomed socket.
    expect(candidates([found({ pin: "" })], "PIN")).toHaveLength(1);
  });

  it("uses the ADVERTISED port, because that is the one the daemon bound", () => {
    expect(candidates([found({ port: 9999 })], "PIN")[0].port).toBe(9999);
  });

  it("drops what the caller has already tried", () => {
    const known = ["192.168.10.160"];
    expect(candidates([found()], "PIN", known)).toEqual([]);
  });

  it("compares known addresses case-insensitively, for a .local name", () => {
    expect(
      candidates([found({ host: "Marvin.local" })], "PIN", ["marvin.local"]),
    ).toEqual([]);
  });

  it("deduplicates a host advertised twice", () => {
    expect(candidates([found(), found()], "PIN")).toHaveLength(1);
  });

  it("keeps everything when this phone has no pin to compare against", () => {
    expect(
      candidates(
        [found({ pin: "A" }), found({ host: "10.0.0.9", pin: "B" })],
        "",
      ),
    ).toHaveLength(2);
  });

  it("preserves order, so a picker does not reshuffle between browses", () => {
    const list = [
      found({ host: "10.0.0.1" }),
      found({ host: "10.0.0.2" }),
      found({ host: "10.0.0.3" }),
    ];
    expect(candidates(list, "PIN").map((c) => c.host)).toEqual([
      "10.0.0.1",
      "10.0.0.2",
      "10.0.0.3",
    ]);
  });
});
