<script lang="ts">
  // The checkpoints tab (SUSHI-624): one snapshot of the session's worktree per
  // agent turn, recorded by the daemon when the turn ends. Pick one to read what
  // that turn changed; restore the worktree to it when the turn went wrong, or
  // fork a new agent session from it to try another direction.
  //
  // Restore moves FILES only (HEAD and the branch stay put) and the daemon saves
  // the current state as a new checkpoint first, so it is one click to undo —
  // the confirmation says which checkpoint that will be. The daemon refuses a
  // restore while the agent is mid-turn; `restorable` mirrors that so the button
  // can say why before anyone clicks.
  //
  // Layout is CSS grid throughout (see CLAUDE.md, "WebKit ≠ Chrome").
  import { untrack } from "svelte";
  import { DaemonService } from "@bindings/desktop";
  import type { CheckpointInfo, DiffData } from "@bindings/internal/protocol";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { parsePatch, langOf, tokenize, type Row, type TokKind } from "$lib/diff";
  import Button from "./Button.svelte";

  let { sessionId }: { sessionId: string } = $props();
  const session = $derived(store.sessionById(sessionId));

  // Follow the ID's VALUE, not the session object every daemon push replaces
  // (same reason as DiffView).
  const id = $derived(sessionId);
  // A turn ending is what records a checkpoint, and it shows up as the agent
  // axis moving — reload the list on that, not on every push.
  const agentState = $derived(session?.agentState ?? "");

  let list = $state<CheckpointInfo[]>([]);
  let restorable = $state(false);
  let listError = $state("");
  let loading = $state(false);
  let selected = $state(0); // seq; 0 = none
  let busy = $state(false);
  let diff = $state<DiffData | null>(null);
  let diffError = $state("");

  let seq = 0;
  async function loadList() {
    const my = ++seq;
    loading = true;
    try {
      const d = await DaemonService.Checkpoints(id);
      if (my !== seq) return;
      list = d.checkpoints ?? [];
      restorable = d.restorable;
      listError = "";
      // Keep the selection when it still exists; otherwise show the newest.
      if (!list.some((c) => c.seq === selected)) selected = list.at(-1)?.seq ?? 0;
    } catch (err) {
      if (my !== seq) return;
      listError = String(err);
    } finally {
      if (my === seq) loading = false;
    }
  }

  $effect(() => {
    id; // a different session starts over
    list = [];
    selected = 0;
    diff = null;
    untrack(() => void loadList());
  });
  $effect(() => {
    agentState; // the turn ended (or started): there may be a new checkpoint
    untrack(() => void loadList());
  });

  // --- the selected checkpoint's diff ----------------------------------------

  let dseq = 0;
  $effect(() => {
    const s = selected;
    const sid = id;
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

  const current = $derived(list.find((c) => c.seq === selected));
  const newestFirst = $derived([...list].reverse());
  const files = $derived(diff?.files ?? []);
  const totals = $derived(
    files.reduce((t, f) => ({ add: t.add + f.additions, del: t.del + f.deletions }), { add: 0, del: 0 }),
  );

  // --- actions -----------------------------------------------------------------

  function askRestore(c: CheckpointInfo) {
    confirm.ask({
      title: `Restore checkpoint #${c.seq}?`,
      body: `Put ${sessionId}'s files back to checkpoint #${c.seq} (${c.label}).`,
      detail:
        "Every change since is undone in the worktree — including later commits, which stay in history and show up as uncommitted edits. The current state is saved as a new checkpoint first, so you can restore it again. Tell the agent afterwards: it still remembers the discarded changes.",
      confirmLabel: "Restore",
      onConfirm: async () => {
        busy = true;
        await store.restoreCheckpoint(sessionId, c.seq);
        busy = false;
        await loadList();
      },
    });
  }

  function askFork(c: CheckpointInfo) {
    confirm.ask({
      title: `Fork from checkpoint #${c.seq}?`,
      body: `Start a new agent session from ${sessionId} as it was at checkpoint #${c.seq} (${c.label}).`,
      detail:
        "The fork gets its own worktree and branch, holding the checkpoint's files, and shares this session's context folder. This session keeps running untouched.",
      confirmLabel: "Fork",
      onConfirm: async () => {
        busy = true;
        const r = await store.forkCheckpoint(sessionId, c.seq);
        busy = false;
        if (r?.sessionId) nav.select(r.sessionId);
      },
    });
  }

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

  function when(iso: string): string {
    const d = new Date(iso);
    return isNaN(d.getTime()) ? "" : d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  }
</script>

<div class="grid h-full min-h-0 grid-cols-[minmax(12rem,18rem)_1fr] bg-panel" data-testid="checkpoints-view">
  <!-- The list: newest on top, the one a bad turn most likely wants. -->
  <nav class="grid min-h-0 grid-rows-[auto_1fr] border-r border-edge/60" aria-label="checkpoints">
    <div class="grid grid-cols-[1fr_auto] items-center gap-2 border-b border-edge/60 px-3 py-1.5 text-sm text-faint">
      <span>{list.length} checkpoint{list.length === 1 ? "" : "s"}</span>
      <Button size="xs" {loading} title="re-read the checkpoints" onclick={loadList}>Refresh</Button>
    </div>
    <div class="min-h-0 overflow-auto py-1">
      {#if listError}
        <div class="px-3 py-2 text-sm text-bad">{listError}</div>
      {:else if list.length === 0 && !loading}
        <div class="px-3 py-2 text-sm text-faint">
          No checkpoints yet — one is recorded each time the agent ends a turn.
        </div>
      {/if}
      {#each newestFirst as c (c.seq)}
        <!-- A card-shaped row (selectable, two lines), like the other nav rows. -->
        <button
          type="button"
          aria-pressed={c.seq === selected}
          class="grid w-full grid-cols-[auto_1fr_auto] items-baseline gap-2 px-3 py-1 text-left hover:bg-sel/60 {c.seq ===
          selected
            ? 'bg-sel text-ink'
            : 'text-faint'}"
          onclick={() => (selected = c.seq)}
        >
          <span class="font-mono text-sm">#{c.seq}</span>
          <span class="truncate">{c.label}</span>
          <span class="text-sm text-faint">{when(c.created)}</span>
        </button>
      {/each}
    </div>
  </nav>

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
      {#if current}
        <div class="flex items-center gap-1">
          <Button
            size="xs"
            disabled={!restorable || busy}
            title={restorable
              ? "put the worktree's files back to this checkpoint (undoable)"
              : "the agent is mid-turn — wait for it to finish before restoring"}
            onclick={() => askRestore(current)}>Restore…</Button
          >
          <Button
            size="xs"
            disabled={busy}
            title="start a new agent session from this checkpoint on its own branch"
            onclick={() => askFork(current)}>Fork…</Button
          >
        </div>
      {/if}
    </div>

    <div class="min-h-0 overflow-auto font-mono text-[12px] leading-5">
      {#if diffError}
        <div class="p-4 font-sans text-bad">{diffError}</div>
      {:else if current && diff && files.length === 0}
        <div class="p-4 font-sans text-faint">this turn changed no files</div>
      {:else if !current}
        <div class="p-4 font-sans text-faint">select a checkpoint</div>
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
