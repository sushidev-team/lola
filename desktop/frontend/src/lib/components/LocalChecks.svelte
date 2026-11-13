<script lang="ts">
  import type { BoardCheck } from "@bindings/internal/protocol";
  import { checkKind, CHECK_WORD, EVIDENCE_TAG } from "$lib/board";
  import { tooltip } from "$lib/tooltip";
  import BoardMarker from "./BoardMarker.svelte";
  import Icon from "./Icon.svelte";
  let { checks, live = false }: { checks: BoardCheck[]; live?: boolean } = $props();
</script>

<ul class="local-checks">
  {#each checks as c (c.name)}
    {@const kind = checkKind(c.state)}
    {@const warning = c.evidence === 'unverified' || c.evidence === 'contradicted'}
    <li class="local-check-row">
      <span class="local-check-mark"><BoardMarker {kind} {live} /></span>
      <span class="local-check-content" class:has-detail={!!c.summary}>
        <span class="local-check-name font-medium text-ink" title={c.name}>{c.name}</span>
        {#if c.summary}<span class="local-check-detail selectable text-ink-2" title={c.summary}>{c.summary}</span>{/if}
        {#if warning}<span class="shrink-0 text-warn" use:tooltip={{ text: c.evidenceNote || EVIDENCE_TAG[c.evidence!].word }}><Icon name="triangle-alert" size={12} /><span class="sr-only">{EVIDENCE_TAG[c.evidence!].word}</span></span>{/if}
      </span>
      <span class="local-check-result {kind === 'pass' ? 'text-good' : kind === 'fail' ? 'text-bad' : 'text-info'}"
        use:tooltip={{ text: c.evidenceNote || (c.evidence ? EVIDENCE_TAG[c.evidence]?.word : '') || 'Agent-reported result' }}>{CHECK_WORD[kind]}</span>
    </li>
  {/each}
</ul>

<style>
  .local-checks { display: grid; }
  .local-check-row { display: grid; grid-template-columns: 16px minmax(0, 1fr) auto; gap: 9px; align-items: center; padding: 8px 0; border-bottom: 1px solid color-mix(in srgb, var(--color-edge) 55%, transparent); }
  .local-check-row:last-child { border-bottom: 0; }
  .local-check-mark { display: flex; align-items: center; }
  .local-check-result { font-size: 11px; line-height: 18px; white-space: nowrap; }
  .local-check-content { display: flex; align-items: center; gap: 8px; min-width: 0; }
  .local-check-name, .local-check-detail { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .local-check-name { min-width: 0; }
  .has-detail .local-check-name { flex-shrink: 0; max-width: 45%; }
  .local-check-detail { min-width: 0; font-size: 12px; line-height: 18px; }
</style>
