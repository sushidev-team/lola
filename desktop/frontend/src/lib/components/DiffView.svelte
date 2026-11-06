<script lang="ts">
  // The diff tab (SUSHI-618): what this session changed against its base branch,
  // read line by line, with comments a human queues on lines and sends back to
  // the coding agent as one message.
  //
  // With `turn` set it shows what that turn changed instead, against the checkpoint before it, in
  // the same layout. Read-only: a line number in one turn's diff means nothing to the agent later,
  // so a comment is only ever anchored on the diff against the base. `rail` and `turnBar` are
  // ChangesView's: what sits above the file list, and the actions on a selected turn.
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
  import { untrack, type Snippet } from "svelte";
  import { DaemonService } from "@bindings/desktop";
  import type { DiffData, DiffFile } from "@bindings/internal/protocol";
  import { store } from "$lib/store.svelte";
  import { feedback } from "$lib/feedback.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { changes } from "$lib/changes.svelte";
  import { agentName } from "$lib/ssh";
  import { observeDiffNavigation } from "$lib/diffnavigation";
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

  let {
    sessionId,
    turn = 0,
    compact = false,
    rail,
    turnBar,
  }: { sessionId: string; turn?: number; compact?: boolean; rail?: Snippet; turnBar?: Snippet } = $props();
  const session = $derived(store.sessionById(sessionId));

  // --- data ------------------------------------------------------------------

  let data = $state<DiffData | null>(null);
  let error = $state("");
  let loading = $state(false);

  // The reload must follow the session ID's VALUE, not the prop: the parent
  // passes `session.id`, so reading the prop subscribes to the whole session
  // object, which every daemon push replaces — the diff then blanked and
  // reloaded about once a second. A $derived re-notifies only when the string
  // actually changes.
  const id = $derived(sessionId);

  // Every load gets a sequence number and only the LATEST may land: the
  // selection can move while git runs, and two Refreshes of the same session
  // can finish out of order.
  let seq = 0;
  async function load() {
    const my = ++seq;
    loading = true;
    try {
      const wanted = turn;
      const d = wanted ? await DaemonService.CheckpointDiff(id, wanted) : await DaemonService.Diff(id);
      if (my !== seq) return;
      data = d;
      if (!wanted) changes.note(id, d);
      if (!d.files?.some((f) => f.path === activeFile)) activeFile = orderedFiles[0]?.path ?? "";
      error = "";
    } catch (err) {
      if (my !== seq) return;
      error = String(err);
    } finally {
      if (my === seq) loading = false;
    }
  }

  $effect(() => {
    id; // reload on selection change
    turn; // and on another turn
    data = null;
    error = "";
    sel = null;
    activeFile = "";
    collapsed = new Set();
    // Untracked: load() runs synchronously up to its first await, and anything
    // it reads there would become a dependency of THIS effect.
    untrack(() => void load());
  });

  const files = $derived<DiffFile[]>(data?.files ?? []);
  const parsed = $derived(new Map(files.map((f) => [f.path, parsePatch(f.patch)])));
  const tree = $derived(flattenTree(buildTree(files.map((f) => f.path))));
  const fileByPath = $derived(new Map(files.map((f) => [f.path, f])));
  // Both panes follow the same directory-first traversal. Use the full tree:
  // folding a navigation folder must not hide or reorder the diff itself.
  const orderedFiles = $derived(tree.filter((node) => node.path).map((node) => fileByPath.get(node.path)!));
  let collapsed = $state(new Set<string>());
  const visibleTree = $derived.by(() => {
    let hiddenBelow = Infinity;
    return tree.filter((node) => {
      if (node.depth > hiddenBelow) return false;
      hiddenBelow = !node.path && collapsed.has(node.key) ? node.depth : Infinity;
      return true;
    });
  });

  function toggleFolder(key: string) {
    const next = new Set(collapsed);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    collapsed = next;
  }
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
  let navigator = $state<HTMLElement | null>(null);
  let activeFile = $state("");
  let navigation = $state.raw<ReturnType<typeof observeDiffNavigation> | undefined>();

  $effect(() => {
    orderedFiles; // Reconnect after refresh, including replaced section nodes.
    if (!scroller) return;
    const observer = observeDiffNavigation(scroller, (path) => { activeFile = path; });
    navigation = observer;
    return () => {
      observer.destroy();
      if (navigation === observer) navigation = undefined;
    };
  });

  // Preserve intentional folds. If the current file is hidden, its nearest
  // visible collapsed ancestor carries the location indicator instead.
  const activeNavKey = $derived(visibleTree.find((node) => node.path
    ? node.path === activeFile
    : collapsed.has(node.key) && activeFile.startsWith(`${node.key.slice(4)}/`))?.key);

  $effect(() => {
    const key = activeNavKey;
    const nav = navigator;
    if (!key || !nav) return;
    const row = Array.from(nav.querySelectorAll<HTMLElement>("[data-nav-key]"))
      .find((element) => element.dataset.navKey === key);
    if (!row || nav.clientHeight === 0) return;
    const bounds = nav.getBoundingClientRect();
    const top = bounds.top + (nav.querySelector<HTMLElement>(".nav-heading")?.offsetHeight ?? 0);
    const item = row.getBoundingClientRect();
    const delta = item.top < top ? item.top - top : item.bottom > bounds.bottom ? item.bottom - bounds.bottom : 0;
    // Minimal reveal, only when the current location changes; never focus a
    // control or mirror the diff's scroll distance onto the independent tree.
    if (delta) nav.scrollTo({ top: nav.scrollTop + delta, behavior: "instant" });
  });

  function jump(path: string) {
    navigation?.jump(path);
  }

  // An edit row asked for a file. A row may name it from the worktree's root or as an absolute path,
  // so the end of the path decides. Waits for the diff and its observer, and asks only once.
  $effect(() => {
    const want = changes.wantedFile(id);
    const nav = navigation;
    if (!want || !data || !nav) return;
    untrack(() => {
      const hit = files.find((f) => f.path === want || want.endsWith(`/${f.path}`));
      if (hit) nav.jump(hit.path);
      changes.taken(id);
    });
  });

  function navigationKey(event: KeyboardEvent) {
    if (event.metaKey || event.ctrlKey || event.altKey) return;
    // Keep native scrolling and button activation, but do not let these keys
    // also move the cockpit's session selection or toggle its fullscreen pane.
    if (["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "PageUp", "PageDown", "Home", "End", " ", "Enter"].includes(event.key)) {
      event.stopPropagation();
    }
  }

  // --- selection + composer ---------------------------------------------------

  let sel = $state<Selection | null>(null);
  let body = $state("");

  function pick(path: string, r: Row, e: MouseEvent) {
    const a = anchorOf(r);
    if (!a || turn) return;
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
    const kind = !r ? "diff-empty" : `diff-${r.kind}`;
    return `${kind}${selected ? " diff-selected" : ""}`;
  }

  function marker(r: Row): string {
    return r.kind === "add" ? "+" : r.kind === "del" ? "-" : " ";
  }

  function statusGlyph(f: DiffFile): string {
    if (f.untracked) return "U";
    return f.status === "added" ? "A" : f.status === "deleted" ? "D" : f.status === "renamed" ? "R" : "M";
  }

  function statusLabel(f: DiffFile): string {
    return f.untracked ? "Untracked" : f.status === "added" ? "Added" : f.status === "deleted" ? "Deleted" : f.status === "renamed" ? "Renamed" : "Modified";
  }

  function statusColor(f: DiffFile): string {
    return f.untracked || f.status === "added" ? "text-good" : f.status === "deleted" ? "text-bad" : "text-faint";
  }

  const count = $derived(feedback.count(sessionId));
  const note = $derived(feedback.noteFor(sessionId));
  // In this view "add a comment" is the click on a line, which means nothing on a turn.
  const lineTitle = $derived(turn ? undefined : "comment on this line (shift-click to select a range)");
</script>

{#snippet fileIcon()}
  <svg class="size-3.5 shrink-0" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.25" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M9.5 1.75H4a1 1 0 0 0-1 1v10.5a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1v-8.5Z" />
    <path d="M9 1.75v4h4M5.5 8.5h5M5.5 11h3" />
  </svg>
{/snippet}

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

<div class="diff-view grid h-full min-h-0 grid-rows-[auto_1fr_auto] bg-panel" class:diff-compact={compact} data-testid="diff-view">
  <!-- Toolbar: what the diff is against, its size, and the view toggles. -->
  <div class="diff-toolbar grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-edge/60 px-4 py-2.5">
    <div class="min-w-0 truncate text-sm text-faint">
      {#if data}
        {#if turn}<span class="num mr-2 font-medium text-ink">#{turn}</span>{/if}
        against <span class="font-mono text-ink-2">{data.base}</span>
        {#if !turn}<span class="ml-1 font-mono">{data.mergeBase.slice(0, 8)}</span>{/if}
        <!-- Everything against the base already says its size on the rail and the strip. -->
        {#if turn || !rail}<span class="ml-3 num text-good">+{totals.add}</span> <span class="num text-bad">−{totals.del}</span>{/if}
        {#if data.truncated}<span class="text-warn"> · truncated — too large to show in full</span>{/if}
      {:else if loading}
        loading diff…
      {/if}
    </div>
    <div class="flex min-w-0 items-center gap-1 text-sm">
      {#if turn && !compact}{@render turnBar?.()}{/if}
      <div class="flex shrink-0 gap-0.5 rounded-md border border-edge/60 bg-canvas p-0.5" role="group" aria-label="Diff layout">
        <Button size="xs" selected={mode === "unified"} class={mode === "unified" ? "bg-sel! text-ink!" : ""} onclick={() => setMode("unified")}>Unified</Button>
        <Button size="xs" selected={mode === "split"} class={mode === "split" ? "bg-sel! text-ink!" : ""} onclick={() => setMode("split")}>Split</Button>
      </div>
      <Button size="xs" icon {loading} aria-label="Refresh" title="Refresh diff" onclick={load}>
        {#if !loading}
          <svg class="size-3.5" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M13 6a5.25 5.25 0 1 0 .15 3.5M13 2.5V6H9.5" />
          </svg>
        {/if}
      </Button>
    </div>
    {#if turn && compact}<div class="col-span-2 flex items-center gap-2">{@render turnBar?.()}</div>{/if}
    {#if compact && files.length}
      <select class="diff-file-picker col-span-2 min-w-0 w-full rounded border border-edge bg-canvas px-2 py-1 text-sm text-ink" aria-label="Changed file" value={activeFile} onchange={(event) => jump(event.currentTarget.value)}>
        {#each orderedFiles as file (file.path)}<option value={file.path}>{file.path} (+{file.additions} −{file.deletions})</option>{/each}
      </select>
    {/if}
  </div>

  <!-- Body: the left column (turns, then the file tree) | file diffs. The column stays when a
       turn changed nothing, or there would be no way to pick another. -->
  <div class="diff-body grid min-h-0 grid-rows-[minmax(0,1fr)]">
    <div class="diff-nav grid min-h-0 min-w-0 border-r border-edge/60 {rail ? 'grid-rows-[minmax(0,auto)_minmax(0,1fr)]' : 'grid-rows-[minmax(0,1fr)]'}">
      {#if rail}<div class="grid max-h-[50vh] min-h-0">{@render rail()}</div>{/if}
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
      <nav bind:this={navigator} onkeydown={navigationKey} class="min-h-0 overflow-auto text-sm" aria-label="changed files">
        <div class="nav-heading sticky top-0 z-10 flex h-6 items-center justify-between px-3.5">
          <span class="label text-faint">Files</span>
          <span class="num text-faint">{files.length}</span>
        </div>
        <div class="px-1.5 pb-3">
        <!-- Navigation rows own their full-width layout, like the app's other nav rows. -->
        {#each visibleTree as n (n.key)}
          {#if n.path}
            {@const f = fileByPath.get(n.path)}
            <button
              type="button"
              data-nav-key={n.key}
              class="file-row nav-row {activeFile === n.path ? 'is-active' : ''}"
              style="--depth: {n.depth}"
              title={`${n.path}${f ? ` · ${statusLabel(f)} · +${f.additions} −${f.deletions}` : ''}`}
              aria-current={activeFile === n.path ? "location" : undefined}
              aria-label={`${n.path}${f ? `, ${statusLabel(f)}` : ''}`}
              onclick={() => jump(n.path)}
            >
              <span class={f ? statusColor(f) : "text-faint"}>{@render fileIcon()}</span>
              <span class="truncate">{n.name}</span>
              {#if f}<span class="file-status font-mono {statusColor(f)}" aria-hidden="true">{statusGlyph(f)}</span>{/if}
            </button>
          {:else}
            <button type="button" data-nav-key={n.key} class="folder-row nav-row" class:root-folder={n.depth === 0} class:contains-active={activeNavKey === n.key} style="--depth: {n.depth}"
              aria-expanded={!collapsed.has(n.key)} onclick={() => toggleFolder(n.key)} title={activeNavKey === n.key ? `${n.key.slice(4)} — contains current file` : n.key.slice(4)}>
              <svg class="folder-chevron size-3" class:closed={collapsed.has(n.key)} viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="m4 6 4 4 4-4" /></svg>
              <svg class="size-3.5 text-faint" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.25" stroke-linejoin="round" aria-hidden="true"><path d="M1.5 4a1 1 0 0 1 1-1h3l1.5 1.5h6.5a1 1 0 0 1 1 1v7a1 1 0 0 1-1 1h-11a1 1 0 0 1-1-1Z" /></svg>
              <span class="truncate">{n.name}</span>
            </button>
          {/if}
        {/each}
        </div>
      </nav>
    </div>

    {#if error}
      <div class="p-4 text-bad">{error}</div>
    {:else if data && files.length === 0}
      <div class="grid place-items-center text-faint">{turn ? "This turn changed no files." : `No changes against ${data.base}.`}</div>
    {:else}
      <!-- A focusable scroll region lets keyboard users read without tabbing through every gutter. -->
      <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
      <div bind:this={scroller} onkeydown={navigationKey} tabindex="0" role="region" aria-label="File diffs" class="diff-scroller min-h-0 min-w-0 overflow-auto font-mono text-sm leading-5">
        {#each orderedFiles as f (f.path)}
          {@const rows = parsed.get(f.path) ?? []}
          <section data-file={f.path} class="diff-file" class:is-current={activeFile === f.path}>
            <header
              class="file-heading sticky top-0 z-10 grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-edge/60 px-4 py-3 font-sans"
            >
              <span class="flex min-w-0 items-center gap-2.5" title={f.oldPath ? `${f.oldPath} → ${f.path}` : f.path}>
                <span class={statusColor(f)}>{@render fileIcon()}</span>
                <span class="truncate text-ink">
                {#if f.oldPath}<span class="text-faint">{f.oldPath} → </span>{/if}<span class="text-faint">{f.path.slice(0, f.path.lastIndexOf("/") + 1)}</span><span class="font-medium">{f.path.slice(f.path.lastIndexOf("/") + 1)}</span>
                {#if f.untracked}<span class="text-sm text-faint"> · untracked</span>{/if}
                </span>
              </span>
              <span class="flex items-center gap-3 text-sm num"
                >{#if f.additions}<span class="text-good">+{f.additions}</span>{/if} {#if f.deletions}<span class="text-bad">−{f.deletions}</span>{/if}</span
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
                <div class="diff-row grid grid-cols-[3rem_3rem_1.5rem_minmax(0,1fr)] {rowBg(r, selected)}">
                  {#if a}
                    <button
                      type="button"
                      class="line-gutter col-span-2 grid grid-cols-2 pr-2 text-right select-none hover:text-ink"
                      title={lineTitle}
                      onclick={(e) => pick(f.path, r, e)}
                    >
                      <span>{r.oldLine || ""}</span><span>{r.newLine || ""}</span>
                    </button>
                    <span class="line-marker text-center select-none">{marker(r)}</span>
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
                        class="diff-row grid grid-cols-[3rem_1.5rem_minmax(0,1fr)] border-edge/40 {side === 0 ? 'border-r' : ''} {rowBg(
                          r,
                          !!r && inSelection(sel, f.path, r),
                        )}"
                      >
                        {#if r}
                          <button
                            type="button"
                            class="line-gutter pr-2 text-right select-none hover:text-ink"
                            title={lineTitle}
                            onclick={(e) => pick(f.path, r, e)}>{side === 0 ? r.oldLine : r.newLine}</button
                          >
                          <span class="line-marker text-center select-none">{marker(r)}</span>
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
    {/if}
  </div>

  <!-- A free note beside the line comments. Sending is the footer strip's: it says how many are
       drafted, and it is where every view keeps what you can do now. A turn takes no comments. -->
  {#if !turn}
    <div class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-t border-edge/60 px-4 py-1.5">
      <textarea
        value={note}
        rows="1"
        placeholder={`General note for ${agentName(session?.agent)} (optional)`}
        oninput={(e) => feedback.setNote(sessionId, e.currentTarget.value)}
        class="max-h-32 min-h-7 w-full resize-y rounded-md border border-edge/60 bg-canvas px-2 py-0.5 text-ink focus:border-accent focus-visible:outline-none!"
      ></textarea>
      <div class="flex items-center gap-2">
        {#if session?.feedbackPending}
          <span class="text-sm text-faint" title="The agent is mid-turn. lola sends it once its pane is waiting.">Earlier feedback queued</span>
        {/if}
        {#if count > 0}
          <Button
            size="xs"
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
      </div>
    </div>
  {/if}
</div>

<style>
  .diff-view { container: session-diff / inline-size; }
  .diff-compact .diff-body { grid-template-columns: minmax(0, 1fr); }
  .diff-compact .diff-nav { display: none; }
  .diff-compact .diff-toolbar { padding: 8px; gap: 8px; }
  @container session-diff (min-width: 720px) {
    .diff-compact .diff-body { grid-template-columns: clamp(11rem, 26%, 16rem) minmax(0, 1fr); }
    .diff-compact .diff-nav { display: grid; }
    .diff-compact .diff-toolbar > select { display: none; }
  }
  .diff-body { grid-template-columns: clamp(13rem, 24%, 21rem) minmax(0, 1fr); }
  .diff-nav, .nav-heading { background: var(--color-canvas); }
  .nav-row {
    display: grid;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    min-height: 28px;
    padding-right: 0.5rem;
    border-radius: 4px;
    text-align: left;
  }
  .nav-row:hover { background: color-mix(in srgb, var(--color-sel) 65%, transparent); }
  .nav-row:focus-visible { outline-offset: -2px; }
  .file-row {
    grid-template-columns: 14px minmax(0, 1fr) 18px;
    padding-left: calc(1.5rem + var(--depth) * 0.75rem);
    color: var(--color-ink);
  }
  /* Where the scroll is, not what is selected: the turn rail's row above owns the full band. */
  .file-row.is-active {
    background: color-mix(in srgb, var(--color-sel) 40%, transparent);
    color: var(--color-ink);
  }
  .folder-row.contains-active { background: color-mix(in srgb, var(--color-sel) 50%, transparent); }
  .file-status { text-align: center; font-weight: 500; }
  .folder-row {
    grid-template-columns: 12px 14px minmax(0, 1fr);
    padding-left: calc(0.25rem + var(--depth) * 0.75rem);
    color: var(--color-ink);
    font-weight: 500;
  }
  .root-folder { margin-top: 0.75rem; }
  .folder-chevron { color: var(--color-faint); }
  .folder-chevron.closed { transform: rotate(-90deg); }
  .diff-scroller { background: var(--color-canvas); }
  .diff-file { background: var(--color-panel); border-bottom: 1px solid var(--color-edge); }
  .diff-file + .diff-file { margin-top: 1rem; border-top: 1px solid var(--color-edge); }
  .file-heading { background: var(--color-panel); }
  .diff-scroller:focus-visible { outline-offset: -2px; }
  .diff-row { min-height: 22px; line-height: 22px; }
  .line-gutter { color: var(--color-faint); background: color-mix(in srgb, var(--color-canvas) 35%, transparent); }
  .line-marker { color: var(--color-faint); }
  .diff-add { background: color-mix(in srgb, var(--color-good) 9%, var(--color-panel)); }
  .diff-del { background: color-mix(in srgb, var(--color-bad) 9%, var(--color-panel)); }
  .diff-add .line-gutter, .diff-add .line-marker { color: var(--color-good); background: color-mix(in srgb, var(--color-good) 8%, transparent); }
  .diff-del .line-gutter, .diff-del .line-marker { color: var(--color-bad); background: color-mix(in srgb, var(--color-bad) 8%, transparent); }
  .diff-hunk, .diff-meta {
    padding-top: 0.375rem;
    padding-bottom: 0.375rem;
    color: var(--color-faint);
    background: color-mix(in srgb, var(--color-sel) 45%, var(--color-panel));
  }
  .diff-empty { background: var(--color-canvas); }
  .diff-selected { background: var(--color-accent-fill); }
  .diff-row:hover .line-gutter { color: var(--color-ink); }
  @media (max-width: 800px) {
    .diff-body { grid-template-columns: 12rem minmax(0, 1fr); }
    .file-row { padding-left: calc(1rem + var(--depth) * 0.5rem); }
    .folder-row { padding-left: calc(0.25rem + var(--depth) * 0.5rem); }
  }
</style>
