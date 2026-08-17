<script lang="ts">
  import { onMount } from "svelte";
  import { nav } from "$lib/nav.svelte";
  import Modal from "$lib/components/Modal.svelte";
  import Button from "$lib/components/Button.svelte";
  import { DoctorService, DaemonService } from "@bindings/desktop";
  import type { DoctorReportDTO, DoctorResultDTO, CLIInfoDTO } from "@bindings/desktop";

  let report = $state<DoctorReportDTO | null>(null);
  let error = $state("");
  let running = $state(false);

  // The `lola` CLI, reported separately from the check list because it is the
  // one thing here the app can FIX itself. doctor's own "lola cli" row answers
  // "is it on PATH"; this answers "which binary will this app run, and can I
  // install one" — a DMG-only install has a bundled copy that PATH cannot see,
  // and that distinction is the whole reason the daemon failed to start.
  let cli = $state<CLIInfoDTO | null>(null);
  let installing = $state(false);
  let installMsg = $state("");
  let installBad = $state(false);

  async function loadCLI() {
    try {
      cli = await DaemonService.CLIInfo();
    } catch {
      cli = null; // the section simply does not render; never a dead overlay
    }
  }

  async function install() {
    installing = true;
    installMsg = "";
    installBad = false;
    try {
      const res = await DaemonService.InstallCLI();
      installMsg = res.onPath
        ? `Installed at ${res.path}. Open a new terminal and run \`lola tui\`.`
        : `Installed at ${res.path}, but that directory is not on your PATH — add it to use \`lola\` in a terminal.`;
      await loadCLI();
    } catch (err) {
      installMsg = String(err);
      installBad = true;
    } finally {
      installing = false;
    }
  }

  // Re-runnable: after fixing something (installing gh, adding a webhook) the
  // checks have to be run again, and closing + reopening the overlay to do it is
  // needless. The last report stays visible (dimmed) while a re-run is in flight
  // so the panel doesn't flash empty.
  async function run() {
    running = true;
    error = "";
    try {
      report = await DoctorService.Run();
    } catch (err) {
      error = String(err);
    } finally {
      running = false;
    }
  }

  onMount(() => {
    void run();
    void loadCLI();
  });

  // ✓ passing · ✗ failing+critical · ⚠ failing but non-blocking.
  function glyph(r: DoctorResultDTO): { char: string; cls: string } {
    if (r.ok) return { char: "✓", cls: "text-good" };
    if (r.critical) return { char: "✗", cls: "text-bad" };
    return { char: "⚠", cls: "text-warn" };
  }
</script>

<Modal title="doctor" onClose={() => nav.closeOverlay()}>
  {#if error}
    <div class="selectable text-bad">✗ doctor failed: {error}</div>
  {:else if !report}
    <div class="text-faint">running checks…</div>
  {:else if (report.results?.length ?? 0) === 0}
    <div class="px-1 py-8 text-center text-faint">No checks reported.</div>
  {:else}
    <!-- No size class anywhere in the list: the check name is the base 13px and
         its detail is separated by colour alone. -->
    <div class="selectable flex flex-col gap-0.5 transition-opacity" class:opacity-50={running}>
      {#each report.results ?? [] as r (r.name)}
        {@const g = glyph(r)}
        <div class="flex items-start gap-2 rounded px-1 py-1.5 hover:bg-sel/40">
          <span class="w-4 shrink-0 text-center {g.cls}">{g.char}</span>
          <div class="min-w-0 flex-1">
            <span class="font-medium text-ink">{r.name}</span>
            {#if r.detail}<span class="ml-2 text-sm text-faint">{r.detail}</span>{/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}

  {#if cli}
    <!-- Sits below the check list, separated by a rule: it is the one row here
         that carries an ACTION, and mixing a button into the read-only list
         would make every other row look inert by comparison. -->
    <div class="mt-4 border-t border-edge pt-3">
      <div class="flex items-start gap-2">
        <span class="w-4 shrink-0 text-center {cli.found ? 'text-good' : 'text-bad'}">{cli.found ? "✓" : "✗"}</span>
        <div class="min-w-0 flex-1">
          <span class="font-medium text-ink">lola CLI</span>
          {#if cli.found}
            <span class="ml-2 selectable text-sm text-faint">{cli.path} ({cli.source})</span>
            {#if cli.version}<span class="mt-0.5 block text-sm text-faint">{cli.version}</span>{/if}
            {#if cli.skewed}
              <!-- Version skew is not an error — a developer's own build winning
                   over the bundled copy is the documented dev loop — but it is
                   the cause of "the app has a feature the daemon never heard
                   of", so it is stated rather than left to be debugged. -->
              <span class="mt-1 block text-sm text-warn">
                ▲ This CLI ({cli.version}) differs from the copy bundled with the app ({cli.bundledVersion}). Set
                <span class="font-mono text-sm">LOLA_BIN</span> to pin one.
              </span>
            {/if}
          {:else}
            <span class="mt-0.5 block text-sm text-faint">{cli.error}</span>
          {/if}
        </div>
        {#if cli.bundled && cli.source !== "PATH"}
          <Button variant="secondary" disabled={installing} loading={installing} onclick={install}>
            Install CLI
          </Button>
        {/if}
      </div>
      {#if installMsg}
        <p class="mt-2 pl-6 text-sm {installBad ? 'text-bad' : 'text-good'}">{installMsg}</p>
      {/if}
      {#if !cli.bundled && !cli.found}
        <p class="copy mt-2 pl-6 text-sm text-faint">
          Install it from the release archive, or set <span class="font-mono text-sm">LOLA_BIN</span> to its path.
        </p>
      {/if}
    </div>
  {/if}

  {#snippet footer()}
    <div class="flex items-center gap-2">
      {#if running && report}
        <span class="text-faint">re-running checks…</span>
      {:else if report}
        <span class={report.ok ? "text-good" : "text-bad"}>{report.ok ? "✓" : "✗"}</span>
        <span class="selectable text-faint">{report.summary}</span>
      {:else if error}
        <span class="text-bad">✗</span><span class="text-faint">check run failed</span>
      {:else}
        <span class="text-faint">running checks…</span>
      {/if}
      <!-- Re-run in place after fixing a failing check, rather than reopening. -->
      <Button variant="secondary" size="md" class="ml-auto" disabled={running} onclick={run}>
        {running ? "Running…" : "Re-run"}
      </Button>
    </div>
  {/snippet}
</Modal>
