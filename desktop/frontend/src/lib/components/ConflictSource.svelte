<script lang="ts">
  import {langOf, tokenize} from '$lib/diff';
  import {visibleSource, type fileComparison} from '$lib/conflictdiff';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  let {name,label,path,comparison,top,whitespace,chosen,suggested = false,onuse,onappend,onscroll}: {
    name:'Current'|'Incoming'; label:string; path:string; comparison:ReturnType<typeof fileComparison>;
    top:number; whitespace:boolean; chosen:boolean; suggested?:boolean; onuse:()=>void;
    onappend:(line:string,block:number)=>void; onscroll:(offset:number)=>void;
  } = $props();
  const language = $derived(langOf(path));
  let field=$state<HTMLDivElement>(), height=$state(0);
  const rowHeight=21;
  const page=$derived(Math.max(1,Math.ceil((height || 300)/rowHeight)));
  const start=$derived(Math.max(0,Math.min(comparison.length-page,Math.floor(top/rowHeight))-Math.ceil(page/4)));
  const rows=$derived(visibleSource(comparison,start,Math.ceil(page*1.5)+2,name==='Incoming',whitespace));
  let mirrored=-1;
  $effect(()=>{
    if(field && field.scrollTop!==top) {
      field.scrollTop=top;
      mirrored=field.scrollTop;
    }
  });
  function scrolled() {
    if(field && field.scrollTop!==mirrored && field.scrollTop!==top) {
      mirrored=field.scrollTop;
      onscroll(field.scrollTop);
    }
  }
</script>

<section class="source" class:incoming={name === 'Incoming'} aria-label={name + ' lines'}>
  <header><strong>{name}</strong><span title={label}>{label}</span>{#if suggested}<small class="suggested"><Icon name="sparkles" size={12}/>AI suggestion</small>{/if}<Button size="xs" class="pick" variant="secondary" selected={chosen} title={`Use ${name.toLowerCase()} for this change`} onclick={onuse}>Use {name.toLowerCase()}</Button></header>
    <!-- svelte-ignore a11y_no_noninteractive_tabindex (This scroll region supports native Page Up and Page Down.) -->
    <div class="code" bind:this={field} bind:clientHeight={height} onscroll={scrolled} tabindex="0" role="region" aria-label={name+' source scroll area'}>
      <div class="track" style:height={comparison.length*rowHeight+'px'}>
      <div class="window" style:top={start*rowHeight+'px'}>
      {#each rows as row}
        {#if row.gap}<div class="gap" aria-label="No corresponding line"></div>
        {:else}
          {@const number=row.number}
          <div class="line" class:changed={row.changed}><button aria-label={`Add ${name.toLowerCase()} line ${number}`} title="Append line to result" disabled={!row.changed} onclick={() => onappend(row.text,row.change)}><span>{number}</span><Icon name="plus" size={13}/></button>
            <span class="sign" aria-hidden="true">{row.changed ? name==='Current'?'−':'+' : ''}</span>
            <code>{#each tokenize(row.text.replace(/\r?\n$/,''),language) as token}<span class="syntax-{token.kind}">{row.formatting && row.changed ? token.text.replaceAll(' ','·').replaceAll('\t','→   ') : token.text}</span>{/each}{#if row.formatting && row.changed}<small>{row.text.endsWith('\r\n')?' CRLF':row.text.endsWith('\n')?' LF':' EOF'}</small>{/if}</code>
          </div>
        {/if}
      {/each}
      </div></div>
    </div>
</section>

<style>
  .suggested {display:inline-flex; align-items:center; gap:4px; flex:none; color:var(--side); font-size:11px;}
  .source {--side:var(--color-accent); display:flex; flex-direction:column; min-width:0; min-height:0;}
  .incoming {--side:var(--color-orange); border-inline-start:1px solid var(--color-edge);}
  header {display:flex; gap:8px; align-items:center; padding:6px 12px; border-bottom:1px solid var(--color-edge); min-height:32px;}
  header {background:color-mix(in srgb,var(--side) 7%,var(--color-canvas)); font-size:12px;}
  header strong {color:var(--side);} header span {color:var(--color-ink-2); overflow:hidden; text-overflow:ellipsis; white-space:nowrap;}
  .code {flex:1; overflow:auto; min-height:48px; padding:0; overflow-anchor:none; font:12px/21px var(--font-mono);}
  .track {position:relative;} .window {position:absolute; left:0; min-width:100%;}
  .line {height:21px;} .line button:disabled {cursor:default;}
  .line {display:flex; width:max-content; min-width:100%; align-items:baseline;}
  header :global(.pick) {margin-inline-start:auto;}
  .line.changed {background:color-mix(in srgb,var(--color-bad) 16%,var(--color-panel));}
  .incoming .line.changed {background:color-mix(in srgb,var(--color-good) 16%,var(--color-panel));}
  .sign {width:2ch; flex:none; text-align:center; color:var(--color-bad);}
  .incoming .sign {color:var(--color-good);}
  .gap {height:21px; background:var(--color-canvas);}
  code small {color:var(--color-faint);}
  .line:not(.changed) code {opacity:.7;}
  .line:hover,.line:focus-within {background:color-mix(in srgb,var(--side) 8%,transparent);}
  code {font:inherit; white-space:pre; padding-inline:8px; tab-size:4;}
  .line button {position:sticky; left:0; background:var(--color-canvas); color:var(--color-faint); width:5ch; text-align:right; padding-right:5px; font:inherit; cursor:pointer; flex:none;}
  .line button :global(svg){display:none; margin:auto;} .line:hover button:enabled span,.line:focus-within button:enabled span {display:none;}
  .line:hover button:enabled :global(svg),.line:focus-within button:enabled :global(svg){display:inline; color:var(--side);}
  .syntax-kw {color:var(--color-magenta);} .syntax-str {color:var(--color-good);} .syntax-com {color:var(--color-faint);} .syntax-num {color:var(--color-orange);}

</style>
