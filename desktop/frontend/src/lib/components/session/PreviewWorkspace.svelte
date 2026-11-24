<script lang="ts">
  import { tick, untrack } from "svelte";
  import { isPreviewDevServer, preview, previewAddress, type OpenPreviewTab } from "$lib/preview.svelte";
  import { store } from "$lib/store.svelte";
  import { devUrlGroups, devUrlLabel } from "$lib/devurl";
  import { previewPlace, readPlaces, resolvePlace, savePlaces, type PreviewPlace, type PreviewPlaces } from "$lib/previewplaces";
  import Button from "../Button.svelte";
  import Icon from "../Icon.svelte";
  import Modal from "../Modal.svelte";
  import SessionPreview from "./SessionPreview.svelte";
  import ExternalPreview from "./ExternalPreview.svelte";
  import PreviewDeveloperTools from "./PreviewDeveloperTools.svelte";
  import PreviewSplitter from "./PreviewSplitter.svelte";
  import PreviewTabIcon from "./PreviewTabIcon.svelte";

  let width = $state(window.innerWidth);
  let toolsOpen = $state(false);
  let splitWidth = $state(0), pageHeight = $state(0), customWidth = $state<number | null>(null);
  let splitHeight = $state(0), customHeight = $state<number | null>(null);
  // Leave room for the 820px tablet preset beside a docked sidebar.
  const compact = $derived(width < 1100);
  let customSidebar = $state<number | null>(null);
  const maxSidebar = $derived(Math.max(1, Math.min(560, width - (compact ? 48 : 400))));
  const minSidebar = $derived(Math.min(160, maxSidebar));
  const sidebarWidth = $derived(Math.round(Math.max(minSidebar, Math.min(maxSidebar, customSidebar ?? Math.max(240, Math.min(280, width * .18))))));
  const handleWidth = 12;
  const maxPage = $derived(Math.max(1, splitWidth - handleWidth - (toolsOpen ? Math.min(248, splitWidth * .45) : 0)));
  const minPage = $derived(Math.min(200, maxPage));
  const fitWidth = (n: number) => Math.round(Math.max(minPage, Math.min(maxPage, n)));
  const pageWidth = $derived(fitWidth(customWidth ?? (toolsOpen ? splitWidth - handleWidth - 360 : maxPage)));
  const maxHeight = $derived(Math.max(1, splitHeight - handleWidth));
  const minHeight = $derived(Math.min(120, maxHeight));
  const stageHeight = $derived(Math.min(maxHeight, Math.max(minHeight, customHeight ?? maxHeight)));
  function responsive() {
    const page = pages[current.id];
    if (page && "useResponsiveWidth" in page) page.useResponsiveWidth();
  }
  function toggleTools() {
    if (compact && expanded) closeSidebar();
    toolsOpen = !toolsOpen;
  }
  const expanded = $derived(preview.sidebarOpen ?? (!compact && preview.tabs.length > 1));
  const current = $derived(preview.tabs[preview.active]);
  const session = $derived(store.sessions.find(s => s.id === current?.request.sessionId));
  const groups = $derived(devUrlGroups(session?.devUrls ?? [], session?.apps ?? []));
  const servers = $derived(groups.flatMap(group => group.urls));
  let pages = $state<Record<number, SessionPreview | ExternalPreview>>({});
  const page = $derived(current ? pages[current.id] : undefined);
  const dimensions = $derived(page && "viewportSize" in page ? page.viewportSize() : { width: pageWidth, height: pageHeight });
  const target = $derived(toolsOpen && page && "debugTarget" in page ? page.debugTarget() : undefined);
  let toggle = $state<HTMLButtonElement>();
  const label = (tab: OpenPreviewTab) => tab.title || tab.documentTitle || devUrlLabel(tab.currentURL);
  const tabId = (tab: OpenPreviewTab) => `preview-tab-${tab.id}`;

  let addressTab = $state<number | null>(null);
  let draft = $state("");
  let addressError = $state("");
  let addressInput = $state<HTMLInputElement>();
  let addressButton = $state<HTMLButtonElement>();
  const editing = $derived(!!current && preview.visible && addressTab === current.id);

  const placesKey = $derived(session ? "lola.preview.places.v1/" +
    (session.project ? "project/" + session.project : "session/" + session.id) : "");
  let places = $state.raw<PreviewPlaces>({ bookmarks: [], recent: [] });
  let loadedKey = "", lastVisit = "";
  const currentPlace = $derived.by(() => {
    if (!current) return;
    const place = previewPlace(current.currentURL, groups);
    // During session refreshes a local tab may temporarily lack its server list.
    if (place && place.app === undefined && !current.request.external) return;
    return place;
  });
  const addressOf = (p: PreviewPlace) => resolvePlace(p, groups, current.currentURL);
  const matchesPage = (p: PreviewPlace) => !!currentPlace && p.app === currentPlace.app && p.address === currentPlace.address;
  const bookmarked = $derived(places.bookmarks.some(matchesPage));
  const query = $derived(draft === current?.currentURL ? "" : draft.trim().toLowerCase());
  const matchesQuery = (p: PreviewPlace) => (addressOf(p) ?? p.address).toLowerCase().includes(query);
  const bookmarks = $derived(places.bookmarks.filter(matchesQuery));
  const recent = $derived(places.recent.filter(matchesQuery).sort((a, b) => Number(matchesPage(a)) - Number(matchesPage(b))));
  let highlighted = $state.raw<PreviewPlace>();
  const highlightId = $derived(highlighted && recent.includes(highlighted) ? "preview-recent-" + recent.indexOf(highlighted) : undefined);
  function updatePlaces(next: PreviewPlaces) {
    places = next;
    savePlaces(placesKey, next);
  }
  $effect(() => {
    const key = placesKey, page = currentPlace;
    if (!key || !page || !preview.visible) return;
    untrack(() => {
      if (loadedKey !== key) { loadedKey = key; lastVisit = ""; places = readPlaces(key); }
      const visitKey = JSON.stringify([page.app, page.address]);
      if (visitKey === lastVisit) return;
      lastVisit = visitKey;
      if (places.recent[0]?.address === page.address && places.recent[0]?.app === page.app) return;
      updatePlaces({ ...places, recent: [page, ...places.recent.filter(p => !matchesPage(p))].slice(0, 20) });
    });
  });
  function toggleBookmark() {
    if (!placesKey || !currentPlace) return;
    updatePlaces({ ...places, bookmarks: bookmarked ? places.bookmarks.filter(p => !matchesPage(p)) : [currentPlace, ...places.bookmarks] });
  }
  function removeRecent(place: PreviewPlace) {
    const index = recent.indexOf(place);
    const next = recent[index + 1] ?? recent[index - 1];
    if (highlighted === place) highlighted = next;
    updatePlaces({ ...places, recent: places.recent.filter(p => p !== place) });
    addressInput?.focus();
  }
  function visitSaved(url: string) {
    addressTab = null;
    navigate(url);
  }

  async function editAddress() {
    if (!current || !preview.visible) return;
    if (!editing) draft = current.currentURL;
    highlighted = undefined;
    addressError = "";
    addressTab = current.id;
    await tick();
    addressInput?.focus();
    addressInput?.select();
  }
  async function dismissAddress() {
    addressTab = null;
    await tick();
    addressButton?.focus();
  }
  function goToAddress(e: SubmitEvent) {
    e.preventDefault();
    if (!current || !editing) return;
    const url = previewAddress(draft, current.currentURL, servers);
    if (!url) {
      addressError = "Enter a website address or a page path.";
      addressInput?.focus();
      return;
    }
    addressTab = null;
    navigate(url);
  }
  function navigate(url: string, back = false) {
    const page = pages[current.id];
    const external = !isPreviewDevServer(url, servers);
    const sameBrowser = external && current.request.external;
    preview.visit(current, url, back || sameBrowser ? "replace" : "push");
    current.canGoBackInFrame = false;
    if (sameBrowser || (!external && !current.request.external && new URL(url).origin === new URL(current.request.url).origin)) {
      page.navigatePage(url);
    } else {
      // A different stack member has its own login store and prepared flows.
      // Mount a fresh preview, carrying only the reviewer's viewport choice.
      const device = "device" in page ? page.device() : undefined;
      current.request = { sessionId: current.request.sessionId, url, ...(external ? { external } : device ? { prep: { device } } : {}) };
      current.favicon = undefined;
      current.title = "";
    }
    current.documentTitle = "";
    void focusPage();
  }
  function goBack() {
    if (current.canGoBackInFrame && pages[current.id]?.backInFrame()) return;
    const url = current.backStack.pop();
    if (url) navigate(url, true);
  }
  function addressKey(e: KeyboardEvent) {
    if (e.isComposing || e.keyCode === 229) return;
    if (e.target === addressInput && !e.altKey && !e.metaKey && !e.ctrlKey && !e.shiftKey) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        const available = recent.filter(p => addressOf(p));
        if (!available.length) return;
        e.preventDefault();
        const i = highlighted ? available.indexOf(highlighted) : -1;
        highlighted = e.key === "ArrowDown" ? available[Math.min(i + 1, available.length - 1)] : available[i - 1];
        void tick().then(() => highlightId && document.getElementById(highlightId)?.scrollIntoView?.({ block: "nearest" }));
        return;
      }
      if (e.key === "Enter" && highlightId && highlighted) {
        const url = addressOf(highlighted);
        if (url) { e.preventDefault(); visitSaved(url); return; }
      }
    }
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      void dismissAddress();
    }
  }
  function openServer(url: string) {
    addressTab = null;
    preview.open(current.request.sessionId, url);
    void focusPage();
  }

  // The port distinguishes servers; on the same server the route distinguishes pages.
  const origins = $derived(new Set(preview.tabs.map(t => new URL(t.currentURL).origin)));
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
  export function hidePreview() {
    addressTab = null;
    preview.visible = false;
  }
  export function menuAction(action: string) {
    if (!current) return;
    if (action === "toggle-preview") { hidePreview(); return; }
    // AppKit owns ⌘L even inside a focused, cross-origin frame. Outside
    // Preview the same menu item routes to the SSH conversation.
    if (action === "ssh-talk") { void editAddress(); return; }
    // Keep unrelated app shortcuts from dismissing a partially typed URL.
    if (editing) {
      if (action === "close-tab") void dismissAddress();
      return;
    }
    switch (action) {
      case "close-tab": void closeTab(preview.active); break;
      case "preview-inspector": toggleTools(); break;
      case "preview-duplicate": {
        const page = pages[current.id];
        const viewport = page && "device" in page ? page.device() : current.request.prep?.device;
        preview.duplicate(viewport);
        void focusPage();
        break;
      }
      case "new-shell": {
        // Open the advertised address for this server, even after in-page navigation.
        const origin = new URL(current.currentURL).origin;
        const url = groups.flatMap(g => g.urls).find(url => new URL(url).origin === origin);
        preview.open(current.request.sessionId, url ?? origin, { external: current.request.external });
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
    if (!editing && !(compact && expanded) && !(e.target instanceof Element && e.target.closest(".tools")) && page && "commentKeyShortcut" in page) page.commentKeyShortcut(e);
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
      <h2 class="mr-2 self-stretch content-center text-lg text-ink max-sm:sr-only">Preview</h2>
      <Button icon aria-label="Back" title="Back" disabled={!current.canGoBackInFrame && !current.backStack.length} onclick={goBack}><Icon name="arrow-left" size={16} /></Button>
      <button bind:this={addressButton} type="button" class="address-bar no-drag" aria-label="Edit preview address"
        aria-keyshortcuts="Meta+l" aria-haspopup="dialog" title={current.currentURL} onclick={editAddress}>
        <Icon name="globe" size={15} />
        <span class="min-w-0 flex-1 truncate text-left text-ink">{current.currentURL}</span>
        <kbd class="shrink-0 text-xs max-sm:hidden">⌘L</kbd>
      </button>
      <span aria-label="Preview viewport" class="shrink-0 font-mono text-xs tabular-nums text-faint">{Math.round(dimensions.width || 0)} × {Math.round(dimensions.height || 0)} px</span>
      <Button icon size="sm" aria-label="Developer tools" selected={toolsOpen} aria-expanded={toolsOpen} aria-keyshortcuts="Meta+Alt+i" title="Developer tools · ⌘⌥I" onclick={toggleTools}><Icon name="square-terminal" size={16}/></Button>
    </div>
  {/snippet}
  <div inert={editing}>
    <Modal title="Preview" headerContent={header} visible={preview.visible} onClose={hidePreview} onkey={onModalKey} fullscreen bodyClass="min-w-0 overflow-hidden">
      <div class="preview-workspace relative flex h-full min-h-0 overflow-hidden">
        {#if compact}
          <button type="button" tabindex="-1" aria-label="Dismiss page sidebar" inert={!expanded} aria-hidden={!expanded}
            class="sidebar-scrim" class:expanded onclick={closeSidebar}></button>
        {/if}
        <aside id="preview-sidebar" aria-label="Preview pages" inert={!expanded} aria-hidden={!expanded}
          class="page-sidebar" style:--pages-width={sidebarWidth + "px"} class:compact class:expanded>
          <div role="tablist" aria-label="Open pages" aria-orientation="vertical" class="sidebar-content flex flex-col gap-1">
            {#each preview.tabs as tab, i (tab.id)}
              {@const title = label(tab)}
              {@const address = subtitle(tab)}
              <div role="presentation" class="page-row group" class:selected={current === tab}>
                <button type="button" id={tabId(tab)} role="tab" aria-label={title} aria-selected={current === tab} aria-describedby={address !== title ? tabId(tab) + "-address" : undefined} aria-controls={'preview-page-' + tab.id}
                  tabindex={current === tab ? 0 : -1} title={title === address ? tab.currentURL : title + "\n" + tab.currentURL}
                  class="page-tab" onclick={() => select(i)} onkeydown={(e) => onTabKey(e, i)}>
                  <PreviewTabIcon src={tab.favicon} />
                  <span class="min-w-0">
                    <span class="page-title">{title}</span>
                    {#if address !== title}<span id={tabId(tab) + "-address"} class="page-address">{address}</span>{/if}
                  </span>
                </button>
                <Button size="xs" icon aria-label={`Close ${title}`} title="Close tab · ⌘W" tabindex={current === tab ? 0 : -1}
                  class="text-faint hover:text-ink {current === tab ? '' : 'opacity-0 group-hover:opacity-100 group-focus-within:opacity-100'}" onclick={() => closeTab(i)}>
                  <Icon name="x" size={12} />
                </Button>
              </div>
            {/each}
          </div>
          {#if expanded}<PreviewSplitter label="Resize page sidebar" min={minSidebar} max={maxSidebar} value={sidebarWidth}
            onresize={size => customSidebar = size} onreset={() => customSidebar = null}/>{/if}
        </aside>
        <div bind:clientHeight={splitHeight} bind:clientWidth={splitWidth} class="flex min-w-0 flex-1 overflow-hidden bg-canvas" inert={compact && expanded}>
          <div class="flex min-w-0 shrink-0 flex-col overflow-hidden" style:width={splitWidth ? pageWidth + "px" : "100%"}>
            <div bind:clientHeight={pageHeight} class="relative min-h-0 shrink-0 overflow-hidden"
              style:height={splitHeight ? stageHeight + "px" : "calc(100% - 12px)"}>
          <!-- Keep parked frames laid out. display:none resets iframe scroll in Chromium. -->
          {#each preview.tabs as tab (tab.id)}
            <div role="tabpanel" tabindex="-1" id={'preview-page-' + tab.id} aria-labelledby={tabId(tab)} class="absolute inset-0" style:visibility={tab === current ? undefined : "hidden"} aria-hidden={tab !== current} inert={tab !== current}>
              {#if tab.visited}
                {#key tab.request}
                  {#if tab.request.external}
                    <ExternalPreview {tab} active={preview.visible && current === tab && !editing && !(compact && expanded)}
                      onNewTab={url => preview.openLink(tab.request.sessionId, url, servers)} bind:this={pages[tab.id]} />
                  {:else}
                    <SessionPreview {tab} active={preview.visible && current === tab && !editing && !(compact && expanded)}
                      onPresetChange={() => { customWidth = null; customHeight = null; }} bind:this={pages[tab.id]} />
                  {/if}
                {/key}
              {/if}
            </div>
          {/each}
            </div>
            <PreviewSplitter orientation="horizontal" label="Resize preview height" min={minHeight} max={maxHeight} value={stageHeight}
              onresize={size => { responsive(); customHeight = size; }} onreset={() => { responsive(); customHeight = null; }}/>
          </div>
          <PreviewSplitter label="Resize preview viewport" min={minPage} max={maxPage} value={pageWidth}
            onresize={size => { responsive(); customWidth = size; }} onreset={() => { responsive(); customWidth = null; }}/>
          {#if toolsOpen && preview.visible && !editing}
            <div class="min-w-0 flex-1 overflow-hidden">
              {#key current.id}
                <PreviewDeveloperTools id={current.id} external={!!current.request.external} {target} close={toggleTools}/>
              {/key}
            </div>
          {/if}
        </div>
      </div>
    </Modal>
  </div>
  {#if editing}
      {#snippet placeLabel(place: PreviewPlace)}
        {#if place.app}<span class="text-xs text-faint">{place.app}</span>{/if}
        <span class="truncate">{place.app === undefined ? devUrlLabel(place.address) : place.address}</span>
      {/snippet}
    {#snippet addressForm()}
      <form onsubmit={goToAddress} class="flex min-w-0 items-center gap-2">
        <Icon name="globe" size={17} class="text-faint" />
        <input bind:this={addressInput} bind:value={draft} role="combobox" aria-autocomplete="list" aria-expanded={recent.length > 0} aria-controls="preview-recent-list"
          aria-activedescendant={highlightId} aria-label="Preview address" aria-invalid={!!addressError}
          aria-describedby={addressError ? "preview-address-error" : undefined}
          oninput={() => { addressError = ""; highlighted = undefined; }} type="text" inputmode="url" autocomplete="off" autocapitalize="off"
          spellcheck="false" placeholder="URL or path, e.g. /cp" class="min-w-0 flex-1 rounded-md border border-edge bg-canvas px-3 py-2 text-ink" />
        <Button icon size="sm" type="button" selected={bookmarked} disabled={!placesKey || !currentPlace} aria-pressed={bookmarked}
          aria-label={bookmarked ? "Remove current bookmark" : "Bookmark current page"} title={bookmarked ? "Remove current bookmark" : "Bookmark current page"} onclick={toggleBookmark}><Icon name="star" size={16}/></Button>
        <Button type="submit" variant="primary" disabled={!draft.trim()}>Go<Icon name="arrow-right" size={14} /></Button>
      </form>
    {/snippet}
    <Modal title="Open address" headerContent={addressForm} onClose={dismissAddress} onkey={addressKey} width="580px">
      {#if addressError}<p id="preview-address-error" class="mt-2 text-sm text-warn" role="alert">{addressError}</p>{/if}
      <nav aria-label="Dev servers" class="grid gap-3">
        {#each groups.filter(g => g.urls.length) as group (group.name)}
          <div role="group" aria-label={group.label || "Dev servers"} class="grid min-w-0 max-w-full gap-0.5">
            <h3 class="px-1.5 text-sm font-medium text-faint">{group.label || "Dev servers"}</h3>
            <div class="flex min-w-0 flex-wrap gap-1">
              {#each group.urls as url (url)}
                {@const action = `Open ${devUrlLabel(url)} in a new tab`}
                <Button size="xs" class="max-w-full font-mono!" title={action} aria-label={action} onclick={() => openServer(url)}>
                  <Icon name="plus" size={12} /><span class="truncate">{devUrlLabel(url)}</span>
                </Button>
              {/each}
            </div>
          </div>
        {:else}
          <p class="text-sm text-faint">This session has no running dev servers.</p>
        {/each}
      </nav>

      <section class="mt-3" aria-label="Recent pages" hidden={!recent.length}>
        <header class="flex items-center justify-between px-1.5">
          <h3 class="text-sm font-medium text-faint">Recent pages</h3>
          <Button size="xs" aria-label="Clear recent pages" onclick={() => { highlighted = undefined; updatePlaces({ ...places, recent: [] }); }}>Clear</Button>
        </header>
        <div class="flex items-start gap-1">
          <div id="preview-recent-list" role="listbox" aria-label="Recent pages" class="grid min-w-0 flex-1 gap-0.5">
            {#each recent as place, i}
              {@const url = addressOf(place)}
              <button type="button" role="option" id={"preview-recent-" + i} aria-selected={highlighted === place}
                class="saved-page h-7" disabled={!url} title={url ?? "This dev server is unavailable"} onclick={() => url && visitSaved(url)}>
                <Icon name="history" size={13}/>{@render placeLabel(place)}
              </button>
            {/each}
          </div>
          <div class="grid gap-0.5">
            {#each recent as place}
              <Button size="sm" icon aria-label={'Remove recent page ' + place.address} title="Remove from recent pages"
                onclick={() => removeRecent(place)}><Icon name="x" size={12}/></Button>
            {/each}
          </div>
        </div>
      </section>
      {#if bookmarks.length}
        <section class="mt-3" aria-label="Bookmarks">
          <h3 class="px-1.5 text-sm font-medium text-faint">Bookmarks</h3>
          {#each bookmarks as place}
            {@const url = addressOf(place)}
            <div class="flex min-w-0 items-center">
              <button class="saved-page flex-1" disabled={!url} title={url ?? "This dev server is unavailable"} onclick={() => url && visitSaved(url)}>
                <Icon name="star" size={13}/>{@render placeLabel(place)}
              </button>
              <Button size="xs" icon aria-label={'Remove bookmark for ' + place.address} title="Remove bookmark"
                onclick={() => updatePlaces({ ...places, bookmarks: places.bookmarks.filter(p => p !== place) })}><Icon name="x" size={12}/></Button>
            </div>
          {/each}
        </section>
      {/if}
    </Modal>
  {/if}
{/if}

<style>
  .preview-workspace:has(:global(.sash.dragging)) :global(iframe) { pointer-events:none; }
  .saved-page {display:flex;align-items:center;gap:8px;min-width:0;padding:5px 6px;border-radius:5px;text-align:left;cursor:pointer}
  .saved-page:hover:enabled,.saved-page[aria-selected=true] {background:var(--color-sel);color:var(--color-ink)}
  .saved-page:disabled {opacity:.5;cursor:default}
  .address-bar {
    display: flex;
    align-items: center;
    gap: .625rem;
    flex: 1 1 0;
    max-width: 40rem;
    min-width: 0;
    margin-inline: auto;
    padding: .375rem .75rem;
    border: 1px solid var(--color-edge);
    border-radius: 8px;
    background: var(--color-canvas);
    color: var(--color-faint);
    cursor: text;
  }
  .address-bar:hover, .address-bar:focus-visible { border-color: var(--color-accent); }
  .page-sidebar {
    display:flex;
    position: relative;
    z-index: 10;
    flex: 0 0 auto;
    width: min(var(--pages-width), 100%);
    /* Reserve dock space once per toggle so the framed app reflows only once. */
    margin-inline-end: calc(-1 * var(--pages-width));
    background: var(--color-panel);
    transform: translateX(-100%);
    transition: transform 180ms ease-out;
  }
  .page-sidebar.expanded { margin-inline-end: 0; transform: translateX(0); }
  .sidebar-content {
    flex:1; min-width:0;
    height: 100%;
    padding: .5rem;
    overflow-y: auto;

  }
  .page-row {
    display: flex;
    align-items: center;
    padding-inline-end: 4px;
    border: 1px solid transparent;
    border-radius: 7px;
    color: var(--color-ink-2);
  }
  .page-row:hover { background: var(--color-sel); color: var(--color-ink); }
  .page-row.selected {
    color: var(--color-ink);
    background: color-mix(in srgb, var(--color-accent-fill) 72%, var(--color-panel));
    border-color: color-mix(in srgb, var(--color-accent) 23%, var(--color-panel));
  }
  .page-tab {
    display: flex;
    flex: 1;
    align-items: center;
    gap: 8px;
    min-width: 0;
    min-height: 42px;
    padding: 7px 4px 7px 8px;
    border-radius: 6px;
    text-align: start;
  }
  .page-title {
    display: -webkit-box;
    line-clamp: 2;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    overflow-wrap: anywhere;
    font-size: var(--text-base);
    line-height: 1.3;
    font-weight: 500;
  }
  .selected .page-title { font-weight: 600; }
  .page-address {
    display: block;
    margin-top: 2px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--color-faint);
    font-size: var(--text-sm);
    line-height: 1.3;
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
