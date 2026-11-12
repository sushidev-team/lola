<script lang="ts">
  import { onDestroy, tick, untrack } from 'svelte';
  import { fade } from 'svelte/transition';
  import type { GitActions, GitMenuItem } from '$lib/gitactions.svelte';
  import MenuItem from './MenuItem.svelte';
  import Icon from './Icon.svelte';
  let { actions }: { actions: GitActions } = $props();
  const request = $derived(actions.request);
  let element = $state<HTMLDivElement>();
  $effect(() => {
    const req = request, node = element;
    if (!req || !node) return;
    untrack(() => {
      const rect = node.getBoundingClientRect();
      node.style.left = `${Math.max(6, Math.min(req.x, innerWidth - rect.width - 6))}px`;
      node.style.top = `${Math.max(6, Math.min(req.y, innerHeight - rect.height - 6))}px`;
      void tick().then(() => { if (actions.request === req && node.isConnected) node.querySelector<HTMLButtonElement>('[role="menuitem"]:not(:disabled)')?.focus(); });
    });
  });
  function key(event: KeyboardEvent) {
    event.stopPropagation();
    if (event.key === 'Escape' || event.key === 'Tab') { event.preventDefault(); actions.close(true); return; }
    const items = Array.from(element?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)') ?? []);
    if (!items.length) return;
    const index = items.indexOf(document.activeElement as HTMLButtonElement);
    if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault();
      items[event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length]?.focus();
    }
  }
  function run(item: GitMenuItem) { if (item.disabled) return; actions.close(true); item.run(); }
  onDestroy(() => actions.close());
  function portal(node: HTMLDivElement) { document.body.appendChild(node); return { destroy: () => node.remove() }; }
  function motion() { return typeof HTMLElement.prototype.animate === 'function' && !window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ? 100 : 0; }
</script>

<svelte:window onresize={() => actions.close()} />
{#if request}
  <div use:portal>
  <div class="git-menu-backdrop" role="presentation" onclick={() => actions.close()} oncontextmenu={(event) => { event.preventDefault(); actions.close(); }}></div>
  <div class="git-action-menu" bind:this={element} style={`left:${request.x}px;top:${request.y}px`} role="menu" aria-label="Git actions" tabindex="-1" onkeydown={key} transition:fade={{duration:motion()}}>
    <div class="menu-heading"><strong>{request.title}</strong>{#if request.subtitle}<span>{request.subtitle}</span>{/if}</div>
    {#each request.items as item}
      {#if item.separator}<div class="menu-separator" role="separator"></div>{/if}
      <MenuItem disabled={item.disabled} variant={item.danger ? 'danger' : 'default'} onclick={() => run(item)}><span class="action-label"><Icon name={item.icon} size={15} /><span>{item.label}</span></span></MenuItem>
    {/each}
  </div>
  </div>
{/if}

<style>
  .git-menu-backdrop { position:fixed; inset:0; z-index:80; }
  .git-action-menu { position:fixed; z-index:81; width:max-content; min-width:235px; max-width:min(440px,calc(100vw - 12px)); max-height:calc(100vh - 12px); overflow:auto; padding:5px; background:var(--color-panel); color:var(--color-ink); border:1px solid var(--color-edge); border-radius:8px; box-shadow:0 10px 28px #0004; }
  .menu-heading { display:grid; gap:3px; padding:5px 7px 6px; border-bottom:1px solid var(--color-edge); margin-bottom:3px; }
  .menu-heading strong { font-size:13px; font-weight:600; overflow-wrap:anywhere; }
  .menu-heading > span { font-size:12px; color:var(--color-ink-2); overflow-wrap:anywhere; }
  .menu-separator { height:1px; background:var(--color-edge); margin:4px 5px; }
  .git-action-menu :global(button > span:first-child) { display:none; }
  .git-action-menu :global(button) { padding-block:4px; }
  .action-label { display:flex; align-items:center; gap:7px; font-size:13px; line-height:18px; }
  .action-label > :global(svg) { flex-shrink:0; color:var(--color-ink-2); }
  .action-label > span { overflow:hidden; white-space:nowrap; text-overflow:ellipsis; }
</style>
