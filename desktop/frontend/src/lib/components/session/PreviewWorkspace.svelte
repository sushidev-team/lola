<script lang="ts">
  import { tick } from "svelte";
  import { preview, type OpenPreviewTab } from "$lib/preview.svelte";
  import { store } from "$lib/store.svelte";
  import { devUrlGroups, devUrlLabel } from "$lib/devurl";
  import Button from "../Button.svelte";
  import Icon from "../Icon.svelte";
  import Modal from "../Modal.svelte";
  import SessionPreview from "./SessionPreview.svelte";

  let width = $state(window.innerWidth);
  // Leave room for the 820px tablet preset beside a docked sidebar.
  const compact = $derived(width < 1100);
  const expanded = $derived(preview.sidebarOpen ?? (!compact && preview.tabs.length > 1));
  const current = $derived(preview.tabs[preview.active]);
  const session = $derived(store.sessions.find(s => s.id === current?.request.sessionId));
  const groups = $derived(devUrlGroups(session?.devUrls ?? [], session?.apps ?? []));
  let pages = $state<Record<number, SessionPreview>>({});
  let toggle = $state<HTMLButtonElement>();
  const label = (tab: OpenPreviewTab) => tab.title || tab.documentTitle || devUrlLabel(tab.currentURL);
  const tabId = (tab: OpenPreviewTab) => `preview-tab-${tab.id}`;

  // The port distinguishes servers; on the same server the route distinguishes pages.
  const origins = $derived(new Set(preview.tabs.map(t => new URL(t.request.url).origin)));
  function subtitle(tab: OpenPreviewTab) {
    const url = new URL(tab.currentURL);
    const route = url.pathname + url.search + url.hash;
    return origins.size === 1 && route !== "/" ? route : devUrlLabel(tab.currentURL);
  }

  function focusTab(index: number) {
    const tab = preview.tabs[index];
    if (tab && expanded) document.getElementById(tabId(tab))?.focus();
    else toggle?.focus();
  }
  async function toggleSidebar() {
    preview.sidebarOpen = !expanded;
    await tick();
    if (expanded) focusTab(preview.active);
    else toggle?.focus();
  }
  async function focusPage() {
    if (compact && expanded) closeSidebar();
    await tick();
    if (!current) return;
    const panel = document.getElementById("preview-page-" + current.id);
    (panel?.querySelector("iframe") ?? panel)?.focus({ preventScroll: true });
  }
  function closeSidebar() {
    preview.sidebarOpen = false;
    toggle?.focus();
  }
  function select(index: number) {
    preview.select(index);
    if (compact) closeSidebar();
  }
  async function closeTab(index: number) {
    preview.closeTab(index);
    await tick();
    if (current) focusTab(preview.active);
  }
  function onTabKey(e: KeyboardEvent, index: number) {
    const count = preview.tabs.length;
    let next: number;
    switch (e.key) {
      case "ArrowDown": next = (index + 1) % count; break;
      case "ArrowUp": next = (index + count - 1) % count; break;
      case "Home": next = 0; break;
      case "End": next = count - 1; break;
      case "Delete":
        e.preventDefault();
        void closeTab(index);
        return;
      default: return;
    }
    e.preventDefault();
    // Visiting a new prepared page can run a sign-in flow. Require activation
    // before starting one; moving between already-live documents is immediate.
    if (preview.tabs[next].visited) preview.select(next);
    focusTab(next);
  }
  function hidePreview() {
    preview.visible = false;
  }
  export function menuAction(action: string) {
    if (!current) return;
    if (action === "toggle-preview") { hidePreview(); return; }
    switch (action) {
      case "close-tab": void closeTab(preview.active); break;
      case "preview-duplicate": {
        const viewport = pages[current.id]?.device() ?? current.request.prep?.device;
        preview.duplicate(viewport);
        void focusPage();
        break;
      }
      case "new-shell": {
        // Open the advertised address for this server, even after in-page navigation.
        const origin = new URL(current.request.url).origin;
        const url = groups.flatMap(g => g.urls).find(url => new URL(url).origin === origin);
        preview.open(current.request.sessionId, url ?? origin);
        void focusPage();
        break;
      }
      case "preview-next":
      case "preview-previous": {
        const step = action === "preview-next" ? 1 : -1;
        preview.select((preview.active + step + preview.tabs.length) % preview.tabs.length);
        void focusPage();
        break;
      }
      case "toggle-sidebar": void toggleSidebar(); break;
      case "reload": pages[current.id]?.reloadPage(); break;
    }
  }
  function onModalKey(e: KeyboardEvent) {
    if (e.key === "Escape" && compact && expanded) {
      e.preventDefault();
      closeSidebar();
    }
  }
</script>

<svelte:window bind:innerWidth={width} />

{#if current}
  {#snippet header()}
    <div class="drag flex flex-wrap items-center gap-x-2 gap-y-1">
      <button bind:this={toggle} type="button" class="grid size-7 place-items-center rounded-md text-faint hover:bg-sel hover:text-ink"
        aria-label="Page sidebar" aria-keyshortcuts="Meta+b" title="Show or hide page sidebar · ⌘B" aria-expanded={expanded} aria-controls="preview-sidebar" onclick={toggleSidebar}>
        <Icon name="panel-left" size={16} />
      </button>
      <h2 class="mr-2 self-stretch content-center text-lg text-ink">Preview</h2>
      <nav aria-label="Dev servers" class="drag flex max-h-[22dvh] min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-1 overflow-y-auto">
        {#each groups as group (group.name)}
          <div role="group" aria-label={group.label || "Addresses"} class="grid min-w-0 max-w-full gap-0.5">
            {#if group.label}<h3 class="px-1.5 text-sm font-medium text-ink">{group.label}</h3>{/if}
            <div class="flex min-w-0 flex-wrap gap-1">
              {#each group.urls as url (url)}
                {@const action = `Open ${devUrlLabel(url)} in a new tab`}
                <Button size="xs" class="max-w-full font-mono!" title={action} aria-label={action} onclick={() => { preview.open(current.request.sessionId, url); if (compact && expanded) closeSidebar(); }}>
                  <Icon name="plus" size={12} /><span class="truncate">{devUrlLabel(url)}</span>
                </Button>
              {/each}
            </div>
          </div>
        {/each}
      </nav>
    </div>
  {/snippet}
  <Modal title="Preview" headerContent={header} visible={preview.visible} onClose={hidePreview} onkey={onModalKey} fullscreen bodyClass="min-w-0 overflow-hidden">
      <div class="relative flex h-full min-h-0 overflow-hidden">
        {#if compact}
          <button type="button" tabindex="-1" aria-label="Dismiss page sidebar" inert={!expanded} aria-hidden={!expanded}
            class="sidebar-scrim" class:expanded onclick={closeSidebar}></button>
        {/if}
        <aside id="preview-sidebar" aria-label="Preview pages" inert={!expanded} aria-hidden={!expanded}
          class="page-sidebar" class:compact class:expanded>
          <div role="tablist" aria-label="Open pages" aria-orientation="vertical" class="sidebar-content flex flex-col gap-1">
            {#each preview.tabs as tab, i (tab.id)}
              {@const title = label(tab)}
              {@const address = subtitle(tab)}
              <div role="presentation" class="group flex min-w-0 items-center rounded-md pr-1 {current === tab ? 'bg-accent-fill text-accent-ink' : 'text-faint hover:bg-sel/50'}">
                <button type="button" id={tabId(tab)} role="tab" aria-label={title} aria-selected={current === tab} aria-describedby={address !== title ? tabId(tab) + "-address" : undefined} aria-controls={'preview-page-' + tab.id}
                  tabindex={current === tab ? 0 : -1} title={tab.currentURL}
                  class="min-w-0 flex-1 rounded-md px-2 py-2 text-left" onclick={() => select(i)} onkeydown={(e) => onTabKey(e, i)}>
                  <span class="block truncate text-sm font-medium">{title}</span>
                  {#if address !== title}<span id={tabId(tab) + "-address"} class="block truncate font-mono text-xs text-faint">{address}</span>{/if}
                </button>
                <Button size="xs" icon aria-label={`Close ${title}`} title="Close tab · ⌘W" tabindex={current === tab ? 0 : -1}
                  class={current === tab ? "" : "opacity-0 group-hover:opacity-100 group-focus-within:opacity-100"} onclick={() => closeTab(i)}>
                  <Icon name="x" size={12} />
                </Button>
              </div>
            {/each}
          </div>
        </aside>
        <div class="relative min-w-0 flex-1 overflow-hidden" inert={compact && expanded}>
          <!-- Keep parked frames laid out. display:none resets iframe scroll in Chromium. -->
          {#each preview.tabs as tab (tab.id)}
            <div role="tabpanel" tabindex="-1" id={'preview-page-' + tab.id} aria-labelledby={tabId(tab)} class="absolute inset-0" style:visibility={tab === current ? undefined : "hidden"} aria-hidden={tab !== current} inert={tab !== current}>
              {#if tab.visited}
                <SessionPreview {tab} active={preview.visible && current === tab} bind:this={pages[tab.id]} />
              {/if}
            </div>
          {/each}
        </div>
      </div>
  </Modal>
{/if}

<style>
  .page-sidebar {
    position: relative;
    z-index: 10;
    flex: 0 0 auto;
    width: min(13rem, 100%);
    /* Reserve dock space once per toggle so the framed app reflows only once. */
    margin-inline-end: -13rem;
    background: var(--color-panel);
    transform: translateX(-100%);
    transition: transform 180ms ease-out;
  }
  .page-sidebar.expanded { margin-inline-end: 0; transform: translateX(0); }
  .sidebar-content {
    height: 100%;
    padding: .5rem;
    overflow-y: auto;
    border-right: 1px solid var(--color-edge);
  }
  .page-sidebar.compact {
    position: absolute;
    inset: 0 auto 0 0;
    margin-inline-end: 0;
  }
  .page-sidebar.compact.expanded { box-shadow: 2px 0 14px #0003; }
  .sidebar-scrim {
    position: absolute;
    inset: 0;
    z-index: 5;
    background: rgb(0 0 0 / 15%);
    opacity: 0;
    pointer-events: none;
    transition: opacity 180ms ease-out;
  }
  .sidebar-scrim.expanded { opacity: 1; pointer-events: auto; }
  @media (prefers-reduced-motion: reduce) {
    .page-sidebar, .sidebar-scrim { transition: none; }
  }
</style>
