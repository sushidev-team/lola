<script lang="ts">
  // The story: what the agent said, what actually ran and what you said, interleaved
  // in order, because that interleaving is the point. Three voices, one rail, and the
  // rail is itself a fact: solid while connected, accent while you drove, dashed when
  // nothing was up.
  //
  // While focused it owns the keyboard like a terminal does: every bare key it does
  // not list is swallowed, so `x` cannot ask to kill a session from in here. Esc
  // hands focus back to the app.
  import { tick, untrack } from "svelte";
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import {
    isEnded,
    isAnswer,
    FILTERS,
    agentName,
    clockTime,
    deliveryLine,
    dockCards,
    droveFor,
    transcriptLabel,
    elementRing,
    evidenceTargets,
    foldRows,
    formatMs,
    isClaim,
    itemMatches,
    matchesFilter,
    maybeAnsweredInPane,
    railKinds,
    blockView,
    type Row,
    type RailKind,
    type SSHElement as SSHElementType,
    type SSHTimelineItem,
  } from "$lib/ssh";
  import { sshClock } from "$lib/ssh.svelte";
  import Button from "../Button.svelte";
  import BoardMarker from "../BoardMarker.svelte";
  import Disclosure from "../Disclosure.svelte";
  import SSHBlock from "./SSHBlock.svelte";
  import SSHElement from "./SSHElement.svelte";
  import SSHPromptCard from "./SSHPromptCard.svelte";
  import SSHConnCard from "./SSHConnCard.svelte";
  import SSHWidgetRow from "./SSHWidgetRow.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const agent = $derived(agentName(session?.agent));
  const active = $derived(view.active());
  const items = $derived(view.tabItems);
  const filter = $derived(view.filter);
  const gone = $derived(session?.agentState === "exited" || session?.agentState === "dead");

  // Bookkeeping about who did what is the Facts view's job, the audit record. Elsewhere the
  // entries themselves show it: your commands and the agent's look different.
  const quiet = (i: SSHTimelineItem) =>
    filter !== "facts" && (i.system?.kind === "go" || i.system?.kind === "wheel_taken" || (i.system?.kind === "handed_back" && !i.system.note && !i.system.hasTranscript));
  const visible = $derived(items.filter((i) => matchesFilter(i, filter) && !quiet(i) && !emptyMessage(i)));
  const emptyMessage = (i: SSHTimelineItem) => !!i.human && !i.human.text?.trim() && !i.human.transcript;
  // This tab's. Another tab's question counts on that tab's label.
  const prompts = $derived(active?.prompt && !view.answeredPrompts.has(active.prompt.id) ? [active] : []);
  // Signing in with no question recognized, in the place that question's card will take.
  const signingIn = $derived(active?.state === "authenticating" && !active.prompt ? [active] : []);
  // Gone once its connection has its next question, or a sign-in has landed.
  const promptNote = $derived.by(() => {
    const n = view.promptNote;
    if (!n || prompts.some((c) => c.id === n.conn)) return "";
    const state = view.connections.find((c) => c.id === n.conn)?.state;
    if (n.signIn && state !== "connecting" && state !== "authenticating") return "";
    return n.text;
  });
  const answerItem = (i: SSHTimelineItem) => i.element?.kind === "widget" || (i.element?.kind === "narration" && (isAnswer(i.element.title) || !!i.element.body));
  const keep = $derived(evidenceTargets(items));
  const rows = $derived(foldRows(visible, filter, view.openedFolds, keep));

  // A run of widgets is one line: their pages are in the dock, and twelve rows of nothing but
  // names would push the actual work off the screen.
  const widgetAt = (i: number) => {
    const r = rows[i];
    return r?.kind === "item" && r.item.element?.kind === "widget" ? r.item.element : null;
  };
  const followsWidget = (i: number) => !!widgetAt(i) && !!widgetAt(i - 1);
  function widgetRun(i: number): SSHElementType[] {
    const out: SSHElementType[] = [];
    for (let j = i, w = widgetAt(j); w; w = widgetAt(++j)) out.push(w);
    return out;
  }

  // The rail is computed over ALL items and then read per visible row, so a filter
  // never rewrites what happened to the connection.
  const railById = $derived.by(() => {
    const kinds = railKinds(items, active);
    const m = new Map<string, RailKind>();
    items.forEach((it, i) => m.set(it.id, kinds[i]));
    return m;
  });
  function railOf(row: Row): RailKind {
    const it = row.kind === "fold" ? row.items[0] : row.item;
    return railById.get(it.id) ?? "down";
  }

  // Item ids are the daemon's, but a selector built from one is still escaped.
  function rowSelector(id: string): string {
    const safe = typeof CSS !== "undefined" && CSS.escape ? CSS.escape(id) : id;
    return `[data-row="${safe}"]`;
  }

  // --- scroll ---------------------------------------------------------------
  let scroller = $state<HTMLDivElement | null>(null);
  let stuck = true;
  let newCount = $state(0);
  let lastCount = 0;
  let back = $state<number | null>(null);

  function atBottom(): boolean {
    const el = scroller;
    return !el || el.scrollHeight - el.scrollTop - el.clientHeight <= 40;
  }
  function onScroll() {
    stuck = atBottom();
    if (stuck) newCount = 0;
  }
  function toBottom() {
    if (scroller) scroller.scrollTop = scroller.scrollHeight;
    stuck = true;
    newCount = 0;
  }
  // Stick while the reader is within 40px of the bottom. Otherwise never move them.
  $effect(() => {
    const n = visible.length;
    const added = n - lastCount;
    lastCount = n;
    items; // a running block's preview line can change the height too
    untrack(() => {
      if (stuck) void tick().then(toBottom);
      else if (added > 0) newCount += added;
    });
  });

  // --- digging --------------------------------------------------------------
  $effect(() => {
    const j = view.jump;
    if (!j || j.n === view.seen.jump) return;
    view.seen.jump = j.n;
    untrack(() => void landOn(j.block));
  });

  async function landOn(target: string) {
    const isItem = target.startsWith("item:");
    const id = isItem ? target.slice(5) : target;
    const item = isItem ? items.find((i) => i.id === id) : items.find((i) => i.block?.id === id);
    if (!item) return;
    if (!matchesFilter(item, filter)) view.filter = "everything";
    // A jump must land on something visible, so a fold holding it opens.
    for (const r of foldRows(items.filter((i) => matchesFilter(i, view.filter)), view.filter, view.openedFolds, keep)) {
      if (r.kind === "fold" && r.items.some((x) => x.id === item.id)) view.openedFolds.add(r.id);
    }
    back = scroller?.scrollTop ?? null;
    stuck = false;
    await tick();
    const el = scroller?.querySelector(rowSelector(item.id));
    // A block lands at the top, because the first thing a dig shows is the exact
    // command. Centring a tall expanded plate clips that line under the filter row.
    el?.scrollIntoView?.({ block: item.block ? "start" : "center" });
    view.cursor = item.id;
    if (item.block) {
      view.flashBlock = item.block.id;
      setTimeout(() => {
        if (view.flashBlock === item.block?.id) view.flashBlock = "";
      }, 200);
    }
  }

  function goBack() {
    if (scroller && back != null) scroller.scrollTop = back;
    back = null;
    stuck = atBottom();
  }

  // --- find -----------------------------------------------------------------
  let finding = $state(false);
  let query = $state("");
  let findAt = $state(0);
  let findEl = $state<HTMLInputElement | null>(null);
  const found = $derived(query.trim() ? items.filter((i) => itemMatches(i, query)) : []);
  $effect(() => {
    const n = view.findFocus;
    if (n === view.seen.find) return;
    view.seen.find = n;
    finding = true;
    void tick().then(() => findEl?.focus());
  });
  function walk(d: number) {
    if (!found.length) return;
    findAt = (findAt + d + found.length) % found.length;
    view.jump = { block: `item:${found[findAt].id}`, n: (view.jump?.n ?? 0) + 1 };
  }

  // --- keyboard -------------------------------------------------------------
  $effect(() => {
    const n = view.streamFocus;
    if (n === view.seen.stream) return;
    view.seen.stream = n;
    // `t`: a waiting card comes first, because that is what the keyboard is for.
    if (dockCards(view.connections, view.items, active?.id ?? "").some((c) => c.kind === "approval" || c.kind === "go")) {
      view.dockFocus++;
      return;
    }
    if (!view.cursor) view.cursor = rowItemIds().at(-1) ?? "";
    scroller?.focus();
  });

  function rowItemIds(): string[] {
    return rows.flatMap((r, i) => (r.kind === "fold" ? [r.id] : followsWidget(i) ? [] : [r.item.id]));
  }

  function move(d: number) {
    const ids = rowItemIds();
    if (!ids.length) return;
    const i = ids.indexOf(view.cursor);
    const at = i < 0 ? (d > 0 ? 0 : ids.length - 1) : Math.max(0, Math.min(ids.length - 1, i + d));
    view.cursor = ids[at];
    void tick().then(() => scroller?.querySelector(rowSelector(ids[at]))?.scrollIntoView?.({ block: "nearest" }));
  }

  function cursorItem(): SSHTimelineItem | undefined {
    return items.find((i) => i.id === view.cursor);
  }

  function onKey(e: KeyboardEvent) {
    const t = e.target as HTMLElement;
    if (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT") return;
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === "Tab") return;
    const cur = cursorItem();
    const blk = cur?.block;
    let handled = true;
    switch (e.key) {
      case "j":
      case "ArrowDown":
        move(1);
        break;
      case "k":
      case "ArrowUp":
        move(-1);
        break;
      case "Enter":
      case "ArrowRight": {
        if (t.tagName === "BUTTON" || t.tagName === "A") return;
        if (view.cursor.startsWith("fold-")) view.openedFolds.add(view.cursor);
        else if (blk?.status === "pending_approval") view.dockFocus++;
        else if (blk) view.openBlock(blk.id);
        break;
      }
      case "ArrowLeft":
        if (blk) view.expanded.delete(blk.id);
        break;
      case "s":
        if (blk && view.expanded.has(blk.id)) {
          const cur = view.streams.get(blk.id);
          view.streams.set(blk.id, cur === "stderr" ? "stdout" : cur === "stdout" ? "stderr" : blk.status === "failed" && blk.stderrBytes ? "stdout" : "stderr");
        }
        break;
      case "a":
        if (blk) ssh.askAbout(sessionId, blk.id);
        break;
      case "X":
        if (blk?.status === "running") void ssh.stop(sessionId, blk);
        break;
      case "/":
        finding = true;
        void tick().then(() => findEl?.focus());
        break;
      case "Escape":
        scroller?.blur();
        break;
      // The stream owns the keyboard, except for Drive: taking the wheel must stay one
      // key away wherever focus is. These go on to the app's handler.
      case "W":
      case "A":
      case "?":
        return;
      default:
        handled = e.key.length === 1;
    }
    if (handled) {
      e.preventDefault();
      e.stopPropagation();
    }
  }

  const promptPending = $derived(prompts[0]?.host ?? "");
  const announce = $derived.by(() => {
    const last = items[items.length - 1];
    if (!last) return "";
    if (last.block) return `${last.block.id} ${blockView(last.block).result}`;
    if (last.element) return `${agent} says ${last.element.title ?? ""}`;
    return "";
  });

  function emptyLine(): string {
    if (filter === "facts") return "No commands yet.";
    if (filter === "findings") return `${agent} has not posted any findings or theories yet.`;
    if (active?.wheel === "human") return "";
    if (active?.state === "connected") {
      return session?.agentState === "idle" || session?.agentState === "waiting_input"
        ? `Connected. ${agent} is waiting for you to tell it what to look at.`
        : `Connected. ${agent} has not run anything yet.`;
    }
    return "";
  }
</script>

{#snippet rail(kind: RailKind)}
  {#if kind === "human"}
    <span class="absolute inset-y-0 left-[19px] w-0.5 bg-accent" aria-hidden="true"></span>
  {:else if kind === "agent"}
    <span class="absolute inset-y-0 left-5 w-px bg-edge/60" aria-hidden="true"></span>
  {:else}
    <span class="absolute inset-y-0 left-5 border-l border-dashed border-edge/60" aria-hidden="true"></span>
  {/if}
{/snippet}

{#snippet marker(it: SSHTimelineItem)}
  {#if it.block}
    {@const v = blockView(it.block)}
    {#if it.block.actor === "human"}
      <span class="h-2 w-2 bg-accent" aria-hidden="true"></span>
    {:else}
      <span class="bg-canvas"><BoardMarker kind={v.marker} live={it.block.status === "running"} tone={v.markerCls === "text-warn" ? "warn" : ""} /></span>
    {/if}
  {:else if it.element?.kind === "widget"}
    <!-- Its row has a dot of its own, and two side by side read as noise. -->
  {:else if it.element}
    {@const ring = elementRing(it.element)}
    <span class="relative inline-flex h-2 w-2 items-center justify-center rounded-full bg-canvas {ring}" aria-hidden="true">
      {#if it.element.kind === "hypothesis" && it.element.state === "confirmed"}<span class="h-1 w-1 rounded-full bg-ink"></span>{/if}
    </span>
  {:else if it.human}
    <span class="h-2 w-2 bg-accent" aria-hidden="true"></span>
  {:else}
    <span class="h-1 w-1 rounded-[1px] bg-faint" aria-hidden="true"></span>
  {/if}
{/snippet}

{#snippet human(it: SSHTimelineItem)}
  {@const h = it.human}
  {#if h}
    {@const d = deliveryLine(h, session?.agent ?? "", gone)}
    <span class="sr-only">You said</span>
    <div class="ml-auto w-fit max-w-[80%] rounded-lg rounded-br-sm bg-accent-fill/70 px-3 py-1.5" data-voice="you">
      <span class="copy selectable whitespace-pre-wrap break-words {h.recalled ? 'text-faint line-through' : 'text-ink'}">{h.text}</span>
      {#if h.transcript}
        <div class="relative mt-1">
          <Disclosure label={transcriptLabel(items, it)} class="-ml-1.5">
            <pre class="selectable mt-1 ml-1.5 max-h-60 overflow-auto rounded border border-edge/60 bg-canvas px-2 py-1.5 font-mono text-sm whitespace-pre-wrap text-ink">{h.transcript}</pre>
          </Disclosure>
          {#if !d.canRecall}<span class="num absolute top-1.5 right-0 text-sm text-faint">{d.text}</span>{/if}
        </div>
      {/if}
    </div>
    {#if !h.transcript || d.canRecall}
      <div class="mt-0.5 flex items-center justify-end gap-1.5 px-1 text-sm text-faint">
        <span class="num">{d.text}</span>
        {#if d.canRecall}<Button size="xs" class="h-5!" onclick={() => ssh.recall(sessionId, it.id)}>Recall</Button>{/if}
      </div>
    {/if}
    {#if maybeAnsweredInPane(items, it, session?.agentState, sshClock.now)}
      <div class="mt-0.5 flex items-center gap-1.5 px-1 text-sm text-faint">
        {agent} may have answered in its own terminal.
        <Button size="xs" class="h-5!" onclick={() => ssh.showAgentPane(sessionId)}>Show</Button>
      </div>
    {/if}
  {/if}
{/snippet}

<div class="relative grid min-h-0 grid-rows-[auto_minmax(0,1fr)] overflow-hidden">
  <!-- Filter row, sticky by being outside the scroller. The legend both teaches and confirms. -->
  <div class="flex min-h-8 flex-wrap items-center gap-x-3 gap-y-1 border-b border-edge/40 px-3 py-1">
    <span class="flex items-center gap-0.5" role="group" aria-label="Filter the timeline">
      {#each FILTERS as f (f.id)}
        <Button size="xs" selected={filter === f.id} onclick={() => (view.filter = f.id)}>{f.label}</Button>
      {/each}
    </span>
    {#if finding}
      <span class="flex items-center gap-1.5">
        <input
          bind:this={findEl}
          class="w-44 rounded border border-edge bg-canvas px-2 py-0.5 text-sm text-ink outline-none placeholder:text-placeholder focus:border-accent"
          placeholder="Find in this timeline"
          aria-label="Find in this timeline"
          bind:value={query}
          oninput={() => (findAt = 0)}
          onkeydown={(e) => {
            e.stopPropagation();
            if (e.key === "Enter" || e.key === "ArrowDown") {
              e.preventDefault();
              walk(1);
            } else if (e.key === "ArrowUp") {
              e.preventDefault();
              walk(-1);
            } else if (e.key === "Escape") {
              e.preventDefault();
              finding = false;
              query = "";
              scroller?.focus();
            }
          }}
        />
        {#if query.trim()}
          {#if found.length}
            <span class="num text-sm text-faint">{findAt + 1} of {found.length}</span>
          {:else}
            <span class="text-sm text-faint">Nothing here matches. Output has its own find.</span>
          {/if}
        {/if}
      </span>
    {/if}
  </div>

  <!-- role="log" with aria-live off, plus one short announcer below: a screen reader
       hears new claims and block outcomes in a short form, and never output. -->
  <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
  <div
    bind:this={scroller}
    role="log"
    aria-label="SSH timeline"
    aria-live="off"
    tabindex="0"
    onscroll={onScroll}
    onkeydown={onKey}
    class="min-h-0 overflow-y-auto py-2 outline-none"
  >
    <div class="w-full" data-stream-column>
    {#if !view.loaded}
      <div class="grid gap-2 px-3 py-1" aria-hidden="true">
        {#each [0, 1, 2] as i (i)}<div class="ml-10 h-8 rounded-md bg-sel/40"></div>{/each}
      </div>
    {:else if view.predates}
      <div class="grid justify-items-start gap-2 px-10 py-3 text-faint">
        <p>This daemon predates SSH sessions. Restart it to use them.</p>
        <Button variant="secondary" size="sm" onclick={() => ssh.restartDaemon()}>Restart daemon</Button>
      </div>
    {:else if view.error}
      <div class="grid justify-items-start gap-2 px-10 py-3 text-faint">
        <p>Could not load this timeline. Lola keeps recording it.</p>
        <Button variant="secondary" size="sm" onclick={() => view.fetch()}>Try again</Button>
      </div>
    {:else if rows.length === 0 && emptyLine()}
      <p class="px-10 py-3 text-faint">{emptyLine()}</p>
    {/if}

    {#each rows as row, i (row.kind === "fold" ? row.id : row.item.id)}
      {@const prev = rows[i - 1]}
      {#if row.kind === "fold"}
        <div
          data-row={row.id}
          class="relative grid grid-cols-[40px_minmax(0,1fr)] py-1.5 pr-3 {view.cursor === row.id ? 'bg-sel/30' : ''}"
        >
          {@render rail(railOf(row))}
          <div class="relative flex items-start justify-center pt-1.5"><span class="bg-canvas"><BoardMarker kind="pass" /></span></div>
          <button
            type="button"
            data-plate
            class="grid grid-cols-[minmax(0,1fr)_auto] rounded-md border border-edge/60 bg-panel px-2.5 py-1.5 text-left font-mono text-sm transition-colors hover:bg-sel/30"
            onclick={() => view.openedFolds.add(row.id)}
          >
            <span class="num text-ink">{row.items.length} commands · all exit 0 · {formatMs(row.totalMs)} total</span>
            <span class="text-faint" aria-hidden="true">▸</span>
          </button>
        </div>
      {:else if !followsWidget(i)}
        {@const it = row.item}
        {@const claim = isClaim(it)}
        {@const prevClaim = prev?.kind === "item" && isClaim(prev.item) && !answerItem(prev.item)}
        {@const tight = it.element?.kind === "narration" && prev?.kind === "item" && prev.item.element?.kind === "narration"}
        <div
          data-row={it.id}
          data-kind={it.kind}
          class="ssh-fade-in relative grid scroll-mt-1 grid-cols-[40px_minmax(0,1fr)] pr-3 {tight ? 'py-0.5' : 'py-1.5'} {view.cursor === it.id ? 'bg-sel/30' : ''}"
        >
          {@render rail(railById.get(it.id) ?? "down")}
          <!-- items-start: a stretched marker's canvas backing would erase the rail below it. -->
          <div class="relative flex items-start justify-center {it.block ? 'pt-1.5' : 'pt-[5px]'}">{@render marker(it)}</div>
          <div class="min-w-0">
            {#if claim && !prevClaim && !answerItem(it)}<div class="mb-0.5 text-sm font-medium text-faint">{agent}</div>{/if}
            {#if it.block}
              <span class="sr-only">Lola recorded</span>
              <SSHBlock {sessionId} block={it.block} {filter} />
            {:else if it.element?.kind === "widget"}
              <div class="flex min-w-0 flex-wrap gap-1.5" data-widget-run>
                {#each widgetRun(i) as w (w.id)}<SSHWidgetRow {sessionId} element={w} />{/each}
              </div>
            {:else if it.element}
              <SSHElement {sessionId} element={it.element} />
            {:else if it.human}
              {@render human(it)}
            {:else if it.system}
              <div class="font-mono text-sm text-faint" data-voice="lola">
                <div class="flex items-baseline gap-3">
                  <span class="selectable min-w-0 flex-1">
                    <span class="sr-only">Lola recorded </span>{it.system.text}
                  </span>
                  <span class="num shrink-0">{clockTime(it.at)}</span>
                </div>
                {#if it.system.kind === "handed_back"}
                  <div class="mt-0.5 font-sans text-accent-ink">{droveFor(items, it)}</div>
                {/if}
                {#if it.system.note}
                  <div class="copy mt-1 rounded-md bg-sel/50 px-2.5 py-1.5 font-sans text-ink"><span class="mr-1.5 text-sm text-accent-ink">You</span>{it.system.note}</div>
                {/if}
              </div>
            {/if}
          </div>
        </div>
      {/if}
    {/each}

    <!-- Sticky, so a question stays in sight for someone reading further up. -->
    {#if prompts.length || signingIn.length || promptNote}
      <div class="sticky bottom-0 z-10 grid gap-1.5 bg-canvas pt-1.5 pr-3 pb-1 pl-10" data-prompts>
        {#each signingIn as c (c.id)}
          <SSHConnCard {sessionId} conn={c} kind="auth" />
        {/each}
        {#each prompts as c (c.id)}
          {#if c.prompt}
            {#key c.prompt.id}
              <SSHPromptCard {sessionId} conn={c} prompt={c.prompt} />
            {/key}
          {/if}
        {/each}
        {#if promptNote}
          <p class="text-sm text-faint" role="status" data-prompt-note>{promptNote}</p>
        {/if}
      </div>
    {/if}
    </div>
  </div>

  <div class="sr-only" aria-live="polite">{promptPending ? `${promptPending} is asking for input.` : announce}</div>

  <!-- Pills above the composer. Digging must not lose your place. -->
  {#if back != null || newCount > 0}
    <div class="pointer-events-none absolute inset-x-0 bottom-2 flex justify-center gap-2">
      {#if back != null}
        <span class="pointer-events-auto"><Button variant="secondary" size="xs" class="bg-panel! shadow-md" onclick={goBack}>← Back to where you were</Button></span>
      {/if}
      {#if newCount > 0}
        <span class="pointer-events-auto"><Button variant="secondary" size="xs" class="bg-panel! shadow-md" onclick={toBottom}><span class="num">{newCount}</span> new · Jump to latest</Button></span>
      {/if}
    </div>
  {/if}
</div>
