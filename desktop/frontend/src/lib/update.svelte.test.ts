import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { service, on, unsubscribe } = vi.hoisted(() => ({
  service: {
    GetVersion: vi.fn(),
    GetUpdateSettings: vi.fn(),
    ShouldAutoCheck: vi.fn(),
    CheckForUpdates: vi.fn(),
    IsVersionSkipped: vi.fn(),
  },
  on: vi.fn(),
  unsubscribe: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({ UpdateService: service }));
vi.mock("@wailsio/runtime", () => ({ Events: { On: on } }));

import { updates } from "./update.svelte";

const release = {
  available: true,
  currentVersion: "0.2.7",
  latestVersion: "0.2.8",
  releaseNotes: "New release",
  publishedAt: "2026-10-21T00:00:00Z",
  downloadURL: "https://example.com/lola.dmg",
  browserURL: "https://example.com/release",
  assetName: "lola.dmg",
  assetSize: 100,
  releases: [],
};

describe("automatic update checks", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.resetAllMocks();
    on.mockReturnValue(unsubscribe);
    service.GetVersion.mockResolvedValue("0.2.7");
    service.GetUpdateSettings.mockResolvedValue({ autoCheck: true });
    service.ShouldAutoCheck.mockResolvedValue(false);
    service.CheckForUpdates.mockResolvedValue(release);
    service.IsVersionSkipped.mockResolvedValue(false);
    updates.info = null;
    updates.error = "";
    updates.checking = false;
    updates.downloading = false;
    updates.installing = false;
    updates.dmgPath = "";
  });

  afterEach(() => {
    updates.stop();
    vi.useRealTimers();
  });

  it("checks at startup even when the saved interval is not due, and initializes once", async () => {
    await updates.init();
    await updates.init();

    expect(updates.version).toBe("0.2.7");
    expect(updates.available).toBe(true);
    expect(service.CheckForUpdates).toHaveBeenCalledExactlyOnceWith(false);
    expect(on).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(1);
  });

  it("checks periodically when due, including after a manual check resets the cadence", async () => {
    const day = 24 * 60 * 60 * 1000;
    let lastCheck = Date.now();
    service.CheckForUpdates.mockImplementation(async () => {
      lastCheck = Date.now();
      return release;
    });
    service.ShouldAutoCheck.mockImplementation(async () => Date.now() - lastCheck > day);
    await updates.init();
    await vi.advanceTimersByTimeAsync(day / 2);
    expect(service.CheckForUpdates).toHaveBeenCalledTimes(1);

    await updates.check();
    expect(service.CheckForUpdates).toHaveBeenLastCalledWith(true);
    await vi.advanceTimersByTimeAsync(day);
    expect(service.CheckForUpdates).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(service.CheckForUpdates).toHaveBeenCalledTimes(3);
    expect(service.CheckForUpdates).toHaveBeenLastCalledWith(false);
    await vi.advanceTimersByTimeAsync(day + 60_000);
    expect(service.CheckForUpdates).toHaveBeenCalledTimes(4);
  });

  it("honours disabled checks and notices when they become enabled", async () => {
    service.GetUpdateSettings.mockResolvedValue({ autoCheck: false });
    await updates.init();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(service.CheckForUpdates).not.toHaveBeenCalled();

    service.ShouldAutoCheck.mockResolvedValue(true);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(service.CheckForUpdates).toHaveBeenCalledExactlyOnceWith(false);
  });

  it("keeps skipped releases hidden on both startup and periodic checks", async () => {
    service.IsVersionSkipped.mockResolvedValue(true);
    service.ShouldAutoCheck.mockResolvedValue(true);
    await updates.init();
    expect(updates.available).toBe(false);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(updates.available).toBe(false);
    expect(service.CheckForUpdates).toHaveBeenCalledTimes(2);

    await updates.check();
    expect(updates.available).toBe(true);
  });

  it("retries quietly after a failed check and preserves the previous result", async () => {
    await updates.init();
    service.ShouldAutoCheck.mockResolvedValue(true);
    service.CheckForUpdates.mockRejectedValueOnce(new Error("offline"));
    await vi.advanceTimersByTimeAsync(60_000);
    expect(updates.available).toBe(true);
    expect(updates.error).toBe("");
    await vi.advanceTimersByTimeAsync(60_000);
    expect(service.CheckForUpdates).toHaveBeenCalledTimes(3);
  });

  it("recovers from a failed startup preferences read", async () => {
    service.GetUpdateSettings.mockRejectedValueOnce(new Error("bridge unavailable"));
    service.ShouldAutoCheck.mockResolvedValue(true);
    await updates.init();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(service.CheckForUpdates).toHaveBeenCalledExactlyOnceWith(false);
  });

  it("does not overlap an in-flight check or restart its timer after stop", async () => {
    let resolve!: (value: typeof release) => void;
    service.CheckForUpdates.mockReturnValueOnce(new Promise((done) => { resolve = done; }));
    const init = updates.init();
    await vi.advanceTimersByTimeAsync(0);
    await updates.check();
    await vi.advanceTimersByTimeAsync(120_000);
    expect(service.CheckForUpdates).toHaveBeenCalledTimes(1);

    updates.stop();
    resolve(release);
    await init;
    expect(vi.getTimerCount()).toBe(0);
    expect(unsubscribe).toHaveBeenCalledTimes(1);
  });

  it.each(["downloading", "installing", "dmgPath"] as const)("pauses checks while %s is set", async (field) => {
    await updates.init();
    service.ShouldAutoCheck.mockResolvedValue(true);
    if (field === "dmgPath") updates.dmgPath = "/tmp/lola.dmg";
    else updates[field] = true;
    await vi.advanceTimersByTimeAsync(60_000);
    expect(service.CheckForUpdates).toHaveBeenCalledTimes(1);
  });

  it("cleans up and supports a fresh mount", async () => {
    await updates.init();
    updates.stop();
    await vi.advanceTimersByTimeAsync(120_000);
    expect(service.ShouldAutoCheck).not.toHaveBeenCalled();
    expect(unsubscribe).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
    await updates.init();
    expect(service.CheckForUpdates).toHaveBeenCalledTimes(2);
  });
});
