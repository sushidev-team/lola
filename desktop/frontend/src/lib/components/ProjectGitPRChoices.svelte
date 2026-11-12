<script lang="ts">
  import { tick } from 'svelte';
  import type { PRChoice } from '$lib/gitpullrequest';
  import Icon from './Icon.svelte';
  import MenuItem from './MenuItem.svelte';
  let {label,options,selected=$bindable([]),disabled=false}:{label:string;options:PRChoice[];selected?:string[];disabled?:boolean}=$props();
  const uid=$props.id();
  let query=$state(''),open=$state(false),active=$state(0);
  let field=$state<HTMLInputElement>(),menu=$state<HTMLDivElement>();
  let position=$state({left:0,top:0,width:0,height:240});
  const shown=$derived(options.filter(o=>`${o.label} ${o.value}`.toLowerCase().includes(query.toLowerCase())));
  const summary=$derived(selected.map(value=>options.find(o=>o.value===value)?.label??value).join(', '));
  function place(){
    if(!field)return;
    const rect=field.getBoundingClientRect(),width=Math.min(Math.max(rect.width,220),window.innerWidth-16);
    const desired=Math.min(240,Math.max(40,shown.length*32+8)),below=window.innerHeight-rect.bottom-8,above=rect.top-8;
    const up=below<desired&&above>below,height=Math.min(desired,Math.max(40,up?above:below));
    position={left:Math.max(8,Math.min(rect.left,window.innerWidth-width-8)),top:up?rect.top-height-4:rect.bottom+4,width,height};
  }
  function show(){if(disabled)return;query='';active=0;place();open=true;}
  function close(){open=false;query='';}
  function toggle(value:string){selected=selected.includes(value)?selected.filter(v=>v!==value):[...selected,value];}
  async function reveal(){await tick();menu?.querySelector<HTMLElement>(`[id="${uid}-${active}"]`)?.scrollIntoView?.({block:'nearest'});}
  function key(event:KeyboardEvent){
    if(event.key==='Escape'&&open){event.preventDefault();event.stopPropagation();close();return;}
    if(event.key==='Tab'){close();return;}
    if(event.key==='ArrowDown'||event.key==='ArrowUp'){
      event.preventDefault();event.stopPropagation();
      if(!open)show();else active=shown.length?(active+(event.key==='ArrowDown'?1:-1)+shown.length)%shown.length:0;
      void reveal();return;
    }
    if(event.key==='Enter'){event.preventDefault();event.stopPropagation();if(!open)show();else if(shown[active])toggle(shown[active].value);}
  }
  function portal(node:HTMLDivElement){document.body.appendChild(node);return {destroy:()=>node.remove()};}
  $effect(()=>{if(disabled)close();});
  $effect(()=>{
    if(!open)return;
    const outside=(event:PointerEvent)=>{if(event.target!==field&&!menu?.contains(event.target as Node))close();};
    const scroll=(event:Event)=>{if(!menu?.contains(event.target as Node))close();};
    document.addEventListener('pointerdown',outside,true);window.addEventListener('scroll',scroll,true);window.addEventListener('resize',place);
    return()=>{document.removeEventListener('pointerdown',outside,true);window.removeEventListener('scroll',scroll,true);window.removeEventListener('resize',place);};
  });
</script>
<div class="pr-combobox">
  <label for={uid}>{label}</label>
  <div class="choice-field">
    <input id={uid} bind:this={field} role="combobox" aria-autocomplete="list" aria-expanded={open} aria-controls={open?`${uid}-list`:undefined} aria-activedescendant={open&&shown[active]?`${uid}-${active}`:undefined} {disabled} placeholder={open?'Search…':'None'} value={open?query:summary} title={!open?summary:undefined} onfocus={show} onclick={()=>{if(!open)show();}} oninput={event=>{query=event.currentTarget.value;active=0;if(!open)open=true;place();}} onkeydown={key} onblur={close}/>
    <Icon name="chevron-down" size={13} class="choice-caret"/>
  </div>
</div>
{#if open}
 <div use:portal bind:this={menu} class="lola-select-menu pr-choice-menu" style={`left:${position.left}px;top:${position.top}px;width:${position.width}px;max-height:${position.height}px`} id={`${uid}-list`} role="listbox" aria-label={label} aria-multiselectable="true">
  {#each shown as option,i (option.value)}
   <MenuItem role="option" id={`${uid}-${i}`} tabindex={-1} aria-selected={selected.includes(option.value)} icon={selected.includes(option.value)?'✓':''} data-active={active===i} onpointermove={()=>active=i} onmousedown={event=>event.preventDefault()} onclick={()=>toggle(option.value)}>{option.label}</MenuItem>
  {:else}<p role="status">{options.length?'No matches':'No options available'}</p>{/each}
 </div>
{/if}
<style>
 .pr-combobox { min-width:0; }
 label { display:block; margin-bottom:5px; color:var(--color-ink-2); font-size:13px; }
 .choice-field { position:relative; }
 input { width:100%; min-width:0; padding:7px 28px 7px 9px; border:1px solid var(--color-edge); border-radius:6px; background:var(--color-canvas); color:var(--color-ink); font-size:13px; text-overflow:ellipsis; }
 input::placeholder { color:var(--color-faint); }input:focus { outline:2px solid var(--color-accent); outline-offset:1px; }input:disabled { opacity:.4;cursor:not-allowed; }
 .choice-field :global(.choice-caret) { position:absolute; right:9px; top:50%; transform:translateY(-50%); pointer-events:none; color:var(--color-faint); }
 .pr-choice-menu :global(button) { cursor:pointer; transition:none; }
 .pr-choice-menu :global(button[data-active="true"]) { background:var(--color-sel); }
 .pr-choice-menu p { padding:7px; color:var(--color-faint); font-size:13px; }
</style>
