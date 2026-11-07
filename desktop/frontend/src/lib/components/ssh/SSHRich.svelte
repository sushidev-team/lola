<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  // The agent's prose: headlines, lists, tables, callouts and folds. It renders from parsed
  // data and has no {@html}, because this text is written by something that reads untrusted
  // remote output. Links and block references come from SSHText, as everywhere else.
  import { inlineSpans, itemStatus, richBlocks, type InlineSpan, type RichBlock } from "$lib/ssh";
  import { answerBlocks, answerStatus, cellTone, fenceFold, prHref } from "$lib/answerblocks";
  import { store } from "$lib/store.svelte";
  import { disclosureMotion } from "$lib/disclosureMotion";
  import Chevron from "../Chevron.svelte";
  import SyntaxText from "../SyntaxText.svelte";
  import Disclosure from "../Disclosure.svelte";
  import SSHText from "./SSHText.svelte";

  let { sessionId, text, quiet = false, conversation = false, compact = false, reveal = false }: {
    sessionId: string;
    text: string | undefined;
    /** Narration: its first line takes the surrounding ink rather than leading with the top one. */
    quiet?: boolean;
    /** The agent's answer in the Conversation: parsed into its elements, at their measures. */
    conversation?: boolean;
    /** Completed responses keep supporting sections behind named disclosures. */
    compact?: boolean;
    reveal?: boolean;
  } = $props();

  const blocks = $derived(conversation ? answerBlocks(text ?? "") : richBlocks(text ?? ""));
  const sections = $derived.by(() => {
    const result: { heading: string; blocks: RichBlock[]; open: boolean }[] = [];
    for (const block of blocks) {
      if (block.kind === "heading") {
        result.push({ heading: block.text, blocks: [], open: /risk|warn|block|fail|next|question|confirm|attention|important|risik|fehler|achtung|noch offen|noch zu tun|prüfen|nächste|frage|bestätig/i.test(block.text) });
      } else {
        if (!result.length) result.push({ heading: "", blocks: [], open: true });
        result[result.length - 1].blocks.push(block);
      }
    }
    // Bound the visible opening even when the agent used plain-text headings.
    const opening = result[0];
    if (opening && !opening.heading && opening.blocks.length > 2) {
      const second = opening.blocks[1];
      const secondIsLabel = second?.kind === "p" && second.text.length < 120 && /:\s*$/.test(second.text);
      const rest = opening.blocks.splice(secondIsLabel ? 1 : 2);
      const first = rest[0];
      const named = first?.kind === "p" && first.text.length < 120 && /:\s*$/.test(first.text);
      result.splice(1, 0, { heading: named && first.kind === "p" ? first.text : "Full response", blocks: named ? rest.slice(1) : rest, open: false });
    }
    return result;
  });
  const prUrl = $derived(conversation ? store.sessionById(sessionId)?.prUrl : undefined);

  const toneRail = { ok: "border-good", note: "border-info", warn: "border-warn", bad: "border-bad" } as const;
  const toneMark = { ok: "text-good", warn: "text-warn", bad: "text-bad" } as const;
  /** A cell that is only a number lines its digits up. */
  const NUMBER = /^[-+−]?[\d.,]+\s?%?$/;
  const toneWord = { ok: "OK", note: "Note", warn: "Warning", bad: "Problem" } as const;
  // Here an agent's "ok" is a claim, so it never wears green. Only a plate may.
  const claimMark = { ok: "text-ink-2", warn: "text-warn", bad: "text-bad" } as const;
  const claimRail = { ok: "border-edge", note: "border-info", warn: "border-warn", bad: "border-bad" } as const;
  const claimWord = { ok: "text-ink", note: "text-info", warn: "text-warn", bad: "text-bad" } as const;
  const labelCls = { "": "text-ink", warn: "text-warn", bad: "text-bad" } as const;
</script>

{#snippet spans(list: InlineSpan[])}
  {#each list as s, i (i)}{#if s.kind === "code"}<code class="selectable font-mono [overflow-wrap:anywhere] {conversation ? 'text-[0.875em]' : 'rounded-[3px] bg-sel/50 px-1 py-px text-[0.88em]'}"><SyntaxText text={s.text} /></code>{:else if s.kind === "bold"}<strong class="font-semibold {conversation ? 'text-ink' : ''}">{#if s.spans}{@render spans(s.spans)}{:else}<SSHText {sessionId} text={s.text} />{/if}</strong>{:else if s.kind === "em"}<em class="italic">{#if s.spans}{@render spans(s.spans)}{:else}<SSHText {sessionId} text={s.text} />{/if}</em>{:else if s.kind === "link"}{@render link(s.text, s.href, "text-accent-ink underline underline-offset-2")}{:else if s.kind === "pr" && conversation}{@const href = prHref(prUrl, s.text)}{#if href}{@render link(s.text, href, "num font-medium text-magenta hover:underline")}{:else}<span class="num font-medium text-magenta" data-pr>{s.text}</span>{/if}{:else}<SSHText {sessionId} text={s.text} />{/if}{/each}
{/snippet}

<!-- The address came out of agent text, so it opens through the daemon's http(s) guard. -->
{#snippet link(label: string, href: string, cls: string)}<a class="selectable {cls}" {href} onclick={(e) => { e.preventDefault(); e.stopPropagation(); void store.openURL(href); }}>{label}</a>{/snippet}

{#snippet inline(line: string)}{@render spans(inlineSpans(line))}{/snippet}

{#snippet claim(tone: "ok" | "warn" | "bad")}
  <Icon name={tone === 'ok' ? 'check' : tone === 'bad' ? 'x' : 'circle-alert'} class="size-3.5 translate-y-0.5 {claimMark[tone]}" role="img" aria-label={toneWord[tone]} />
{/snippet}

{#snippet sublist(items: string[])}
  <ul class="mt-1.5 grid gap-1.5" data-rich-sub>
    {#each items as item, n (n)}
      <li class="grid grid-cols-[1.25rem_minmax(0,1fr)] items-baseline"><span class="text-faint" aria-hidden="true">–</span><span>{@render inline(item)}</span></li>
    {/each}
  </ul>
{/snippet}

<!-- Block flow rather than a grid, because a grid item sticks only inside its own row and the lead
     must stay up for as long as its answer is on screen. -->
{#snippet answer(list: RichBlock[])}
  {#each list as b, i (i)}
    {#if b.kind === "lead"}
      <p class="cv-lead -mx-2 max-w-[680px] bg-canvas px-2 pb-0 text-[13px] leading-[21px] font-semibold text-ink" data-rich-lead>{@render inline(b.text)}</p>
    {:else if b.kind === "p"}
      <p class="max-w-[560px]">{@render inline(b.text)}</p>
    {:else if b.kind === "heading"}
      <p class="mt-2 max-w-[560px] text-prose font-semibold text-ink" data-rich-heading>{@render inline(b.text)}</p>
    {:else if b.kind === "question"}
      <p class="max-w-[560px] border-l-2 border-ink-2 pl-3.5 text-ink" data-rich-question>{@render inline(b.text)}</p>
    {:else if b.kind === "callout"}
      {@const word = b.word ?? (b.tone === "note" ? "" : toneWord[b.tone])}
      <div class="max-w-[560px] border-l-2 pl-3.5 {claimRail[b.tone]}" data-rich-callout={b.tone}>{#if word}<span class="font-semibold {claimWord[b.tone]}">{word}:</span>{" "}{/if}{@render inline(b.text)}</div>
    {:else if b.kind === "ledger"}
      <dl class="cv-ledger grid max-w-[720px] grid-cols-[22%_minmax(0,1fr)]" data-rich-ledger>
        {#each b.rows as row, r (r)}
          {#if row.label}
            <dt class="border-t border-edge/50 py-[11px] pr-4 text-prose font-semibold {labelCls[row.tone]} {row.stacked ? 'col-span-2 pb-0' : ''}" data-ledger-label={row.tone || undefined}>{#if row.status}<span class="mr-1.5 inline-block">{@render claim(row.status)}</span>{/if}{@render inline(row.label)}</dt>
          {:else}
            <dt class="border-t border-edge/50 py-[11px] pr-4">{#if row.status}{@render claim(row.status)}{/if}</dt>
          {/if}
          <dd class="py-[11px] {row.stacked ? 'col-span-2 pt-1' : 'border-t border-edge/50'}" data-ledger-text>{@render inline(row.text)}{#if row.subs}{@render sublist(row.subs)}{/if}</dd>
        {/each}
      </dl>
    {:else if b.kind === "table"}
      <div class="max-w-[720px] overflow-x-auto" data-rich-table>
        <table class="w-full border-collapse">
          <thead>
            <tr>{#each b.head as cell, c (c)}<th class="border-b border-edge py-1.5 pr-6 text-left text-sm font-normal text-faint">{@render inline(cell)}</th>{/each}</tr>
          </thead>
          <tbody>
            {#each b.rows as row, r (r)}
              <tr>{#each row as cell, c (c)}{@const tone = cellTone(cell)}<td class="border-b border-edge/40 py-2 pr-6 align-top {tone === 'bad' ? 'text-bad' : tone === 'warn' ? 'text-warn' : c === 0 ? 'font-medium text-ink' : 'text-ink-2'} {NUMBER.test(cell.trim()) ? 'num text-right' : ''}">{@render inline(cell)}</td>{/each}</tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else if b.kind === "code"}
      {@const fold = fenceFold(b.text)}
      {#if fold.folds}
        <Disclosure label={fold.label} class="-ml-1.5">
          <pre class="selectable mt-1 ml-1.5 overflow-x-auto border-l border-edge pl-3 font-mono text-sm" data-rich-code><SyntaxText text={b.text} language={b.lang} /></pre>
        </Disclosure>
      {:else}
        <pre class="selectable overflow-x-auto border-l border-edge pl-3 font-mono text-sm" data-rich-code><SyntaxText text={b.text} language={b.lang} /></pre>
      {/if}
    {:else if b.kind === "fold"}
      <Disclosure label={b.title} class="-ml-1.5">
        <div class="mt-1 ml-1.5 space-y-3 border-l border-edge/60 pl-2.5">{@render answer(b.blocks)}</div>
      </Disclosure>
    {:else}
      <svelte:element this={b.kind} class="grid max-w-[560px] gap-2" data-rich-list>
        {#each b.items as item, n (n)}
          {@const st = answerStatus(item)}
          <li class="grid grid-cols-[1.25rem_minmax(0,1fr)] items-baseline">
            {#if st.tone}{@render claim(st.tone)}{:else}<span class="num text-faint" aria-hidden="true">{b.kind === "ol" ? `${n + 1}.` : "–"}</span>{/if}
            <span>{@render inline(st.text)}{#if b.subs?.[n]}{@render sublist(b.subs[n])}{/if}</span>
          </li>
        {/each}
      </svelte:element>
    {/if}
  {/each}
{/snippet}

{#snippet mark(tone: "ok" | "warn" | "bad")}
  <Icon name={tone === 'ok' ? 'check' : tone === 'bad' ? 'x' : 'circle-alert'} class="size-3.5 translate-y-0.5 {toneMark[tone]}" role="img" aria-label={toneWord[tone]} />
{/snippet}

{#snippet render(list: RichBlock[])}
  {#each list as b, i (i)}
    {#if b.kind === "lead"}
      <p class="font-medium {quiet ? '' : 'text-ink'}" data-rich-lead>{@render inline(b.text)}</p>
    {:else if b.kind === "p"}
      <p>{@render inline(b.text)}</p>
    {:else if b.kind === "heading"}
      <p class="mt-2 -mb-1.5 text-lg text-ink" data-rich-heading>{@render inline(b.text)}</p>
    {:else if b.kind === "code"}
      <pre class="selectable overflow-x-auto rounded-md border border-edge/60 bg-canvas px-2.5 py-1.5 font-mono text-sm"><SyntaxText text={b.text} language={b.lang} /></pre>
    {:else if b.kind === "callout"}
      <div class="rounded-r border-l-2 bg-canvas/50 px-2.5 py-1 {toneRail[b.tone]}" data-rich-callout={b.tone}>
        <span class="sr-only">{toneWord[b.tone]}: </span>{@render inline(b.text)}
      </div>
    {:else if b.kind === "table"}
      <div class="overflow-x-auto" data-rich-table>
        <table class="w-full border-collapse text-base">
          <thead>
            <tr>{#each b.head as cell, c (c)}<th class="border-b border-edge/70 py-1 pr-3 text-left text-sm font-normal text-faint">{@render inline(cell)}</th>{/each}</tr>
          </thead>
          <tbody>
            {#each b.rows as row, r (r)}
              <tr>{#each row as cell, c (c)}<td class="border-b border-edge/30 py-1 pr-3 align-top {c > 0 ? 'text-ink-2' : b.head.length === 2 ? 'text-faint' : 'text-ink'} {NUMBER.test(cell.trim()) ? 'num' : ''}">{@render inline(cell)}</td>{/each}</tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else if b.kind === "fold"}
      <Disclosure label={b.title} class="-ml-1.5">
        <div class="mt-1 ml-1.5 grid gap-2 border-l border-edge/60 pl-2.5">{@render render(b.blocks)}</div>
      </Disclosure>
    {:else if b.kind === "ul" || b.kind === "ol"}
      <svelte:element this={b.kind} class="grid gap-1">
        {#each b.items as item, n (n)}
          {@const st = itemStatus(item)}
          <li class="grid grid-cols-[1.25rem_minmax(0,1fr)] items-baseline">
            {#if st.tone}{@render mark(st.tone)}{:else}<span class="num text-faint" aria-hidden="true">{b.kind === "ol" ? `${n + 1}.` : "–"}</span>{/if}
            <span>{@render inline(st.text)}{#if b.subs?.[n]}{@render sublist(b.subs[n])}{/if}</span>
          </li>
        {/each}
      </svelte:element>
    {/if}
  {/each}
{/snippet}

{#if conversation}
  <div class="space-y-3" class:cv-response={compact && sections.some(s => s.heading)} data-rich>
    {#if compact}
      {#each sections as section, i (i)}
        {#if section.heading}
          <details class="cv-answer-section" use:disclosureMotion={true} open={reveal ? i === sections.findIndex(s => s.heading) : section.open && i === sections.findIndex(s => s.heading && s.open)}>
            <summary><Chevron class="cv-disclosure-chevron" /><span class="min-w-0 flex-1">{@render inline(section.heading)}</span><span class="cv-disclosure-action" aria-hidden="true"><span class="cv-when-closed">Show details</span><span class="cv-when-open">Hide details</span></span></summary>
            <div class="space-y-3">{@render answer(section.blocks)}</div>
          </details>
        {:else}
          {@render answer(section.blocks)}
        {/if}
      {/each}
    {:else}
      {@render answer(blocks)}
    {/if}
  </div>
{:else}
  <div class="copy grid max-w-[76ch] gap-3" data-rich>{@render render(blocks)}</div>
{/if}
