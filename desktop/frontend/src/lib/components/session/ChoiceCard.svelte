<script lang="ts">
  // The shape a permission and a question share: a read of the agent's pane, drawn verbatim, and
  // a press of the pane's own keys. Everything on it came from a process, which can print
  // anything, so it carries no lock, no logo and nothing in lola's voice that could vouch for it.
  //
  // A press sends the hash of what was drawn. "Yes" reads the same on every prompt, so the daemon
  // re-reads the pane right before pressing and refuses a card that no longer matches, and the
  // card redraws from that answer.
  //
  // The keys are dead for 400ms after the card appears or its text changes, a held key is
  // ignored, and Enter does nothing, so a reflex cannot answer. They only work with focus on the
  // card, which only a person puts there. A long input has to be scrolled to its end first.
  import type { AgentPrompt } from "@bindings/internal/protocol";
  import type { Snippet } from "svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { talk } from "$lib/talk.svelte";
  import { isFrameLine, isPlainChoice, isPrimaryChoice } from "$lib/composer";
  import Button from "../Button.svelte";
  import Chevron from "../Chevron.svelte";

  // Not ssh's 800ms, on purpose: this is a local worktree agent, and fatigue is the larger risk.
  const ARM_MS = 400;

  let {
    sessionId,
    prompt,
    title,
    reading = false,
    narrow = false,
    toTerminal = () => false,
    pager,
    note = () => "",
  }: {
    sessionId: string;
    prompt: AgentPrompt;
    title: string;
    /** The lines are a question to read rather than a command to check. */
    reading?: boolean;
    narrow?: boolean;
    /** A row that needs typing, so pressing it opens the terminal and sends nothing. */
    toTerminal?: (label: string) => boolean;
    pager?: Snippet;
    /** A word of lola's own beside a row. */
    note?: (label: string) => string;
  } = $props();

  const view = $derived(ssh.view(sessionId));
  const choices = $derived(prompt.choices ?? []);
  // Only permission prompts fold qualified answers on narrow panes. Questions keep every answer
  // equally visible, even when some happen to be labelled Yes or No.
  const folds = $derived(!reading && narrow && choices.some((c) => isPlainChoice(c.label)));
  const plain = $derived(folds ? choices.filter((c) => isPlainChoice(c.label)) : choices);
  const more = $derived(folds ? choices.filter((c) => !isPlainChoice(c.label)) : []);
  let moreOpen = $state(false);

  // The daemon hashes the decision, excluding only the permission expiry clock.
  // Its text updates in place without resetting the read/press gates on every tick.
  const drawn = $derived(`${sessionId}\n${prompt.hash}`);
  let armed = $state(false);
  let pressed = $state(false);
  // Which row was pressed, so its keycap can say lola pressed it. That is all the word claims:
  // whether the agent obeyed is the pane's to say, and the card clears from a re-read.
  let pressedKey = $state("");
  let refusal = $state("");
  $effect(() => {
    drawn;
    armed = false;
    pressed = false;
    pressedKey = "";
    const t = setTimeout(() => (armed = true), ARM_MS);
    return () => clearTimeout(t);
  });

  let body = $state<HTMLDivElement | null>(null);
  let bodyEnd = $state<HTMLSpanElement | null>(null);
  let readEnd = $state(false);
  function checkRead() {
    if (!body || !bodyEnd) return;
    if (bodyEnd.getBoundingClientRect().top <= body.getBoundingClientRect().bottom + 1) readEnd = true;
  }
  $effect(() => {
    drawn;
    readEnd = false;
    const el = body;
    if (!el) return;
    checkRead();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(checkRead);
    ro.observe(el);
    return () => ro.disconnect();
  });
  const live = $derived(armed && readEnd && !pressed);
  // The command outranks whatever the agent says about it underneath.
  const firstFact = $derived((prompt.lines ?? []).findIndex((l) => l.trim() && !isFrameLine(l)));

  function openTerminal() {
    ssh.showAgentTerminal(sessionId);
  }

  async function press(key: string, label: string) {
    if (!live) return;
    if (toTerminal(label)) {
      openTerminal();
      return;
    }
    pressed = true;
    pressedKey = key;
    refusal = "";
    try {
      const r = await talk.choose(sessionId, prompt.hash, key);
      if (!r.sent) {
        refusal = r.why ?? "The prompt changed. Read it again.";
        pressed = false;
        pressedKey = "";
      }
    } catch (err) {
      refusal = String(err);
      pressed = false;
      pressedKey = "";
    }
  }

  function onKey(e: KeyboardEvent) {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      view.composerFocus++;
      return;
    }
    if (e.key === "Enter") {
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "PageDown" || e.key === "PageUp") {
      const step = e.key.startsWith("Page") ? (body?.clientHeight ?? 0) * 0.8 : 48;
      body?.scrollBy?.({ top: e.key === "ArrowUp" || e.key === "PageUp" ? -step : step });
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    if (e.key.length !== 1) return;
    e.stopPropagation();
    if (e.repeat) {
      e.preventDefault();
      return;
    }
    const c = choices.find((x) => x.key === e.key);
    if (c) {
      e.preventDefault();
      void press(c.key, c.label);
    }
  }
</script>

{#snippet row(c: { key: string; label: string; detail?: string })}
  {@const primary = !reading && isPrimaryChoice(c.label)}
  {@const side = note(c.label)}
  {@const sent = pressedKey === c.key}
  {@const sends = toTerminal(c.label)}
  <Button
    variant={primary ? "primary" : "ghost"}
    size="sm"
    block={!primary}
    disabled={!live}
    class="h-auto! justify-start! text-left whitespace-normal! {reading ? 'question-choice grid! min-h-11 grid-cols-[24px_minmax(0,1fr)] items-start! gap-x-3! rounded-none! px-3! py-3! disabled:opacity-100! focus-visible:-outline-offset-2' : 'min-h-7 px-2! py-1'} {primary ? 'w-fit! min-w-[88px]' : 'text-ink!'} {sent ? 'opacity-100!' : ''}"
    onclick={() => press(c.key, c.label)}
    data-choice={c.key}
    data-primary={primary ? "" : undefined}
  >
    <kbd class="num inline-flex shrink-0 items-center justify-center rounded border font-mono text-sm leading-none transition-transform duration-100 motion-reduce:transition-none {reading ? 'size-6 bg-canvas text-ink-2' : 'size-4.5'} {primary ? 'border-accent-ink/45' : 'border-edge text-ink-2'} {sent ? 'translate-y-px bg-sel' : ''}" aria-hidden="true">{c.key}</kbd>
    {#if reading}
      <span class="grid min-w-0 gap-1 self-center [overflow-wrap:anywhere]">
        <span class="font-medium">{sent ? "Sent" : c.label}</span>
        {#if c.detail && !sent}<span class="max-w-[75ch] text-sm leading-relaxed font-normal text-ink-2" data-choice-detail>{c.detail}</span>{/if}
        {#if sends}<span class="text-sm font-normal text-ink-2">Opens in terminal <span aria-hidden="true">⌘\</span></span>{/if}
      </span>
    {:else}
      <span class="min-w-0 [overflow-wrap:anywhere]">{sent ? "Sent" : c.label}</span>
    {/if}
    {#if !reading && c.detail && !sent}<span class="ml-2 min-w-0 text-sm font-normal text-faint [overflow-wrap:anywhere]" data-choice-detail>{c.detail}</span>{/if}
    {#if side && !narrow && !sent}<span class="ml-2 shrink-0 text-sm font-normal text-faint">{side}</span>{/if}
    {#if !reading && sends}<span class="ml-2 shrink-0 text-sm font-normal text-faint">Terminal <span aria-hidden="true">⌘\</span></span>{/if}
  </Button>
{/snippet}

<!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_no_noninteractive_tabindex -->
<section
  data-card={prompt.kind}
  data-prompt-hash={prompt.hash}
  tabindex={reading ? 0 : -1}
  aria-label={title}
  class="ssh-fade-in grid min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden rounded-[10px] border {reading ? 'max-h-[inherit] border-edge' : 'border-orange'} bg-panel outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-orange"
  onkeydown={onKey}
>
  <div class="flex min-w-0 items-center gap-2 px-3 {reading ? 'border-b border-edge py-2.5' : 'pt-2'}">
    <h3 class="min-w-0 font-medium text-orange {reading ? '' : 'truncate'}">{title}</h3>
    <span class="ml-auto flex shrink-0 items-center gap-1">
      {#if pager}{@render pager()}{/if}
      <Button size="xs" class={reading ? 'text-ink-2!' : 'h-5!'} onclick={openTerminal}>Terminal <span class="text-ink-2">⌘\</span></Button>
    </span>
  </div>

  <div bind:this={body} class="min-h-0 overflow-y-auto overscroll-contain {reading ? 'px-1 py-1' : 'px-3 pt-2 pb-1.5'}" onscroll={checkRead} data-card-body>
    <div class={reading ? 'px-3 py-3' : ''}>
      {#each prompt.lines ?? [] as line, i (i)}
        <p class="selectable [overflow-wrap:anywhere] whitespace-pre-wrap {reading ? 'text-prose text-ink' : `font-mono text-sm ${isFrameLine(line) ? 'text-faint' : i === firstFact ? 'text-ink' : 'text-ink-2'}`}" data-prompt-line>{line || " "}</p>
      {/each}
    </div>
    <!-- A block sentinel adds no phantom line below a short question. -->
    <span bind:this={bodyEnd} class="block h-0" aria-hidden="true"></span>
    {#if reading}
      <div class="question-choices grid" data-card-actions>
        {#each choices as c (c.key)}{@render row(c)}{/each}
      </div>
    {/if}
  </div>

  {#if reading}
    <div class="border-t border-edge px-4 py-2 text-sm text-ink-2" role="status">
      {#if refusal}<p class="text-orange" data-card-refusal>{refusal}</p>
      {:else if !readEnd}<p data-read-hint>Scroll through the question to enable answers.</p>
      {:else if pressed}<p>Answer sent. Waiting for a response…</p>
      {:else}<p>Choose an option or press its number.</p>{/if}
    </div>
  {:else}
  <!-- Pinned. The decision is never below the fold. -->
  <div class="grid gap-0 px-1 pb-1.5 {readEnd ? '' : 'border-t border-edge/60 pt-1'}" data-card-actions>
    {#if !readEnd}<p class="px-2 text-sm text-faint" data-read-hint>Scroll to the end to answer.</p>{/if}
    {#if refusal}<p class="px-2 text-sm text-faint" role="status" data-card-refusal>{refusal}</p>{/if}
    {#if folds}
      <div class="flex items-center gap-1 [&_[data-choice]]:w-auto!">
        {#each plain as c (c.key)}{@render row(c)}{/each}
        {#if more.length}
          <Button size="sm" class="ml-auto" aria-expanded={moreOpen} onclick={() => (moreOpen = !moreOpen)} data-choice-more>More <Chevron open={!moreOpen} class={moreOpen ? "-rotate-90" : ""} /></Button>
        {/if}
      </div>
      {#if moreOpen}
        {#each more as c (c.key)}{@render row(c)}{/each}
      {/if}
    {:else}
      {#each choices as c (c.key)}{@render row(c)}{/each}
    {/if}
  </div>
  {/if}
</section>

<style>
  .question-choices :global(.question-choice + .question-choice) {
    border-top: 1px solid var(--color-edge);
  }
</style>
