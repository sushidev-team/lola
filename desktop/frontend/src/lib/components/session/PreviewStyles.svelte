<script lang="ts">
  import type { ElementInfo } from "$lib/previewtools";
  import PreviewStyleDeclarations from "./PreviewStyleDeclarations.svelte";

  let { element, onapply, oncomputed }: {
    element: ElementInfo; onapply: (name: string, value: string, originalName?: string, ruleId?: number, enabled?: boolean) => Promise<string>; oncomputed: () => void;
  } = $props();
</script>

<div class="styles">
  <section aria-label="Inline style" class="inline-rule">
    <header><code>element.style <span class="punct">{'{'}</span></code><small title={element.tag}>{element.tag}</small></header>
    <PreviewStyleDeclarations declarations={element.inline} {onapply} ontoggle={(name, enabled) => onapply(name, "", name, undefined, enabled)}/>
    <code class="punct">{'}'}</code>
  </section>

  <section aria-label="Matching rules" class="rules">
    <h3>Matching rules</h3>
    {#each element.rules as rule, i (rule.id ?? -(i + 1))}
      <div class="rule" role="group" aria-label={rule.selector}>
        <header><code>{rule.selector} <span class="punct">{'{'}</span></code></header>
        <small class="source" title={rule.source}>{rule.source}{rule.context ? " · " + rule.context : ""}</small>
        <PreviewStyleDeclarations declarations={rule.declarations} ontoggle={(name, enabled) => onapply(name, "", name, rule.id, enabled)}
          onapply={rule.id === undefined ? undefined : (name, value, originalName) => onapply(name, value, originalName, rule.id)}/>
        <code class="punct">{'}'}</code>
      </div>
    {:else}
      {#if !element.unreadable}<p>No matching rules.</p>{/if}
    {/each}
    {#if element.unreadable}
      <p>{element.unreadable} {element.unreadable === 1 ? "stylesheet is" : "stylesheets are"} restricted by the browser.
        <button class="link" onclick={oncomputed}>View computed styles</button></p>
    {/if}
    {#if element.truncated}<p>Showing 120 matching rules.</p>{/if}
  </section>
</div>

<style>
  .styles {min-height:0;overflow:auto;flex:1;color:var(--color-ink);font-size:var(--text-sm)}
  .inline-rule,.rules {padding:10px}
  .inline-rule {border-bottom:1px solid var(--color-edge)}
  header {display:flex;align-items:baseline;gap:6px}
  header {justify-content:space-between}
  header {margin-bottom:4px}
  header code {color:var(--color-accent-ink);overflow-wrap:anywhere}
  code {font-family:var(--font-mono)}
  small,.punct,.rules p {color:var(--color-faint)}
  small {font-size:inherit;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
  header small {max-width:45%;flex-shrink:2}
  .link {cursor:pointer;color:var(--color-accent-ink)}
  h3 {color:var(--color-ink-2);font-weight:600}
  .rules p {margin:6px 0;line-height:1.5}
  .link {text-decoration:underline;text-underline-offset:2px}
  .rule {padding:8px 0;border-bottom:1px solid var(--color-edge)}
  .source {display:block;margin-bottom:3px}
</style>
