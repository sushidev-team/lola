<script lang="ts">
  import { tick } from "svelte";
  import Icon from "../Icon.svelte";
  import PreviewStyleRow from "./PreviewStyleRow.svelte";

  let { declarations, onapply, ontoggle }: {
    declarations: string[][];
    ontoggle: (name: string, enabled: boolean) => Promise<string>;
    onapply?: (name: string, value: string, originalName?: string) => Promise<string>;
  } = $props();

  let adding = $state(false), insertButton = $state<HTMLButtonElement>(), newRow = $state<PreviewStyleRow>();
  const rows = $state<(PreviewStyleRow | undefined)[]>([]);

  async function add() {
    adding = true;
    await tick();
    await newRow?.focusPart("name");
  }
  async function finish(restoreFocus: boolean) {
    adding = false;
    await tick();
    if (restoreFocus) insertButton?.focus();
  }
  function focusRow(name: string) {
    const index = declarations.findIndex(([key]) => key === name);
    if (rows[index]) void rows[index].focusPart("name");
    else insertButton?.focus();
  }
  function step(name: string, back: boolean) {
    const index = name ? declarations.findIndex(([key]) => key === name) : declarations.length;
    const next = index + (back ? -1 : 1);
    if (next === declarations.length && !adding) {
      insertButton?.focus();
      return true;
    }
    const row = next === declarations.length ? newRow : rows[next];
    if (!row) return false;
    void row.focusPart(back ? "value" : "name");
    return true;
  }
</script>

{#each declarations as [key, val, state], i (key)}
  {#if onapply}
    <PreviewStyleRow bind:this={rows[i]} name={key} value={val} disabled={state === "disabled"} {ontoggle} {declarations} {onapply} onreplaced={focusRow} onstep={step}/>
  {:else}
    <code class="readonly">{key}: {val};</code>
  {/if}
{/each}
{#if onapply}
  {#if adding}
    <PreviewStyleRow bind:this={newRow} {declarations} {onapply} onreplaced={focusRow} onstep={step} onfinish={finish}/>
  {:else}
    <button class="insert" bind:this={insertButton} type="button" aria-label="Add declaration" title="Add declaration" onclick={add}
      onkeydown={event => {
        if (event.key === "Tab" && event.shiftKey && step("", true)) event.preventDefault();
      }}>
      <span class="plus"><Icon name="plus" size={12}/></span>
    </button>
  {/if}
{/if}

<style>
  .readonly {display:block;padding:3px 12px;overflow-wrap:anywhere}
  .insert {position:relative;display:grid;place-items:center;width:100%;height:16px;cursor:pointer;color:var(--color-on-accent)}
  .insert::before {content:"";position:absolute;inset-inline:4px;top:50%;height:1px;background:var(--color-accent);opacity:0}
  .plus {position:relative;display:grid;place-items:center;width:16px;height:16px;border-radius:50%;background:var(--color-accent);opacity:0}
  .insert:is(:hover,:focus-visible)::before,.insert:is(:hover,:focus-visible) .plus {opacity:1}
  @media (hover:none) { .insert::before {opacity:.45} .plus {opacity:1} }
</style>
