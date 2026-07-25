<script lang="ts">
  import { onMount } from "svelte";
  import { nav } from "$lib/nav.svelte";
  import Modal from "$lib/components/Modal.svelte";
  import { DoctorService } from "@bindings/desktop";
  import type { DoctorReportDTO, DoctorResultDTO } from "@bindings/desktop";

  let report = $state<DoctorReportDTO | null>(null);
  let error = $state("");
  let running = $state(false);

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

  onMount(run);

  // ✓ passing · ✗ failing+critical · ⚠ failing but non-blocking.
  function glyph(r: DoctorResultDTO): { char: string; cls: string } {
    if (r.ok) return { char: "✓", cls: "text-good" };
    if (r.critical) return { char: "✗", cls: "text-bad" };
    return { char: "⚠", cls: "text-warn" };
  }
</script>

<Modal title="doctor" onClose={() => nav.closeOverlay()}>
  {#if error}
    <div class="selectable text-xs text-bad">✗ doctor failed: {error}</div>
  {:else if !report}
    <div class="text-xs text-faint">running checks…</div>
  {:else if (report.results?.length ?? 0) === 0}
    <div class="px-1 py-8 text-center text-xs text-faint">No checks reported.</div>
  {:else}
    <div class="selectable flex flex-col gap-0.5 text-xs transition-opacity" class:opacity-50={running}>
      {#each report.results ?? [] as r (r.name)}
        {@const g = glyph(r)}
        <div class="flex items-start gap-2 rounded px-1 py-1 hover:bg-sel/40">
          <span class="w-4 shrink-0 text-center {g.cls}">{g.char}</span>
          <div class="min-w-0 flex-1">
            <span class="font-medium text-ink">{r.name}</span>
            {#if r.detail}<span class="ml-2 text-faint">{r.detail}</span>{/if}
          </div>
        </div>
      {/each}
    </div>
  {/if}

  {#snippet footer()}
    <div class="flex items-center gap-2 text-xs">
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
      <button
        class="ml-auto rounded border border-edge px-2 py-[1px] text-faint hover:border-accent hover:text-accent-ink disabled:opacity-40"
        disabled={running}
        onclick={run}>{running ? "running…" : "↻ re-run"}</button
      >
    </div>
  {/snippet}
</Modal>
