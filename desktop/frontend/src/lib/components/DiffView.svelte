<script lang="ts">
  // The diff tab (SUSHI-618): what this session changed against its base branch,
  // read line by line, with comments a human queues on lines and sends back to
  // the coding agent as one message.
  //
  // Selecting: click a line to select it, shift-click another on the same side of
  // the same file to select the range; a composer opens under the selection.
  // Queued comments sit under the last line they cover. "Send to agent" ships
  // the batch through the daemon (cmd=feedback), which types it only into a pane
  // verifiably resting at its prompt and otherwise QUEUES it — so this view never
  // has to know whether the agent is busy.
  //
  // Layout is CSS grid, not flex, throughout: the production WKWebView does not
  // stretch a flex child inside a flex column (see CLAUDE.md, "WebKit ≠ Chrome").
  import { DaemonService } from "@bindings/desktop";
  import type { DiffData, DiffFile } from "@bindings/internal/protocol";
  import { store } from "$lib/store.svelte";
  import { feedback } from "$lib/feedback.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import {
    parsePatch,
    splitRows,
    buildTree,
    flattenTree,
    anchorOf,
    extendSelection,
    inSelection,
    quoteFor,
    langOf,
    tokenize,
    type Row,
    type Selection,
    type TokKind,
  } from "$lib/diff";
  import Button from "./Button.svelte";

  let { sessionId }: { sessionId: string } = $props();
  const session = $derived(store.sessionById(sessionId));

  // --- data ------------------------------------------------------------------

  let data = $state<DiffData | null>(null);
  let error = $state("");
  let loading = $state(false);

  async function load() {
    const id = sessionId;
    loading = true;
    try {
      const d = await DaemonService.Diff(id);
      if (id !== sessionId) return; // the selection moved while git ran
      data = d;
      error = "";
    } catch (err) {
      if (id !== sessionId) return;
      error = String(err);
    } finally {
      if (id === sessionId) loading = false;
    }
  }

  $effect(() => {
    sessionId; // reload on selection change
    data = null;
    error = "";
    sel = null;
    void load();
  });

  const files = $derived<DiffFile[]>(data?.files ?? []);
  const parsed = $derived(new Map(files.map((f) => [f.path, parsePatch(f.patch)])));
  const tree = $derived(flattenTree(buildTree(files.map((f) => f.path))));
  const totals = $derived(
    files.reduce((t, f) => ({ add: t.add + f.additions, del: t.del + f.deletions }), { add: 0, del: 0 }),
  );

  // --- view mode (persisted per viewer) --------------------------------------

  const MODE_KEY = "lola.diff.mode";
  function readMode(): "unified" | "split" {
    try {
      return localStorage.getItem(MODE_KEY) === "split" ? "split" : "unified";
    } catch {
      return "unified";
    }
  }
  let mode = $state<"unified" | "split">(readMode());
  function setMode(m: "unified" | "split") {
    mode = m;
    try {
      localStorage.setItem(MODE_KEY, m);
    } catch {
      /* per-run only */
    }
  }

  // --- file tree navigation ---------------------------------------------------

  let scroller = $state<HTMLDivElement | null>(null);
  let activeFile = $state("");
  function jump(path: string) {
    activeFile = path;
    const el = scroller?.querySelector<HTMLElement>(`[data-file="${CSS.escape(path)}"]`);
    el?.scrollIntoView({ block: "start" });
  }

  // --- selection + composer ---------------------------------------------------

  let sel = $state<Selection | null>(null);
  let body = $state("");

  function pick(path: string, r: Row, e: MouseEvent) {
    const a = anchorOf(r);
    if (!a) return;
    sel = e.shiftKey ? extendSelection(sel, path, a.side, a.line) : { path, side: a.side, start: a.line, end: a.line };
  }

  // The composer sits under the LAST selected row.
  function composerAfter(path: string, r: Row): boolean {
    const a = anchorOf(r);
    return !!sel && !!a && sel.path === path && a.side === sel.side && a.line === sel.end;
  }

  function addComment() {
    if (!sel || !body.trim()) return;
    feedback.add(sessionId, {
      path: sel.path,
      side: sel.side,
      line: sel.start,
      endLine: sel.end,
      quote: quoteFor(parsed.get(sel.path) ?? [], sel),
      body: body.trim(),
    });
    sel = null;
    body = "";
  }

  function cancel() {
    sel = null;
    body = "";
  }

  function composerKey(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation(); // abandon the comment, never "leave fullscreen"
      cancel();
    } else if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      addComment();
    }
  }

  function draftsAfter(path: string, r: Row) {
    const a = anchorOf(r);
    return a ? feedback.draftsAt(sessionId, path, a.side, a.line) : [];
  }

  // --- rendering helpers -----------------------------------------------------

  const TOK: Record<TokKind, string> = {
    plain: "",
    kw: "text-magenta",
    str: "text-good",
    com: "text-faint italic",
    num: "text-orange",
  };

  function rowBg(r: Row | null, selected: boolean): string {
    if (selected) return "bg-accent/20";
    if (!r) return "bg-canvas/60";
    if (r.kind === "add") return "bg-good/10";
    if (r.kind === "del") return "bg-bad/10";
    if (r.kind === "hunk" || r.kind === "meta") return "bg-sel/50 text-faint";
    return "";
  }

  function marker(r: Row): string {
    return r.kind === "add" ? "+" : r.kind === "del" ? "-" : " ";
  }

  function statusGlyph(f: DiffFile): string {
    if (f.untracked) return "U";
    return f.status === "added" ? "A" : f.status === "deleted" ? "D" : f.status === "renamed" ? "R" : "M";
  }

  const count = $derived(feedback.count(sessionId));
  const note = $derived(feedback.noteFor(sessionId));
</script>

{#snippet code(r: Row, path: string)}
  {@const lang = langOf(path)}
  <span class="whitespace-pre-wrap break-all"
    >{#each tokenize(r.text, lang) as t, i (i)}<span class={TOK[t.kind]}>{t.text}</span>{/each}</span
  >
{/snippet}

{#snippet extras(path: string, r: Row)}
  {#each draftsAfter(path, r) as d (d.key)}
    <div class="mx-3 my-1.5 grid grid-cols-[1fr_auto] gap-2 rounded-md border border-edge bg-panel px-3 py-2 font-sans">
      <div class="min-w-0">
        <div class="mb-0.5 text-sm text-faint">
          {d.path}:{d.line}{d.endLine > d.line ? `-${d.endLine}` : ""}{d.side === "old" ? " (removed)" : ""} · queued
        </div>
        <div class="whitespace-pre-wrap text-ink">{d.body}</div>
      </div>
      <Button size="xs" variant="danger" icon aria-label="remove comment" onclick={() => feedback.remove(sessionId, d.key)}
        >×</Button
      >
    </div>
  {/each}
  {#if composerAfter(path, r) && sel}
    <div class="mx-3 my-1.5 rounded-md border border-accent bg-panel p-2 font-sans">
      <div class="mb-1 text-sm text-faint">
        Comment on {sel.path}:{sel.start}{sel.end > sel.start ? `-${sel.end}` : ""}{sel.side === "old"
          ? " (removed line)"
          : ""}
      </div>
      <!-- svelte-ignore a11y_autofocus -->
      <textarea
        bind:value={body}
        autofocus
        rows="3"
        placeholder="What should the agent change here?"
        onkeydown={composerKey}
        class="w-full resize-y rounded-md border border-edge bg-canvas px-2 py-1.5 text-ink focus:border-accent focus-visible:outline-none!"
      ></textarea>
      <div class="mt-1.5 grid grid-cols-[1fr_auto_auto] items-center gap-2">
        <span class="text-sm text-faint">⌘↩ to add · Esc to cancel</span>
        <Button size="xs" onclick={cancel}>Cancel</Button>
        <Button size="xs" variant="primary" disabled={!body.trim()} onclick={addComment}>Add comment</Button>
      </div>
    </div>
  {/if}
{/snippet}

<div class="grid h-full min-h-0 grid-rows-[auto_1fr_auto] bg-panel" data-testid="diff-view">
  <!-- Toolbar: what the diff is against, its size, and the view toggles. -->
  <div class="grid grid-cols-[1fr_auto] items-center gap-3 border-b border-edge/60 px-3 py-1.5">
    <div class="min-w-0 truncate text-sm text-faint">
      {#if data}
        against <span class="font-mono text-ink">{data.base}</span>
        <span class="font-mono">({data.mergeBase.slice(0, 8)})</span> ·
        {files.length} file{files.length === 1 ? "" : "s"} ·
        <span class="text-good">+{totals.add}</span> <span class="text-bad">−{totals.del}</span>
        {#if data.truncated}<span class="text-warn"> · truncated — too large to show in full</span>{/if}
      {:else if loading}
        loading diff…
      {/if}
    </div>
    <div class="flex items-center gap-1">
      <Button size="xs" selected={mode === "unified"} onclick={() => setMode("unified")}>Unified</Button>
      <Button size="xs" selected={mode === "split"} onclick={() => setMode("split")}>Split</Button>
      <Button size="xs" {loading} title="re-read the worktree" onclick={load}>Refresh</Button>
    </div>
  </div>

  <!-- Body: file tree | file diffs -->
  {#if error}
    <div class="p-4 text-bad">{error}</div>
  {:else if data && files.length === 0}
    <div class="flex items-center justify-center text-faint">no changes against {data.base}</div>
  {:else}
    <div class="grid min-h-0 grid-cols-[minmax(10rem,16rem)_1fr]">
      <nav class="min-h-0 overflow-auto border-r border-edge/60 py-1 text-sm" aria-label="changed files">
        {#each tree as n (n.path || `${n.depth}:${n.name}`)}
          {#if n.path}
            {@const f = files.find((x) => x.path === n.path)}
            <button
              type="button"
              class="grid w-full grid-cols-[1fr_auto] gap-2 py-0.5 pr-2 text-left hover:bg-sel/60 {activeFile === n.path
                ? 'bg-sel text-ink'
                : 'text-faint'}"
              style="padding-left: {0.5 + n.depth * 0.75}rem"
              title={n.path}
              onclick={() => jump(n.path)}
            >
              <span class="truncate">{n.name}</span>
              <span class="font-mono text-faint">{f ? statusGlyph(f) : ""}</span>
            </button>
          {:else}
            <div class="truncate py-0.5 text-faint/80" style="padding-left: {0.5 + n.depth * 0.75}rem">{n.name}/</div>
          {/if}
        {/each}
      </nav>

      <div bind:this={scroller} class="min-h-0 overflow-auto font-mono text-[12px] leading-5">
        {#each files as f (f.path)}
          {@const rows = parsed.get(f.path) ?? []}
          <section data-file={f.path} class="border-b border-edge/60">
            <header
              class="sticky top-0 z-10 grid grid-cols-[1fr_auto] gap-3 border-b border-edge/60 bg-panel px-3 py-1.5 font-sans"
            >
              <span class="truncate text-ink">
                {#if f.oldPath}<span class="text-faint">{f.oldPath} → </span>{/if}{f.path}
                {#if f.untracked}<span class="text-sm text-faint"> · untracked</span>{/if}
              </span>
              <span class="text-sm"
                ><span class="text-good">+{f.additions}</span> <span class="text-bad">−{f.deletions}</span></span
              >
            </header>
            {#if f.binary}
              <div class="px-3 py-2 font-sans text-faint">binary file — not shown</div>
            {:else if f.tooLarge}
              <div class="px-3 py-2 font-sans text-faint">diff too large to show</div>
            {:else if mode === "unified"}
              {#each rows as r, i (i)}
                {@const a = anchorOf(r)}
                {@const selected = inSelection(sel, f.path, r)}
                <!-- The row is the click target; a11y via the gutter button. -->
                <div class="grid grid-cols-[3rem_3rem_1rem_1fr] {rowBg(r, selected)}">
                  {#if a}
                    <button
                      type="button"
                      class="col-span-2 grid grid-cols-2 pr-1 text-right text-faint/80 select-none hover:text-accent-ink"
                      title="comment on this line (shift-click to select a range)"
                      onclick={(e) => pick(f.path, r, e)}
                    >
                      <span>{r.oldLine || ""}</span><span>{r.newLine || ""}</span>
                    </button>
                    <span class="text-faint select-none">{marker(r)}</span>
                    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
                    <div class="cursor-pointer pr-3" onclick={(e) => pick(f.path, r, e)}>{@render code(r, f.path)}</div>
                  {:else}
                    <span class="col-span-4 px-3 whitespace-pre-wrap">{r.text}</span>
                  {/if}
                </div>
                {@render extras(f.path, r)}
              {/each}
            {:else}
              {#each splitRows(rows) as sr, i (i)}
                {#if sr.full}
                  <div class="px-3 whitespace-pre-wrap {rowBg(sr.full, false)}">{sr.full.text}</div>
                {:else}
                  <div class="grid grid-cols-2">
                    {#each [sr.left, sr.right] as r, side (side)}
                      <div
                        class="grid grid-cols-[3rem_1rem_1fr] border-edge/40 {side === 0 ? 'border-r' : ''} {rowBg(
                          r,
                          !!r && inSelection(sel, f.path, r),
                        )}"
                      >
                        {#if r}
                          <button
                            type="button"
                            class="pr-1 text-right text-faint/80 select-none hover:text-accent-ink"
                            title="comment on this line (shift-click to select a range)"
                            onclick={(e) => pick(f.path, r, e)}>{side === 0 ? r.oldLine : r.newLine}</button
                          >
                          <span class="text-faint select-none">{marker(r)}</span>
                          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
                          <div class="cursor-pointer pr-2" onclick={(e) => pick(f.path, r, e)}>
                            {@render code(r, f.path)}
                          </div>
                        {/if}
                      </div>
                    {/each}
                  </div>
                  <!-- Context rows sit on both sides with one anchor; render
                       their extras once. -->
                  {#if sr.left && sr.left !== sr.right}{@render extras(f.path, sr.left)}{/if}
                  {#if sr.right}{@render extras(f.path, sr.right)}{/if}
                {/if}
              {/each}
            {/if}
          </section>
        {/each}
      </div>
    </div>
  {/if}

  <!-- The send bar: a free note and the batch's one action. -->
  <div class="grid grid-cols-[1fr_auto] items-end gap-2 border-t border-edge/60 px-3 py-2">
    <textarea
      value={note}
      rows="1"
      placeholder="General note for the agent (optional)"
      oninput={(e) => feedback.setNote(sessionId, e.currentTarget.value)}
      class="max-h-32 min-h-8 w-full resize-y rounded-md border border-edge bg-canvas px-2 py-1 text-ink focus:border-accent focus-visible:outline-none!"
    ></textarea>
    <div class="flex items-center gap-2">
      {#if session?.feedbackPending}
        <span class="text-sm text-warn" title="the agent is mid-turn; lola sends it once its pane is waiting"
          >earlier feedback queued</span
        >
      {/if}
      {#if count > 0}
        <Button
          size="sm"
          variant="danger"
          onclick={() =>
            confirm.ask({
              title: "Discard feedback?",
              body: `Discard ${count} unsent comment${count === 1 ? "" : "s"} for this session?`,
              confirmLabel: "Discard",
              onConfirm: () => feedback.clear(sessionId),
            })}>Discard…</Button
        >
      {/if}
      <Button
        size="sm"
        variant="primary"
        disabled={count === 0}
        loading={feedback.isSending(sessionId)}
        title="deliver every queued comment as one message — typed only when the agent is waiting at its prompt"
        onclick={() => feedback.send(sessionId)}
      >
        Send to agent{count > 0 ? ` (${count})` : ""}
      </Button>
    </div>
  </div>
</div>
