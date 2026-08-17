<script lang="ts">
  import { onMount } from "svelte";
  import { nav } from "$lib/nav.svelte";
  import { updates } from "$lib/update.svelte";
  import { store } from "$lib/store.svelte";
  import Modal from "$lib/components/Modal.svelte";
  import Button from "$lib/components/Button.svelte";

  // Open with a FRESH check every time, not just when nothing was loaded yet.
  // Opening this overlay is the one unambiguous "is there something new?" — and
  // the answer it used to reuse was whatever the launch auto-check saw, which
  // for a long-running app is hours old. `check(true)` also clears the backend's
  // release cache, so the two staleness layers lift together.
  onMount(() => {
    if (!updates.checking) void updates.check(true);
  });

  function fmtBytes(n: number): string {
    if (!n || n <= 0) return "";
    const units = ["B", "KB", "MB", "GB"];
    let v = n;
    let i = 0;
    while (v >= 1024 && i < units.length - 1) {
      v /= 1024;
      i++;
    }
    return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
  }

  function fmtDate(iso: string): string {
    if (!iso) return "";
    const d = new Date(iso);
    return isNaN(d.getTime()) ? "" : d.toLocaleDateString();
  }

  // Prefer the per-version changelog (current < v <= latest); fall back to the
  // latest release's notes when the intermediate list is empty.
  const notesList = $derived(
    (updates.info?.releases?.length ?? 0) > 0
      ? (updates.info!.releases ?? [])
      : updates.info?.releaseNotes
        ? [
            {
              version: updates.info.latestVersion,
              releaseNotes: updates.info.releaseNotes,
              publishedAt: updates.info.publishedAt,
            },
          ]
        : [],
  );

  const pct = $derived(Math.round(updates.progress?.percentage ?? 0));
</script>

<Modal title="software update" onClose={() => nav.closeOverlay()} width="620px">
  {#if updates.checking && !updates.info}
    <div class="text-sm text-faint">checking for updates…</div>
  {:else if updates.error && !updates.info}
    <div class="text-bad">✗ {updates.error}</div>
  {:else if !updates.available}
    <div class="flex flex-col gap-2 py-4 text-center">
      <span class="text-good">✓ you're up to date</span>
      <span class="num text-sm text-faint">running v{updates.info?.currentVersion || updates.version}</span>
    </div>
  {:else if !updates.installable}
    <!-- A newer version is out but carries no macOS build yet: the release is
         published minutes before its signed+notarized DMG is attached, and that
         job can also fail outright. Saying so is the whole point — the previous
         version of this screen showed "up to date" here. -->
    <div class="flex flex-col gap-2 py-4 text-center">
      <span class="text-ink">
        v{updates.info?.latestVersion} is out — no macOS build attached to it yet
      </span>
      <span class="num text-sm text-faint">
        running v{updates.info?.currentVersion || updates.version} · the .dmg is signed and notarized after the
        release is published, which takes a few minutes
      </span>
    </div>
  {:else}
    <div class="flex flex-col gap-3">
      <div class="num flex items-baseline gap-2">
        <span class="text-sm text-faint">v{updates.info?.currentVersion || updates.version}</span>
        <span class="text-sm text-faint">→</span>
        <!-- text-xl is the ceiling and brings its own 600: the version you are
             being offered is the one thing this dialog is about. -->
        <span class="text-xl text-accent-ink">v{updates.info?.latestVersion}</span>
        {#if updates.info?.publishedAt}
          <span class="ml-auto text-sm text-faint">{fmtDate(updates.info.publishedAt)}</span>
        {/if}
      </div>

      {#if updates.info?.assetSize}
        <div class="num text-sm text-faint">download size: {fmtBytes(updates.info.assetSize)}</div>
      {/if}

      {#if notesList.length}
        <div class="flex max-h-64 flex-col gap-3 overflow-auto rounded border border-edge/70 bg-canvas/40 p-3">
          {#each notesList as r (r.version)}
            <div>
              <div class="num font-medium text-ink">v{r.version}</div>
              {#if r.releaseNotes}
                <!-- Multi-line prose: `copy` is the only sanctioned line-height
                     deviation and the only measure constraint in the app. -->
                <pre class="copy mt-1 font-sans text-sm whitespace-pre-wrap text-faint">{r.releaseNotes}</pre>
              {:else}
                <div class="mt-1 text-sm text-faint italic">no release notes</div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}

      {#if updates.downloading}
        <div class="flex flex-col gap-1">
          <div class="h-2 overflow-hidden rounded bg-sel/60">
            <div class="h-full bg-accent transition-[width] duration-150" style="width:{pct}%"></div>
          </div>
          <div class="num text-sm text-faint">
            downloading… {pct}%
            {#if updates.progress?.totalBytes}
              · {fmtBytes(updates.progress.downloadedBytes)} / {fmtBytes(updates.progress.totalBytes)}
            {/if}
          </div>
        </div>
      {:else if updates.dmgPath}
        <div class="text-good">✓ downloaded — ready to install</div>
      {/if}

      {#if updates.error}
        <div class="text-bad">✗ {updates.error}</div>
      {/if}
    </div>
  {/if}

  {#snippet footer()}
    <div class="flex items-center gap-2">
      {#if updates.available && !updates.installable}
        {#if updates.info?.browserURL}
          <Button variant="secondary" size="md" onclick={() => store.openURL(updates.info!.browserURL)}>
            Open release page
          </Button>
        {/if}
        <Button size="md" disabled={updates.checking} onclick={() => updates.check(true)}>
          {updates.checking ? "Checking…" : "Check again"}
        </Button>
        <span class="num ml-auto text-sm text-faint">v{updates.version}</span>
      {:else if updates.installable}
        {#if updates.installing}
          <span class="text-faint">installing — the app will restart…</span>
        {:else if updates.dmgPath}
          <Button variant="primary" size="md" onclick={() => updates.install()}>Install & restart</Button>
          <span class="text-sm text-faint">lola will quit and reopen on the new version</span>
        {:else if updates.downloading}
          <span class="text-faint">please wait…</span>
        {:else}
          <Button variant="primary" size="md" onclick={() => updates.download()}>Download</Button>
          <Button
            variant="secondary"
            size="md"
            onclick={() => {
              void updates.skip();
              nav.closeOverlay();
            }}>Skip this version</Button
          >
          <Button size="md" class="ml-auto" onclick={() => nav.closeOverlay()}>Later</Button>
        {/if}
      {:else}
        <Button variant="secondary" size="md" disabled={updates.checking} onclick={() => updates.check(true)}>
          {updates.checking ? "Checking…" : "Check again"}
        </Button>
        <span class="num ml-auto text-sm text-faint">v{updates.version}</span>
      {/if}
    </div>
  {/snippet}
</Modal>
