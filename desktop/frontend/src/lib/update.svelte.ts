// The self-update store. Mirrors the store.svelte.ts shape: runes state fed by
// the UpdateService bindings + the `update:download-progress` push event, with
// each action wrapping one bound call. Components read `updates.available` etc.
// and call `updates.download()` — they never touch the bindings directly.
//
// Flow: init() loads the compiled version and (interval-gated) auto-checks →
// check() populates `info` → download() streams the DMG (progress events) →
// install() swaps the bundle and quits. The backend is authoritative on whether
// a newer version exists; the frontend only layers the skip/dismiss UX on top.

import { Events } from "@wailsio/runtime";
import { UpdateService } from "@bindings/desktop";
import type { UpdateInfoDTO, UpdateProgressDTO } from "@bindings/desktop";

class UpdateStore {
  version = $state("dev");
  info = $state<UpdateInfoDTO | null>(null);
  checking = $state(false);
  error = $state("");
  progress = $state<UpdateProgressDTO | null>(null);
  downloading = $state(false);
  installing = $state(false);
  /** Path to the downloaded DMG once fetched; enables the install step. */
  dmgPath = $state("");

  private started = false;

  /** A newer, installable release exists and hasn't been suppressed. */
  available = $derived(
    !!this.info?.available &&
      !!this.info?.downloadURL &&
      !!this.info?.latestVersion &&
      this.info.latestVersion !== this.info.currentVersion,
  );

  /** Subscribe to progress events, load the version, kick the auto-check. Idempotent. */
  async init() {
    if (this.started) return;
    this.started = true;

    Events.On("update:download-progress", (e) => {
      const p = e.data as UpdateProgressDTO;
      this.progress = p;
      if (p?.status === "complete") {
        this.downloading = false;
        if (p.filePath) this.dmgPath = p.filePath;
      } else if (p?.status === "error") {
        this.downloading = false;
        this.error = p.error || "download failed";
      }
    });

    try {
      this.version = await UpdateService.GetVersion();
    } catch {
      /* keep the "dev" default */
    }
    try {
      if (await UpdateService.ShouldAutoCheck()) await this.check(false);
    } catch {
      /* auto-check is best-effort; a manual check surfaces errors */
    }
  }

  /**
   * Query the latest release. A manual check surfaces errors and always shows an
   * available update; an auto check stays silent on error and honours the
   * user's skipped version so the footer badge doesn't reappear.
   */
  async check(manual = true) {
    this.checking = true;
    this.error = "";
    try {
      const info = await UpdateService.CheckForUpdates();
      if (!manual && info.available && (await UpdateService.IsVersionSkipped(info.latestVersion))) {
        this.info = { ...info, available: false };
      } else {
        this.info = info;
      }
    } catch (err) {
      this.error = String(err);
      if (manual) this.info = null;
    } finally {
      this.checking = false;
    }
  }

  /** Download the DMG. Progress arrives via the event handler above; the return
   *  value is the authoritative saved path. */
  async download() {
    if (!this.info?.downloadURL) return;
    this.downloading = true;
    this.error = "";
    this.progress = null;
    this.dmgPath = "";
    try {
      this.dmgPath = await UpdateService.DownloadUpdate(this.info.downloadURL);
    } catch (err) {
      this.error = String(err);
    } finally {
      this.downloading = false;
    }
  }

  /** Swap the bundle and relaunch. The app quits inside this call, so nothing
   *  after the await runs on success. */
  async install() {
    if (!this.dmgPath) return;
    this.installing = true;
    this.error = "";
    try {
      await UpdateService.InstallAndRestart(this.dmgPath);
    } catch (err) {
      this.error = String(err);
      this.installing = false;
    }
  }

  /** Suppress the current latest version so it stops nagging. */
  async skip() {
    if (!this.info?.latestVersion) return;
    try {
      await UpdateService.SkipVersion(this.info.latestVersion);
    } catch {
      /* non-fatal */
    }
    if (this.info) this.info = { ...this.info, available: false };
  }
}

export const updates = new UpdateStore();
