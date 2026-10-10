<script lang="ts">
  // The main pane's checkpoint diff (SUSHI-624): what the turn behind the
  // checkpoint selected in the sidebar's Checkpoints tab (CheckpointList)
  // changed, against the checkpoint before it. The list lives in the sidebar so
  // the tab strip stays for terminals; this pane is reached by picking a row,
  // and the strip's Agent tab is the way back.
  //
  // Layout is CSS grid throughout (see CLAUDE.md, "WebKit ≠ Chrome").
  import { DaemonService } from "@bindings/desktop";
  import type { DiffData } from "@bindings/internal/protocol";
  import { checkpoints } from "$lib/checkpoints.svelte";
  import { terms, AGENT } from "$lib/terms.svelte";
  import { nav } from "$lib/nav.svelte";
  import { parsePatch, langOf, tokenize, type Row, type TokKind } from "$lib/diff";
  import Button from "./Button.svelte";

  let { sessionId }: { sessionId: string } = $props();
  const st = $derived(checkpoints.of(sessionId));
  const selected = $derived(st.selected);

  let diff = $state<DiffData | null>(null);
  let diffError = $state("");

  let dseq = 0;
  $effect(() => {
    const s = selected;
    const sid = sessionId;
    diff = null;
    diffError = "";
    if (!s) return;
    const my = ++dseq;
    DaemonService.CheckpointDiff(sid, s)
      .then((d) => {
        if (my === dseq) diff = d;
      })
      .catch((err) => {
        if (my === dseq) diffError = String(err);
      });
  });

  const current = $derived(st.list.find((c) => c.seq === selected));
  const files = $derived(diff?.files ?? []);
  const totals = $derived(
    files.reduce((t, f) => ({ add: t.add + f.additions, del: t.del + f.deletions }), { add: 0, del: 0 }),
  );

  // --- rendering helpers ---------------------------------------------------------

  const TOK: Record<TokKind, string> = {
    plain: "",
    kw: "text-magenta",
    str: "text-good",
    com: "text-faint italic",
    num: "text-orange",
  };

  function rowBg(r: Row): string {
    if (r.kind === "add") return "bg-good/10";
    if (r.kind === "del") return "bg-bad/10";
    if (r.kind === "hunk" || r.kind === "meta") return "bg-sel/50 text-faint";
    return "";
  }

  function marker(r: Row): string {
    return r.kind === "add" ? "+" : r.kind === "del" ? "-" : " ";
  }

</script>

<div class="grid h-full min-h-0 bg-panel" data-testid="checkpoints-view">
  <!-- The selected checkpoint: its actions, then what that turn changed. -->
  <div class="grid min-h-0 grid-rows-[auto_1fr]">
    <div class="grid grid-cols-[1fr_auto] items-center gap-3 border-b border-edge/60 px-3 py-1.5">
      <div class="min-w-0 truncate text-sm text-faint">
        {#if current}
          <span class="text-ink">#{current.seq} · {current.label}</span>
          <span class="font-mono"> {current.sha.slice(0, 8)}</span>
          {#if diff}
            · against {diff.base} · {files.length} file{files.length === 1 ? "" : "s"} ·
            <span class="text-good">+{totals.add}</span> <span class="text-bad">−{totals.del}</span>
            {#if diff.truncated}<span class="text-warn"> · truncated</span>{/if}
          {/if}
        {/if}
      </div>
      <div class="flex items-center gap-1">
        {#if current}
          <Button
            size="xs"
            disabled={!st.restorable || st.busy}
            title={st.restorable
              ? "put the worktree's files back to this checkpoint (undoable)"
              : "the agent is mid-turn — wait for it to finish before restoring"}
            onclick={() => checkpoints.askRestore(sessionId, current)}>Restore…</Button
          >
          <Button
            size="xs"
            disabled={st.busy}
            title="start a new agent session from this checkpoint on its own branch"
            onclick={() => checkpoints.askFork(sessionId, current, (id) => nav.select(id))}>Fork…</Button
          >
        {/if}
        <Button size="xs" title="back to the agent's terminal" onclick={() => terms.select(sessionId, AGENT)}
          >Close</Button
        >
      </div>
    </div>

    <div class="min-h-0 overflow-auto font-mono text-[12px] leading-5">
      {#if diffError}
        <div class="p-4 font-sans text-bad">{diffError}</div>
      {:else if current && diff && files.length === 0}
        <div class="p-4 font-sans text-faint">this turn changed no files</div>
      {:else if !current}
        <div class="p-4 font-sans text-faint">pick a checkpoint in the sidebar</div>
      {/if}
      {#each files as f (f.path)}
        {@const lang = langOf(f.path)}
        <section class="border-b border-edge/60">
          <header
            class="sticky top-0 z-10 grid grid-cols-[1fr_auto] gap-3 border-b border-edge/60 bg-panel px-3 py-1.5 font-sans"
          >
            <span class="truncate text-ink"
              >{#if f.oldPath}<span class="text-faint">{f.oldPath} → </span>{/if}{f.path}</span
            >
            <span class="text-sm"
              ><span class="text-good">+{f.additions}</span> <span class="text-bad">−{f.deletions}</span></span
            >
          </header>
          {#if f.binary}
            <div class="px-3 py-2 font-sans text-faint">binary file — not shown</div>
          {:else if f.tooLarge}
            <div class="px-3 py-2 font-sans text-faint">diff too large to show</div>
          {:else}
            {#each parsePatch(f.patch) as r, i (i)}
              <div class="grid grid-cols-[3rem_3rem_1rem_1fr] {rowBg(r)}">
                {#if r.kind === "hunk" || r.kind === "meta"}
                  <span class="col-span-4 px-3 whitespace-pre-wrap">{r.text}</span>
                {:else}
                  <span class="pr-1 text-right text-faint/80 select-none">{r.oldLine || ""}</span>
                  <span class="pr-1 text-right text-faint/80 select-none">{r.newLine || ""}</span>
                  <span class="text-faint select-none">{marker(r)}</span>
                  <span class="pr-3 whitespace-pre-wrap break-all"
                    >{#each tokenize(r.text, lang) as t, j (j)}<span class={TOK[t.kind]}>{t.text}</span>{/each}</span
                  >
                {/if}
              </div>
            {/each}
          {/if}
        </section>
      {/each}
    </div>
  </div>
</div>
