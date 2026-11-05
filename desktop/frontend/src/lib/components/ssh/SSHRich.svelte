<script lang="ts">
  // The agent's prose: headlines, lists, tables, callouts and folds. It renders from parsed
  // data and has no {@html}, because this text is written by something that reads untrusted
  // remote output. Links and block references come from SSHText, as everywhere else.
  import { inlineSpans, itemStatus, richBlocks, type RichBlock } from "$lib/ssh";
  import Disclosure from "../Disclosure.svelte";
  import SSHText from "./SSHText.svelte";

  let { sessionId, text }: { sessionId: string; text: string | undefined } = $props();

  const blocks = $derived(richBlocks(text ?? ""));

  const toneRail = { ok: "border-good", note: "border-info", warn: "border-warn", bad: "border-bad" } as const;
  const toneMark = { ok: "text-good", warn: "text-warn", bad: "text-bad" } as const;
  const toneWord = { ok: "OK", note: "Note", warn: "Warning", bad: "Problem" } as const;
</script>

{#snippet inline(line: string)}
  {#each inlineSpans(line) as s, i (i)}{#if s.kind === "code"}<code class="selectable rounded bg-sel/60 px-1 py-px font-mono text-sm break-all">{s.text}</code>{:else if s.kind === "bold"}<strong class="font-medium"><SSHText {sessionId} text={s.text} /></strong>{:else}<SSHText {sessionId} text={s.text} />{/if}{/each}
{/snippet}

{#snippet mark(tone: "ok" | "warn" | "bad")}
  <svg viewBox="0 0 24 24" class="size-3.5 translate-y-0.5 {toneMark[tone]}" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" role="img" aria-label={toneWord[tone]}>
    {#if tone === "ok"}<path d="m5 12.5 4.5 4.5L19 7.5" />{:else if tone === "bad"}<path d="M7 7l10 10M17 7 7 17" />{:else}<path d="M12 5v9M12 19v.01" />{/if}
  </svg>
{/snippet}

{#snippet render(list: RichBlock[])}
  {#each list as b, i (i)}
    {#if b.kind === "lead"}
      <p class="font-medium text-ink" data-rich-lead>{@render inline(b.text)}</p>
    {:else if b.kind === "p"}
      <p>{@render inline(b.text)}</p>
    {:else if b.kind === "heading"}
      <p class="mt-1 text-sm font-medium text-faint">{@render inline(b.text)}</p>
    {:else if b.kind === "code"}
      <pre class="selectable overflow-x-auto rounded-md border border-edge/60 bg-canvas px-2.5 py-1.5 font-mono text-sm">{b.text}</pre>
    {:else if b.kind === "callout"}
      <div class="rounded-r border-l-2 bg-canvas/50 px-2.5 py-1 {toneRail[b.tone]}" data-rich-callout={b.tone}>
        <span class="sr-only">{toneWord[b.tone]}: </span>{@render inline(b.text)}
      </div>
    {:else if b.kind === "table"}
      <div class="overflow-x-auto" data-rich-table>
        <table class="w-full border-collapse text-sm">
          <thead>
            <tr>{#each b.head as cell, c (c)}<th class="border-b border-edge/60 py-1 pr-3 text-left {b.head.length === 2 && c === 0 ? 'font-normal text-faint' : 'font-medium text-ink'}">{@render inline(cell)}</th>{/each}</tr>
          </thead>
          <tbody>
            {#each b.rows as row, r (r)}
              <tr>{#each row as cell, c (c)}<td class="border-b border-edge/30 py-1 pr-3 align-top {b.head.length === 2 && c === 0 ? 'text-faint' : ''}">{@render inline(cell)}</td>{/each}</tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else if b.kind === "fold"}
      <Disclosure label={b.title} class="-ml-1.5">
        <div class="mt-1 ml-1.5 grid gap-2 border-l border-edge/60 pl-2.5">{@render render(b.blocks)}</div>
      </Disclosure>
    {:else}
      <svelte:element this={b.kind} class="grid gap-1">
        {#each b.items as item, n (n)}
          {@const st = itemStatus(item)}
          <li class="grid grid-cols-[1.25rem_minmax(0,1fr)] items-baseline">
            {#if st.tone}{@render mark(st.tone)}{:else}<span class="num text-faint" aria-hidden="true">{b.kind === "ol" ? `${n + 1}.` : "–"}</span>{/if}
            <span>{@render inline(st.text)}</span>
          </li>
        {/each}
      </svelte:element>
    {/if}
  {/each}
{/snippet}

<div class="copy grid max-w-[76ch] gap-2" data-rich>{@render render(blocks)}</div>
