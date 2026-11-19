<script lang="ts">
  import {langOf, tokenize} from '$lib/diff';
  import {selectedLines} from '$lib/conflictmerge';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  let {name, label, value, path, firstLine, chosen, onuse, onappend}: {
    name:'Current'|'Incoming'; label:string; value:string; path:string; firstLine:number;
    chosen:boolean; onuse:()=>void; onappend:(lines:string)=>void;
  } = $props();
  const lines = $derived(value.length < 64_000 ? value.match(/[^\n]*\n|[^\n]+$/g) ?? [] : null);
  const language = $derived(langOf(path));
  let field = $state<HTMLTextAreaElement>();
</script>

<section class="source" class:incoming={name === 'Incoming'} aria-label={name + ' lines'}>
  <header><strong>{name}</strong><span title={label}>{label}</span><Button size="xs" class="pick" variant="secondary" selected={chosen} onclick={onuse}>Use {name.toLowerCase()}</Button></header>
  {#if lines && lines.length <= 300}
    <div class="code">
      {#each lines as line, i}
        <div class="line"><button aria-label={`Add ${name.toLowerCase()} line ${firstLine+i}`} title="Append line to result" onclick={() => onappend(line)}><span>{firstLine+i}</span><Icon name="plus" size={13}/></button><code>{#each tokenize(line.replace(/\r?\n$/,''),language) as token}<span class="syntax-{token.kind}">{token.text}</span>{/each}</code></div>
      {:else}<p>No lines on this side</p>{/each}
    </div>
  {:else}
    <textarea bind:this={field} aria-label={name+' source text'} readonly spellcheck="false" wrap="off" {value}></textarea>
  {/if}
  {#if field && (!lines || lines.length > 300)}<footer><Button size="xs" onclick={() => {if(field){const picked=selectedLines(field.value,field.selectionStart,field.selectionEnd); onappend(value.includes('\r\n')?picked.replace(/\n/g,'\r\n'):picked);}}}>Add selected lines</Button></footer>{/if}
</section>

<style>
  .source {--side:var(--color-accent); display:flex; flex-direction:column; min-width:0; min-height:0;}
  .incoming {--side:var(--color-orange); border-inline-start:1px solid var(--color-edge);}
  header,footer {display:flex; gap:8px; align-items:center; padding:6px 12px; border-bottom:1px solid var(--color-edge); min-height:32px;}
  header {background:color-mix(in srgb,var(--side) 7%,var(--color-canvas)); font-size:12px;}
  header strong {color:var(--side);} header span {color:var(--color-ink-2); overflow:hidden; text-overflow:ellipsis; white-space:nowrap;}
  .code,textarea {flex:1; overflow:auto; min-height:48px; padding-block:6px; font:12px/21px var(--font-mono);}
  textarea {width:100%; resize:none; padding-inline:12px; background:var(--color-panel);}
  .line {display:flex; width:max-content; min-width:100%; align-items:baseline;}
  header :global(.pick) {margin-inline-start:auto;}
  .line {background:color-mix(in srgb,var(--side) 4%,var(--color-panel));}
  .line:hover,.line:focus-within {background:color-mix(in srgb,var(--side) 8%,transparent);}
  code {font:inherit; white-space:pre; padding-inline:8px; tab-size:4;}
  .line button {position:sticky; left:0; background:var(--color-canvas); color:var(--color-faint); width:5ch; text-align:right; padding-right:5px; font:inherit; cursor:pointer; flex:none;}
  .line button :global(svg){display:none; margin:auto;} .line:hover button span,.line:focus-within button span {display:none;}
  .line:hover button :global(svg),.line:focus-within button :global(svg){display:inline; color:var(--side);}
  .syntax-kw {color:var(--color-magenta);} .syntax-str {color:var(--color-good);} .syntax-com {color:var(--color-faint);} .syntax-num {color:var(--color-orange);}
  footer {border-top:1px solid var(--color-edge);} p {font:12px var(--font-sans); color:var(--color-faint); padding:8px 12px;}
</style>
