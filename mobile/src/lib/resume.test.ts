import { describe, expect, it, vi } from "vitest";
import { installResume } from "./resume";
import { ChannelTransport } from "../wailsshim/channeltransport";
import { FakeLink } from "../wailsshim/fakelink";
import { LinkError } from "../wailsshim/pluginerror";

async function setup() {
  const ch = new FakeLink();
  const t = new ChannelTransport({ open: async () => ch });
  const calls: string[] = [];
  const targets = {
    watches: { reset: vi.fn(() => calls.push("reset")), resume: vi.fn(() => calls.push("resume")) },
    bridge: { onEpoch: vi.fn(async () => void calls.push("panes")) },
  };
  return { ch, t, calls, targets };
}

describe("resume", () => {
  it("resumes the watches and the panes on the first ready epoch", async () => {
    const { ch, t, calls, targets } = await setup();
    installResume(t, targets);
    await t.connect({ daemonId: "d" });
    expect(ch.up.epoch).toBe(1);
    expect(calls).toEqual(["resume", "panes"]);
  });

  it("resets when the connection drops and resumes on the next epoch", async () => {
    const { ch, t, calls, targets } = await setup();
    installResume(t, targets);
    await t.connect({ daemonId: "d" });
    calls.length = 0;
    ch.raiseDown(new LinkError("peer_closed", "x"));
    expect(calls).toEqual(["reset"]);
    ch.raiseUp({ epoch: 2 });
    expect(calls).toEqual(["reset", "resume", "panes"]);
  });

  it("resumes when a migration moves a ready connection to a new epoch", async () => {
    const { ch, t, calls, targets } = await setup();
    installResume(t, targets);
    await t.connect({ daemonId: "d" });
    calls.length = 0;
    ch.raiseUp({ epoch: 2 });
    expect(calls).toEqual(["resume", "panes"]);
  });

  it("stops following when unsubscribed", async () => {
    const { t, calls, targets } = await setup();
    const off = installResume(t, targets);
    off();
    await t.connect({ daemonId: "d" });
    expect(calls).toEqual([]);
  });
});
