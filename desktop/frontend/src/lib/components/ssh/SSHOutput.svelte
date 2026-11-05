<script lang="ts">
  import type { Snippet } from "svelte";
  // Raw output, the bottom of the dig. Streams stay separate on purpose: lola has no
  // record of how the two interleaved, and a false ordering would be worse than two
  // tabs. Everything renders as text nodes. A token matching http(s) is a link
  // through the daemon, and nothing else in output is ever interactive.
  //
  // It loads lazily and only on expand, 256 KiB at a time on scroll to the end. A
  // running block polls from its offset once a second and stays at the bottom until
  // the reader scrolls up.
  import { tick, untrack } from "svelte";
  import { SSHService } from "@bindings/desktop";
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import {
    SSH_MAX_POLL_BYTES,
    SSH_TAIL_BYTES,
    defaultStream,
    findOffsets,
    formatBytes,
    isTerminal,
    linkify,
    outputNotice,
    type SSHBlock,
    type Stream,
  } from "$lib/ssh";
  import Button from "../Button.svelte";

  let {
    sessionId,
    block,
    full = false,
    head,
    tail,
    info,
    onHead,
  }: { sessionId: string; block: SSHBlock; full?: boolean; head?: Snippet; tail?: Snippet; info?: Snippet; onHead?: () => void } = $props();
  let showInfo = $state(false);
  // The keyboard path is the chevron button, so this stays a plain click target. A drag
  // over the command to copy it ends in a click too, and must not fold the block.
  function headClick() {
    if (!window.getSelection()?.toString()) onHead?.();
  }

  type StreamState = {
    text: string;
    offset: number;
    total: number;
    eof: boolean;
    tail: string;
    dropped: number;
    pruned: boolean;
    failed: boolean;
    loading: boolean;
    started: boolean;
  };
  const blank = (): StreamState => ({ text: "", offset: 0, total: 0, eof: false, tail: "", dropped: 0, pruned: false, failed: false, loading: false, started: false });

  const view = $derived(ssh.view(sessionId));
  let streams = $state<Record<Stream, StreamState>>({ stdout: blank(), stderr: blank() });
  const stream = $derived<Stream>(view.streams.get(block.id) ?? defaultStream(block));
  const cur = $derived(streams[stream]);

  let wrap = $state(true);
  let finding = $state(false);
  let query = $state("");
  let matchAt = $state(0);
  let following = $state(true);
  let copied = $state("");
  let scroller = $state<HTMLDivElement | null>(null);
  let findEl = $state<HTMLInputElement | null>(null);

  const matches = $derived(findOffsets(cur.text, query));
  const live = $derived(!isTerminal(block.status));

  async function load(which: Stream) {
    const st = streams[which];
    if (st.loading) return;
    st.loading = true;
    st.failed = false;
    try {
      const r = await SSHService.Output({ session: sessionId, block: block.id, stream: which, offset: st.offset, limit: SSH_MAX_POLL_BYTES });
      st.text += r.data ?? "";
      st.offset = r.nextOffset ?? st.offset;
      st.total = r.totalBytes ?? 0;
      st.eof = !!r.eof;
      st.tail = r.tail ?? "";
      st.dropped = r.droppedBytes ?? 0;
      st.pruned = !!r.pruned;
      st.started = true;
      if (following && live && which === stream) {
        await tick();
        if (scroller) scroller.scrollTop = scroller.scrollHeight;
      }
    } catch {
      st.failed = true;
    } finally {
      st.loading = false;
    }
  }

  // First fetch for whichever stream is showing, and again when the tab changes.
  $effect(() => {
    const s = stream;
    untrack(() => {
      if (!streams[s].started) void load(s);
    });
  });

  // A running block keeps growing. Once it ends, one more read picks up what landed.
  $effect(() => {
    if (!live) {
      untrack(() => {
        if (streams[stream].started && !streams[stream].eof) void load(stream);
      });
      return;
    }
    const t = setInterval(() => void load(stream), 1000);
    return () => clearInterval(t);
  });

  function onScroll() {
    const el = scroller;
    if (!el) return;
    const atEnd = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
    if (live) following = atEnd;
    else if (atEnd && !cur.eof && !cur.loading) void load(stream);
  }

  function resume() {
    following = true;
    if (scroller) scroller.scrollTop = scroller.scrollHeight;
  }

  function setStream(s: Stream) {
    view.streams.set(block.id, s);
    matchAt = 0;
  }

  async function copy() {
    const all = cur.text + (cur.tail ? `\n${cur.tail}` : "");
    await navigator.clipboard?.writeText(all);
    copied = cur.eof ? "Copied." : `Copied the loaded ${formatBytes(cur.text.length)}. Load the rest first to copy it all.`;
    setTimeout(() => (copied = ""), 4000);
  }

  function openFind() {
    finding = true;
    void tick().then(() => findEl?.focus());
  }

  function stepMatch(d: number) {
    if (!matches.length) return;
    matchAt = (matchAt + d + matches.length) % matches.length;
    void tick().then(() => scroller?.querySelector("mark[data-current]")?.scrollIntoView?.({ block: "center" }));
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === "/" && (e.target as HTMLElement).tagName !== "INPUT") {
      e.preventDefault();
      e.stopPropagation();
      openFind();
    } else if (e.key === "Escape" && showInfo) {
      e.preventDefault();
      e.stopPropagation();
      showInfo = false;
    } else if (e.key === "Escape" && full) {
      e.preventDefault();
      e.stopPropagation();
      view.outputMax = "";
    }
  }

  const notice = $derived(cur.failed ? "" : outputNotice(block, cur.text.length, cur.total));
  const showDrop = $derived(cur.dropped > 0 && cur.eof);

  // Split around the find's matches. Text nodes and <mark> only.
  type Part = { text: string; url?: boolean; mark?: boolean; current?: boolean };
  const parts = $derived.by<Part[]>(() => {
    if (!query) return linkify(cur.text);
    const out: Part[] = [];
    let at = 0;
    matches.forEach((m, i) => {
      if (m > at) out.push({ text: cur.text.slice(at, m) });
      out.push({ text: cur.text.slice(m, m + query.length), mark: true, current: i === matchAt });
      at = m + query.length;
    });
    if (at < cur.text.length) out.push({ text: cur.text.slice(at) });
    return out;
  });
</script>

{#snippet text(ps: Part[])}{#each ps as p, i (i)}{#if p.url}<a
        class="text-accent-ink underline underline-offset-2"
        href={p.text}
        onclick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          void store.openURL(p.text);
        }}>{p.text}</a
      >{:else if p.mark}<mark
        data-current={p.current ? "" : undefined}
        class="rounded-[2px] {p.current ? 'bg-accent text-on-accent' : 'bg-accent-fill text-accent-ink'}">{p.text}</mark
      >{:else}{p.text}{/if}{/each}{/snippet}

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="grid min-h-0 gap-1.5 {full ? 'h-full grid-rows-[auto_auto_auto_minmax(0,1fr)]' : ''}" onkeydown={onKey} data-output>
  <!-- What you ran on the left, what you can do on the right. Streams appear only when
       there is a real choice between them: most commands have one. -->
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="flex items-start gap-2 {onHead ? 'cursor-pointer' : ''}" onclick={headClick} data-output-head>
    <div class="min-w-0 flex-1">{#if head}{@render head()}{/if}</div>
    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
    <span class="flex shrink-0 cursor-default items-center gap-0.5 font-sans" onclick={(e) => e.stopPropagation()} data-output-actions>
      {#if full}<Button size="xs" selected={wrap} onclick={() => (wrap = !wrap)}>Wrap</Button>{/if}
      <Button size="xs" icon selected={finding} aria-label="Find in output" title="Find in output" onclick={() => (finding ? (finding = false, query = "") : openFind())}>
        <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="11" cy="11" r="6.5" /><path d="m20 20-4.2-4.2" /></svg>
      </Button>
      <Button
        size="xs"
        icon
        aria-label={full ? "Leave full view" : "Fill the stream column"}
        title={full ? "Leave full view (esc)" : "Fill the stream column"}
        onclick={() => (view.outputMax = full ? "" : block.id)}
      >
        <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          {#if full}<path d="M9 4v5H4M15 4v5h5M9 20v-5H4M15 20v-5h5" />{:else}<path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5" />{/if}
        </svg>
      </Button>
      {#if tail}{@render tail()}{/if}
    </span>
  </div>

  {#if block.stdoutBytes && block.stderrBytes}
    <span class="flex items-center gap-0.5 font-sans" role="tablist" aria-label="Output stream">
      {#each ["stdout", "stderr"] as const as s (s)}
        <Button size="xs" role="tab" aria-selected={stream === s} aria-pressed={undefined} selected={stream === s} onclick={() => setStream(s)}>{s === "stdout" ? "Output" : "Errors"}</Button>
      {/each}
    </span>
  {/if}

  {#if finding || copied}
    <div class="flex flex-wrap items-center gap-2 text-sm">
      {#if finding}
        <input
          bind:this={findEl}
          class="w-48 rounded border border-edge bg-canvas px-2 py-0.5 text-ink outline-none placeholder:text-placeholder focus:border-accent"
          placeholder="Find in this output"
          aria-label="Find in this output"
          bind:value={query}
          oninput={() => (matchAt = 0)}
          onkeydown={(e) => {
            e.stopPropagation();
            if (e.key === "Enter" || e.key === "ArrowDown") {
              e.preventDefault();
              stepMatch(1);
            } else if (e.key === "ArrowUp") {
              e.preventDefault();
              stepMatch(-1);
            } else if (e.key === "Escape") {
              e.preventDefault();
              finding = false;
              query = "";
            }
          }}
        />
        {#if query}<span class="num text-faint">{matches.length ? `${matchAt + 1} of ${matches.length}` : "0 of 0"}</span>{/if}
        {#if query && !cur.eof}
          <span class="text-faint">Searched the first {formatBytes(cur.text.length)}.</span>
          <Button size="xs" loading={cur.loading} onclick={() => load(stream)}>Load the rest</Button>
        {/if}
      {/if}
      {#if copied}<span class="text-faint">{copied}</span>{/if}
    </div>
  {/if}

  <!-- A frame around the scroller, so the corner tools stay put while output scrolls under
       them. They overlay it on purpose: a command's facts are rarely what you came for. -->
  <div class="group/out relative font-sans {full ? 'grid min-h-0 grid-rows-[minmax(0,1fr)]' : ''}">
  <div
    bind:this={scroller}
    onscroll={onScroll}
    class="selectable relative overflow-auto rounded border border-edge/60 bg-canvas font-mono {full ? 'min-h-0' : 'max-h-80'}"
    tabindex="-1"
  >
    {#if block.binary}
      <div class="border-b border-edge/60 px-2.5 py-1.5 text-sm text-warn">This output contained binary data. Control characters were removed.</div>
    {/if}
    {#if cur.failed}
      <div class="flex items-center gap-2 px-2.5 py-2 text-sm text-faint">
        Could not load this output.
        <Button size="xs" onclick={() => load(stream)}>Try again</Button>
      </div>
    {:else if notice}
      <div class="px-2.5 py-2 text-sm text-faint">{notice}</div>
    {:else if !cur.started}
      <div class="px-2.5 py-2 text-sm text-faint">Loading…</div>
    {:else}
      <pre class="px-2.5 py-2 font-mono text-sm text-ink {wrap ? 'whitespace-pre-wrap [overflow-wrap:anywhere]' : 'w-max whitespace-pre'}">{@render text(parts)}</pre>
      {#if !cur.eof && !live}
        <div class="px-2.5 pb-2"><Button size="xs" loading={cur.loading} onclick={() => load(stream)}>Load more</Button></div>
      {/if}
      {#if showDrop}
        <div class="flex items-center gap-3 px-2.5 py-1 text-sm text-warn" title="Lola kept the first {formatBytes(cur.offset)} and the last {formatBytes(SSH_TAIL_BYTES)}. The middle is gone.">
          <span class="h-px flex-1 bg-warn/40" aria-hidden="true"></span>
          <span class="num">{formatBytes(cur.dropped)} not kept</span>
          <span class="h-px flex-1 bg-warn/40" aria-hidden="true"></span>
        </div>
        <p class="px-2.5 text-center text-sm text-faint">Lola kept the first {formatBytes(cur.offset)} and the last {formatBytes(SSH_TAIL_BYTES)}. The middle is gone.</p>
        {#if cur.tail}<pre class="px-2.5 py-2 font-mono text-sm text-ink {wrap ? 'whitespace-pre-wrap [overflow-wrap:anywhere]' : 'w-max whitespace-pre'}">{@render text(linkify(cur.tail))}</pre>{/if}
      {/if}
    {/if}
    {#if live && !following}
      <div class="sticky bottom-1.5 flex justify-center">
        <Button variant="secondary" size="xs" class="bg-panel!" onclick={resume}>Following paused · Resume</Button>
      </div>
    {/if}
  </div>
  <div class="absolute right-1.5 bottom-1.5 z-10 flex items-center gap-0.5 rounded bg-canvas/90" data-output-corner>
    {#if info}
      <Button size="xs" icon selected={showInfo} aria-expanded={showInfo} aria-label="About this command" title="About this command" onclick={() => (showInfo = !showInfo)}>
        <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="8.5" /><path d="M12 11v5" /><path d="M12 8v.01" /></svg>
      </Button>
    {/if}
    <Button size="xs" icon aria-label="Copy output" title="Copy output" onclick={copy}>
      <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V6a2 2 0 0 1 2-2h9" /></svg>
    </Button>
  </div>
  {#if info && showInfo}
    <div class="absolute right-1.5 bottom-9 z-20 w-[min(24rem,calc(100%-0.75rem))] rounded-lg border border-edge bg-panel p-3 shadow-xl" data-output-info>
      {@render info()}
    </div>
  {/if}
  </div>
</div>
