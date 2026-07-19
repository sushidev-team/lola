<script lang="ts">
  import { onMount } from "svelte";
  import { nav } from "$lib/nav.svelte";
  import Modal from "$lib/components/Modal.svelte";
  import { DoctorService } from "@bindings/desktop";
  import type { DoctorReportDTO, DoctorResultDTO } from "@bindings/desktop";

  let report = $state<DoctorReportDTO | null>(null);
  let error = $state("");

  onMount(async () => {
    try {
      report = await DoctorService.Run();
    } catch (err) {
      error = String(err);
    }
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
    <div class="text-xs text-bad">✗ doctor failed: {error}</div>
  {:else if !report}
    <div class="text-xs text-faint">running checks…</div>
  {:else if report.results.length === 0}
    <div class="px-1 py-8 text-center text-xs text-faint">No checks reported.</div>
  {:else}
    <div class="flex flex-col gap-0.5 text-xs">
      {#each report.results as r (r.name)}
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
      {#if report}
        <span class={report.ok ? "text-good" : "text-bad"}>{report.ok ? "✓" : "✗"}</span>
        <span class="text-faint">{report.summary}</span>
      {:else if error}
        <span class="text-bad">✗</span><span class="text-faint">check run failed</span>
      {:else}
        <span class="text-faint">running checks…</span>
      {/if}
    </div>
  {/snippet}
</Modal>
