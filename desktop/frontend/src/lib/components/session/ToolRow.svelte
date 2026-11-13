<script lang="ts">
  // One tool call from the agent's own log: a claim, including its "ok". That is why it has no
  // success tick and never the word "exit". Those belong to a plate, which lola
  // witnessed. Surface accents identify activity, not verified success. A bounded excerpt stays
  // visible only on the current row; opening it expands the same output surface.
  //
  // Hand-rolled buttons on purpose, like the other card-shaped rows: the row IS the control, and
  // a Button's padding and hover chip would break these rows' shared columns.
  import { untrack } from "svelte";
  import { tooltip } from "$lib/tooltip";
  import { disclosureSlide } from "$lib/disclosureMotion";
  import { changes } from "$lib/changes.svelte";
  import { store } from "$lib/store.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import { feed } from "$lib/feed.svelte";
  import { formatMs, mmss } from "$lib/ssh";
  import { toMillis } from "$lib/time";
  import { buildStory, clipMiddle, flatRows, liveVerb, pastVerb, toolStatusWords, type ToolRowData } from "$lib/turns";
  import { changesOffered } from "$lib/sessionview";
  import StoryRow from "./StoryRow.svelte";
  import Button from "../Button.svelte";
  import Chevron from "../Chevron.svelte";
  import SyntaxText from "../SyntaxText.svelte";
  import ToolPreview from "./ToolPreview.svelte";

  let {
    active = false,
    sessionId,
    row,
    chars = 72,
    readonly = false,
    nested = false,
  }: { active?: boolean; sessionId: string; row: ToolRowData; chars?: number; readonly?: boolean; nested?: boolean } = $props();

  const f = $derived(feed.view(sessionId));
  const tool = $derived(row.tool);
  const running = $derived(row.status === "running");
  const elapsed = $derived(running ? mmss(sshClock.now - toMillis(row.item.at as unknown as string)) : "");
  const verb = $derived(running ? liveVerb(row.verb) : row.status === "ok" ? pastVerb(row.verb) : row.verb);
  const words = $derived(toolStatusWords(tool, row.status, elapsed, tool.durationMs ? formatMs(tool.durationMs) : "", verb !== row.verb));
  const tail = $derived(words.tail || (row.status === "ok" && tool.category === "read" && tool.durationMs ? formatMs(tool.durationMs) : ""));
  const isEdit = $derived(tool.category === "edit" && row.status === "ok");
  const session = $derived(store.sessionById(sessionId));
  const toChanges = $derived(!readonly && isEdit && !!session && changesOffered(session));
  const canOpen = $derived(!readonly && (!!tool.hasOutput || !!tool.preview || !!tool.subagent));
  const open = $derived(canOpen && f.expanded.has(row.id));
  const output = $derived(f.outputs.get(row.id));
  const thread = $derived(tool.subagent ? f.threads.get(tool.subagent) : undefined);
  const threadRows = $derived(thread ? flatRows(buildStory({ feed: thread.items, items: [], alive: row.status === "running" })) : []);
  // A path says the least in its middle, and a command the least at its end, which CSS clips.
  const target = $derived(tool.category === "run" ? row.target : clipMiddle(row.target, Math.max(16, chars)));

  function press() {
    // A link, so a second press stays there, and it lands on the file the row is about.
    if (!canOpen && toChanges && session) {
      changes.want(sessionId, row.target ?? "");
      ssh.openChanges(session);
      return;
    }
    if (!canOpen) return;
    if (open) f.expanded.delete(row.id);
    else f.expanded.add(row.id);
  }

  // Only the current completed tool and deliberately opened rows read their bounded output.
  $effect(() => {
    if (!open && !(active && !running)) return;
    const id = row.id;
    const subagent = tool.subagent;
    const hasOutput = tool.hasOutput;
    untrack(() => {
      if (open && subagent) void f.thread(subagent);
      else if (!subagent && hasOutput) void f.output(id);
    });
  });
</script>

{#snippet cells()}
  <span class="cv-tool-verb"><span class="cv-tool-indicator" data-running={running ? "" : undefined} aria-hidden="true"></span>{verb}</span>
  <span class="min-w-0 truncate font-mono text-ink-2"><SyntaxText text={target} language={tool.category === "run" ? "sh" : "text"} /></span>
  <span class="cv-tool-meta num flex items-baseline gap-2 whitespace-nowrap text-faint">
    {#if words.word}<span class={words.bad ? (row.settled ? "text-ink-2" : "text-bad") : ""}>{words.word}</span>{/if}
    {#if tail}<span>{tail}</span>{/if}
  </span>
  <span class="grid place-items-center text-faint group-hover:text-ink">{#if canOpen}<Chevron {open} />{/if}</span>
{/snippet}

<div class="cv-tool-card" data-tool={tool.category} data-status={row.status} data-tool-layout={nested ? "columns" : "summary"}>
  {#if canOpen || toChanges}
    <button type="button" class="cv-tool-summary group" use:tooltip={{ text: tool.detail || row.target, language: tool.category === "run" ? "sh" : "text" }} aria-expanded={canOpen ? open : undefined} onclick={press}>
      {@render cells()}
    </button>
  {:else}
    <div class="cv-tool-summary" use:tooltip={{ text: tool.detail || row.target, language: tool.category === "run" ? "sh" : "text" }}>{@render cells()}</div>
  {/if}

  {#if open || active}
    <div class="cv-tool-content" data-tool-expansion={open ? "" : undefined}>
      {#if open && toChanges && session}<Button size="xs" onclick={() => { changes.want(sessionId, row.target); if (session) ssh.openChanges(session); }}>View changes</Button>{/if}
      {#if tool.subagent}
        {#if open}
          <div transition:disclosureSlide>
            {#if thread?.loading}
              <p class="text-sm text-faint">Reading what this agent did</p>
            {:else if thread?.failed}
              <p class="text-sm text-faint">Could not read this agent's log.</p>
            {:else}
              {#each threadRows as r (r.id)}
                <StoryRow {sessionId} row={r} {chars} {readonly} nested role="narration" />
              {/each}
            {/if}
          </div>
        {/if}
      {:else}
        <ToolPreview text={output?.text || tool.preview || ""} {running} expanded={open} command={tool.detail || row.target} sourcePath={tool.category === "read" || tool.category === "edit" ? row.target : ""} />
        {#if open}
          {#if output?.loading && !tool.preview}<p class="text-sm text-faint">Reading its output</p>{/if}
          {#if output?.failed}<p class="text-sm text-faint">Could not read this output. The terminal may still show it.</p>{/if}
          {#if output?.truncated}
            <p class="flex items-center gap-2 text-sm text-faint">
              This is the first part.
              <Button size="xs" onclick={() => ssh.showAgentPane(sessionId)}>Terminal</Button>
            </p>
          {/if}
        {/if}
      {/if}
    </div>
  {/if}
</div>
