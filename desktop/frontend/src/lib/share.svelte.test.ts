import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { ShareMembers, ShareOffered, ShareTrust, ShareUntrust, SetShared } = vi.hoisted(() => ({
  ShareMembers: vi.fn(),
  ShareOffered: vi.fn(),
  ShareTrust: vi.fn(),
  ShareUntrust: vi.fn(),
  SetShared: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({ HubService: { ShareMembers, ShareOffered, ShareTrust, ShareUntrust, SetShared } }));

import { SHARE_OUTDATED_MESSAGE, SHARE_POLL_MS, ShareStore } from "./share.svelte";

const member = { deviceId: "d1", name: "Ada · mini", fingerprint: "0000 1111", allowed: false, trusted: false };

beforeEach(() => {
  for (const f of [ShareMembers, ShareOffered, ShareTrust, ShareUntrust, SetShared]) f.mockReset();
  ShareMembers.mockResolvedValue({ members: [member] });
  ShareOffered.mockResolvedValue({ offers: [] });
});
afterEach(() => vi.useRealTimers());

describe("ShareStore", () => {
  it("polls only while a view watches", async () => {
    vi.useFakeTimers();
    const s = new ShareStore();
    expect(s.polling).toBe(false);
    const release = s.watch();
    await vi.waitFor(() => expect(ShareMembers).toHaveBeenCalledTimes(1));
    expect(s.polling).toBe(true);
    await vi.advanceTimersByTimeAsync(SHARE_POLL_MS);
    expect(ShareMembers).toHaveBeenCalledTimes(2);
    release();
    release();
    expect(s.polling).toBe(false);
    await vi.advanceTimersByTimeAsync(SHARE_POLL_MS * 3);
    expect(ShareMembers).toHaveBeenCalledTimes(2);
  });

  it("one failing read does not blank the other", async () => {
    ShareOffered.mockRejectedValue(new Error("hub: offline"));
    const s = new ShareStore();
    await s.refresh();
    expect(s.members).toHaveLength(1);
    expect(s.error).toBe("hub: offline");
    expect(s.loaded).toBe(true);
  });

  it("answers an older daemon with one plain sentence", async () => {
    ShareMembers.mockRejectedValue(new Error('unknown cmd "shareMembers"'));
    const s = new ShareStore();
    await s.refresh();
    expect(s.error).toBe(SHARE_OUTDATED_MESSAGE);
  });

  it("trust sends the fingerprint the person compared and the chosen directions", async () => {
    ShareTrust.mockResolvedValue({ members: [{ ...member, allowed: true }] });
    const s = new ShareStore();
    expect(await s.trust("d1", "0000 1111", { allow: true })).toBe(true);
    expect(ShareTrust).toHaveBeenCalledWith({ deviceId: "d1", fingerprint: "0000 1111", allow: true, trust: false });
    expect(s.members[0].allowed).toBe(true);
  });

  it("a read begun before a successful removal cannot bring the teammate back", async () => {
    let late: ((v: unknown) => void) | undefined;
    ShareMembers.mockImplementationOnce(() => new Promise((r) => (late = r)));
    ShareUntrust.mockResolvedValue({ members: [{ ...member, allowed: false }] });
    const s = new ShareStore();
    const pending = s.refresh();
    expect(await s.untrust("d1", { allow: true })).toBe(true);
    late!({ members: [{ ...member, allowed: true, trusted: true }] });
    await pending;
    expect(s.members[0].allowed).toBe(false);
  });

  it("shows the daemon's refusal of a pin in its own words and pins nothing", async () => {
    ShareTrust.mockRejectedValue(new Error("Error: share: the fingerprint changed since you looked; compare it again"));
    const s = new ShareStore();
    expect(await s.trust("d1", "x", { allow: true })).toBe(false);
    expect(s.error).toBe("share: the fingerprint changed since you looked; compare it again");
    expect(s.busy.d1).toBeUndefined();
  });

  it("busy is per key: a second change on the same teammate waits, another does not", async () => {
    let done!: () => void;
    ShareUntrust.mockImplementation(() => new Promise((r) => (done = () => r({ members: [] }))));
    const s = new ShareStore();
    const first = s.untrust("d1");
    expect(s.busy.d1).toBe(true);
    expect(await s.untrust("d1")).toBe(false);
    expect(ShareUntrust).toHaveBeenCalledTimes(1);
    ShareUntrust.mockResolvedValueOnce({ members: [] });
    expect(await s.untrust("d2")).toBe(true);
    done();
    expect(await first).toBe(true);
  });

  it("setShared returns the daemon's answer, or undefined with the reason on a refusal", async () => {
    SetShared.mockResolvedValueOnce({ session: "s1", on: true, shareId: "x" });
    const s = new ShareStore();
    expect((await s.setShared({ session: "s1", on: true }))?.shareId).toBe("x");
    SetShared.mockRejectedValueOnce(new Error("share: join a team hub first"));
    expect(await s.setShared({ session: "s1", on: true })).toBeUndefined();
    expect(s.error).toBe("share: join a team hub first");
  });
});
