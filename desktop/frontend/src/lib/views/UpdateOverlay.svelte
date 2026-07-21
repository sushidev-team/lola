<script lang="ts">
  import { onMount } from "svelte";
  import { nav } from "$lib/nav.svelte";
  import { updates } from "$lib/update.svelte";
  import Modal from "$lib/components/Modal.svelte";

  // Open with a fresh check unless a check already produced info this session.
  onMount(() => {
    if (!updates.info && !updates.checking) void updates.check(true);
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
    <div class="text-xs text-faint">checking for updates…</div>
  {:else if updates.error && !updates.info}
    <div class="text-xs text-bad">✗ {updates.error}</div>
  {:else if !updates.available}
    <div class="flex flex-col gap-2 py-4 text-center">
      <span class="text-good">✓ you're up to date</span>
      <span class="text-xs text-faint">running v{updates.info?.currentVersion || updates.version}</span>
    </div>
  {:else}
    <div class="flex flex-col gap-3 text-xs">
      <div class="flex items-baseline gap-2">
        <span class="text-faint">v{updates.info?.currentVersion || updates.version}</span>
        <span class="text-faint">→</span>
        <span class="text-base font-semibold text-accent-ink">v{updates.info?.latestVersion}</span>
        {#if updates.info?.publishedAt}
          <span class="ml-auto text-faint">{fmtDate(updates.info.publishedAt)}</span>
        {/if}
      </div>

      {#if updates.info?.assetSize}
        <div class="text-faint">download size: {fmtBytes(updates.info.assetSize)}</div>
      {/if}

      {#if notesList.length}
        <div class="flex max-h-64 flex-col gap-3 overflow-auto rounded border border-edge/70 bg-canvas/40 p-3">
          {#each notesList as r (r.version)}
            <div>
              <div class="font-semibold text-ink">v{r.version}</div>
              {#if r.releaseNotes}
                <pre class="mt-1 font-sans text-[11px] whitespace-pre-wrap text-faint">{r.releaseNotes}</pre>
              {:else}
                <div class="mt-1 text-[11px] text-faint italic">no release notes</div>
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
          <div class="text-faint">
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
    <div class="flex items-center gap-2 text-xs">
      {#if updates.available}
        {#if updates.installing}
          <span class="text-faint">installing — the app will restart…</span>
        {:else if updates.dmgPath}
          <button
            class="rounded border border-accent bg-accent px-2.5 py-1 font-medium text-accent-ink hover:opacity-90"
            onclick={() => updates.install()}>install & restart</button
          >
          <span class="text-faint">lola will quit and reopen on the new version</span>
        {:else if updates.downloading}
          <span class="text-faint">please wait…</span>
        {:else}
          <button
            class="rounded border border-accent bg-accent px-2.5 py-1 font-medium text-accent-ink hover:opacity-90"
            onclick={() => updates.download()}>download</button
          >
          <button
            class="rounded border border-edge px-2.5 py-1 text-ink hover:border-accent"
            onclick={() => {
              void updates.skip();
              nav.closeOverlay();
            }}>skip this version</button
          >
          <button
            class="ml-auto rounded px-2.5 py-1 text-faint hover:text-ink"
            onclick={() => nav.closeOverlay()}>later</button
          >
        {/if}
      {:else}
        <button
          class="rounded border border-edge px-2.5 py-1 text-ink hover:border-accent disabled:opacity-50"
          disabled={updates.checking}
          onclick={() => updates.check(true)}>{updates.checking ? "checking…" : "check again"}</button
        >
        <span class="ml-auto text-faint">v{updates.version}</span>
      {/if}
    </div>
  {/snippet}
</Modal>
