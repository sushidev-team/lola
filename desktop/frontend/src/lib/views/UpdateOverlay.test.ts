import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";

// UpdateService bridges into the Wails runtime, which is absent under jsdom.
// Hoisted so the tests can vary one answer per case and assert the call args —
// the `force` flag is half of what this file is about.
const { checkMock, skippedMock, openURLMock } = vi.hoisted(() => ({
  checkMock: vi.fn(),
  skippedMock: vi.fn(),
  openURLMock: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  UpdateService: {
    CheckForUpdates: checkMock,
    IsVersionSkipped: skippedMock,
    GetVersion: vi.fn(async () => "0.2.7"),
    ShouldAutoCheck: vi.fn(async () => false),
    SkipVersion: vi.fn(),
    DownloadUpdate: vi.fn(),
    InstallAndRestart: vi.fn(),
  },
}));

vi.mock("$lib/store.svelte", () => ({ store: { openURL: openURLMock } }));
vi.mock("$lib/nav.svelte", () => ({ nav: { closeOverlay: vi.fn(), openOverlay: vi.fn() } }));

import UpdateOverlay from "./UpdateOverlay.svelte";
import { updates } from "$lib/update.svelte";

/** A check result: newer release published, DMG attached. */
const installable = {
  available: true,
  currentVersion: "0.2.7",
  latestVersion: "0.2.8",
  releaseNotes: "* something new",
  publishedAt: "2026-08-17T08:08:15Z",
  downloadURL: "https://github.com/sushidev-team/lola/releases/download/v0.2.8/lola-desktop-0.2.8-universal.dmg",
  browserURL: "https://github.com/sushidev-team/lola/releases/tag/v0.2.8",
  assetName: "lola-desktop-0.2.8-universal.dmg",
  assetSize: 20405191,
  releases: [],
};

/** The same release minutes earlier: published, but its DMG is still being
 *  signed + notarized, so the release carries no macOS asset yet. */
const noBuildYet = { ...installable, downloadURL: "", assetName: "", assetSize: 0 };

const upToDate = { ...installable, available: false, latestVersion: "0.2.7", downloadURL: "", assetName: "" };

describe("UpdateOverlay", () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    // The store is a module singleton; reset the state each case reads.
    updates.info = null;
    updates.checking = false;
    updates.error = "";
    updates.dmgPath = "";
    updates.version = "0.2.7";
    skippedMock.mockResolvedValue(false);
  });

  it("offers the update when the release carries a DMG", async () => {
    checkMock.mockResolvedValue(installable);
    render(UpdateOverlay);

    // The offered version appears twice (the headline and the notes heading).
    expect((await screen.findAllByText("v0.2.8")).length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: "Download" })).toBeInTheDocument();
    expect(screen.queryByText(/up to date/)).toBeNull();
  });

  // The regression this file exists for: for the minutes between a release being
  // published and its notarized DMG being attached — and indefinitely if that
  // job fails — everyone on the previous version was told they were current.
  it("says a newer version exists even when no macOS build is attached to it", async () => {
    checkMock.mockResolvedValue(noBuildYet);
    render(UpdateOverlay);

    expect(await screen.findByText(/no macOS build attached/)).toBeInTheDocument();
    expect(screen.queryByText(/up to date/)).toBeNull();
    // Nothing to fetch, so no download action is offered…
    expect(screen.queryByRole("button", { name: "Download" })).toBeNull();
    // …but the release itself is reachable.
    await fireEvent.click(screen.getByRole("button", { name: "Open release page" }));
    expect(openURLMock).toHaveBeenCalledWith(installable.browserURL);
  });

  it("reports being current when the latest release is the running version", async () => {
    checkMock.mockResolvedValue(upToDate);
    render(UpdateOverlay);

    expect(await screen.findByText(/up to date/)).toBeInTheDocument();
  });

  // Opening the overlay is the explicit "is there something new?", so it must
  // not reuse the answer the launch auto-check left behind, and it must force
  // the backend past its own hour-long release cache.
  it("forces a fresh check on open, even when a previous result is loaded", async () => {
    checkMock.mockResolvedValue(upToDate);
    updates.info = { ...upToDate };

    render(UpdateOverlay);
    await screen.findByText(/up to date/);

    expect(checkMock).toHaveBeenCalledTimes(1);
    expect(checkMock).toHaveBeenCalledWith(true);
  });

  it("re-checks on demand, forcing the cache again", async () => {
    checkMock.mockResolvedValue(upToDate);
    render(UpdateOverlay);
    await screen.findByText(/up to date/);

    await fireEvent.click(screen.getByRole("button", { name: "Check again" }));
    expect(checkMock).toHaveBeenCalledTimes(2);
    expect(checkMock).toHaveBeenLastCalledWith(true);
  });
});
