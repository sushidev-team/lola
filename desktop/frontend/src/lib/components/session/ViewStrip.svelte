<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  // Tabs share a scrolling row. The add and panel controls stay within reach.
  import { tick } from "svelte";
  import { flip } from "svelte/animate";
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { OVERVIEW_PANEL, CHANGES_PANEL, sessionPanelTitle, widgetTabLabel } from "$lib/sessionpanels";
  import { groupOf } from "$lib/docklayout";
  import { terms, isChangesTab } from "$lib/terms.svelte";
  import { feedback } from "$lib/feedback.svelte";
  import { changes } from "$lib/changes.svelte";
  import { sshNeedsHuman } from "$lib/ssh";
  import { isAgentless, changesOffered } from "$lib/sessionview";
  import Button from "../Button.svelte";
  import MenuItem from "../MenuItem.svelte";

  let { session, changesInSide = false }: { session: SessionInfo; changesInSide?: boolean } = $props();

  const id = $derived(session.id);
  const view = $derived(ssh.view(id));
  const parent = $derived(session.agentTabParent || id);
  const members = $derived(ssh.stripMembers(session));
  const tabs = $derived(ssh.stripTabs(session, members, changesInSide));
  const views = $derived(tabs.views);
  const hosts = $derived(tabs.hosts);
  const terminals = $derived(tabs.terminals);
  type Tab = (typeof views)[number];
  const all = $derived([...views, ...hosts, ...terminals]);
  const selected = $derived(ssh.currentCell(session));
  const current = $derived(!isAgentless(session) && (selected === "terminal" || terms.isDevTab(selected) || (changesInSide && selected === "changes")) ? "conversation" : selected);
  const activeKey = $derived(id + ":" + current);
  const draftCount = $derived(feedback.count(id));
  const stat = $derived(changes.of(id));
  const waitsElsewhere = $derived(sshNeedsHuman(session) && current !== "conversation");

  const widgets = $derived(view.knownWidgets.length);
  const widgetDot = $derived(view.dockUnseen.size > 0);

  let tabList = $state<HTMLElement>();
  $effect(() => {
    const tab = refs[activeKey];
    if (!tab || !tabList) return;
    const list = tabList;
    const reveal = () => {
      const row = list.getBoundingClientRect();
      const rect = tab.getBoundingClientRect();
      if (rect.right > row.right) list.scrollLeft += rect.right - row.right;
      else if (rect.left < row.left) list.scrollLeft -= row.left - rect.left;
    };
    reveal();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(reveal);
    observer.observe(list);
    return () => observer.disconnect();
  });

  let refs = $state<Record<string, HTMLElement | undefined>>({});
  async function pick(t: Tab, focus = false) {
    ssh.selectStripTab(id, t);
    if (focus) {
      await tick();
      refs[t.key]?.focus();
    }
  }
  function cellKey(e: KeyboardEvent, c: Tab) {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    e.stopPropagation();
    const ring = all;
    const at = ring.findIndex((x) => x.key === c.key);
    const next = ring[(at + (e.key === "ArrowRight" ? 1 : -1) + ring.length) % ring.length];
    void pick(next, true);
  }

  // --- drag to sort ---------------------------------------------------------------
  //
  // Pointer events, not HTML5 drag-and-drop: WebKit refuses a native drag from a button, and the
  // native drag image paints badly over the WebGL canvas. The pointer is captured only past the
  // slop, so an ordinary click still reaches the tab.
  const dragSlop = 5;
  const flipMs = 140;
  let dragging = $state(-1);
  let dragFrom = -1;
  let dragX = 0;
  let swapLock = false;
  let tabEls = $state<(HTMLElement | undefined)[]>([]);

  function tabIndexAt(x: number, count: number): number {
    for (let i = 0; i < count; i++) {
      const r = tabEls[i]?.getBoundingClientRect();
      if (r && x < r.left + r.width / 2) return i;
    }
    return count - 1;
  }
  function dragStart(i: number, e: PointerEvent) {
    if (e.button !== 0 || tabMenu) return;
    dragFrom = i;
    dragX = e.clientX;
  }
  function dragMove(e: PointerEvent) {
    if (dragFrom < 0) return;
    if (dragging < 0) {
      if (Math.abs(e.clientX - dragX) < dragSlop) return;
      dragging = dragFrom;
      (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    }
    // A swap is locked for the length of the animation: the rects report mid-flight positions,
    // and re-reading them straight away would swap the pair straight back, forever.
    if (swapLock) return;
    const to = tabIndexAt(e.clientX, terminals.length);
    if (to === dragFrom) return;
    const fromTab = terminals[dragFrom], toTab = terminals[to];
    if (!fromTab || !toTab || fromTab.owner !== toTab.owner) return;
    const shells = terms.shellsFor(fromTab.owner);
    terms.moveTab(fromTab.owner, shells.indexOf(fromTab.cell.name), shells.indexOf(toTab.cell.name));
    dragFrom = to;
    dragging = to;
    swapLock = true;
    setTimeout(() => (swapLock = false), flipMs);
  }
  function dragEnd(e: PointerEvent) {
    const el = e.currentTarget as HTMLElement;
    if (el.hasPointerCapture?.(e.pointerId)) el.releasePointerCapture(e.pointerId);
    dragFrom = -1;
    dragging = -1;
    swapLock = false;
  }

  // --- a shell tab's menu ---------------------------------------------------------

  type TabMenu = { owner: string; tab: string; x: number; y: number; mode: "menu" | "rename" };
  let tabMenu = $state<TabMenu | null>(null);
  let menuEl = $state<HTMLElement | null>(null);
  let renameEl = $state<HTMLInputElement | null>(null);
  let renameText = $state("");
  $effect(() => {
    const m = tabMenu;
    const node = menuEl;
    if (!m || !node) return;
    const { width: w, height: h } = node.getBoundingClientRect();
    node.style.left = `${Math.max(4, Math.min(m.x, window.innerWidth - w - 4))}px`;
    node.style.top = `${Math.max(4, Math.min(m.y, window.innerHeight - h - 4))}px`;
  });
  $effect(() => {
    if (tabMenu?.mode === "rename") renameEl?.select();
  });
  function openTabMenu(owner: string, tab: string, e: MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    tabMenu = { owner, tab, x: e.clientX, y: e.clientY, mode: "menu" };
  }
  function startRename(owner: string, tab: string, at?: MouseEvent) {
    renameText = terms.labelFor(owner, tab);
    tabMenu = { owner, tab, x: at ? at.clientX : (tabMenu?.x ?? 0), y: at ? at.clientY : (tabMenu?.y ?? 0), mode: "rename" };
  }
  function commitRename() {
    const menu = tabMenu;
    tabMenu = null;
    if (menu) terms.rename(menu.owner, menu.tab, renameText);
  }

  // --- add and panel menus ---------------------------------------------------------

  const availablePanels = $derived([
    ...[OVERVIEW_PANEL, ...(changesOffered(session) ? [CHANGES_PANEL] : [])].map(id => ({ id, title: sessionPanelTitle(id), label: sessionPanelTitle(id) })),
    ...view.items.flatMap(i => i.element?.kind === 'widget' ? [{ id: i.element.id, title: i.element.title || 'Widget', label: widgetTabLabel(i.element.title || '') }] : []),
  ]);
  function togglePanel(panel: string) {
    if (groupOf(view.dock, panel) && !view.dockHidden) {
      if (panel === CHANGES_PANEL && isChangesTab(terms.activeTab(id))) terms.toggleDiff(id);
      view.closeWidget(panel);
    } else view.openWidget(panel);
    panelAt = null;
    panelButton?.focus();
  }
  let panelMenu = $state<HTMLDivElement | null>(null);
  let addMenu = $state<HTMLDivElement | null>(null);
  $effect(() => {
    const menu = panelAt ? panelMenu : addAt ? addMenu : null;
    menu?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();
  });
  function menuKey(event: KeyboardEvent) {
    event.stopPropagation();
    const items = [...(event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('button:not(:disabled)')];
    if (!items.length || !['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const at = items.indexOf(document.activeElement as HTMLButtonElement);
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (at + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
    items[next].focus();
  }
  let panelButton = $state<HTMLButtonElement | null>(null);
  let panelAt = $state<{ x: number; y: number } | null>(null);
  $effect(() => {
    if (!panelAt && !addAt && !tabMenu) return;
    const escape = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.preventDefault();
      e.stopImmediatePropagation();
      if (panelAt) panelButton?.focus();
      else if (addAt) addTrigger?.focus();
      panelAt = null;
      addAt = null;
      tabMenu = null;
    };
    window.addEventListener("keydown", escape, true);
    return () => window.removeEventListener("keydown", escape, true);
  });
  let addAt = $state<{ x: number; y: number } | null>(null);
  function portal(node: HTMLDivElement) {
    document.body.appendChild(node);
    return { destroy: () => node.remove() };
  }
  let addTrigger: HTMLElement | null = null;
  function under(e: MouseEvent) {
    addTrigger = e.currentTarget as HTMLElement;
    const r = addTrigger.getBoundingClientRect();
    return { x: Math.max(4, Math.min(r.left, window.innerWidth - 212)), y: r.bottom + 4 };
  }
  function run(fn: () => void) {
    addAt = null;
    fn();
  }
  let starting = $state(false);
  async function newAgent() {
    if (starting) return;
    const source = id;
    starting = true;
    try {
      const next = await store.openManual({ location: "agent-tab", parentSession: parent, project: session.project, branch: "", agent: true, agentKind: members[0].agent });
      if (next?.sessionId && nav.focusedTerm === source) {
        nav.openSession(next.sessionId);
        ssh.selectCell(next.sessionId, { key: "conversation", kind: "conversation" });
      }
    } finally {
      starting = false;
    }
  }
  const cellText = "relative inline-flex h-8 shrink-0 items-center gap-1.5 whitespace-nowrap transition-colors";
</script>

{#snippet underline(on: boolean)}
  {#if on}<span class="absolute inset-x-0 bottom-0 h-0.5 bg-ink" aria-hidden="true"></span>{/if}
{/snippet}

{#snippet addItems()}
  {#if session.canOpenAgentTab}
    <MenuItem disabled={starting} title="Start another agent in this checkout" onclick={() => run(newAgent)}>New agent conversation</MenuItem>
  {/if}
  <MenuItem trailing="⌘T" disabled={!session.worktree} title={session.worktree ? undefined : "This session has no folder"} onclick={() => run(() => terms.newShell(id, session.worktree))}>New shell</MenuItem>
  {#if session.location !== "temporary"}
    <MenuItem onclick={() => run(() => ssh.connectHost(id))}>Connect to host…</MenuItem>
  {/if}
  {#if session.devCommands?.length}
    <MenuItem onclick={() => run(() => store.dev(id, !session.devActive))}>{session.devActive ? "Stop dev commands" : "Dev commands"}</MenuItem>
  {/if}
{/snippet}

<div class="relative z-10 flex h-8 min-w-0 items-stretch gap-2 border-b border-edge/60 bg-canvas px-3 select-none" data-view-strip>
  <div bind:this={tabList} role="tablist" aria-label="Session views" class="flex min-w-0 items-stretch gap-3 overflow-x-auto overscroll-x-contain [scrollbar-width:none]">
    <div class="flex shrink-0 items-stretch gap-4">
      {#each views as c (c.key)}
        {@const on = activeKey === c.key}
        <div class="group flex shrink-0 items-center gap-0.5">
          <button
            bind:this={refs[c.key]}
            type="button"
            role="tab"
            aria-selected={on}
            tabindex={on ? 0 : -1}
            class="{cellText} {on ? 'font-medium text-ink' : 'text-faint hover:text-ink'}"
            data-strip-cell={c.cell.key}
            onclick={() => pick(c)}
            onkeydown={(e) => cellKey(e, c)}
          >
            {c.label}
            {#if c.owner === id && c.cell.kind === "conversation" && waitsElsewhere}<span class="text-orange" aria-label="A card is waiting"><Icon name="circle-alert" size={14} /></span>{/if}
            {#if c.cell.kind === "changes" && stat && stat.add + stat.del > 0}<span class="num text-sm font-normal text-faint" data-changes-stat>+{stat.add} −{stat.del}</span>{/if}
            {#if c.cell.kind === "changes" && draftCount > 0}<span class="num text-sm font-normal text-faint"><span aria-hidden="true"><Icon name="circle-dot" size={14} /></span> {draftCount}</span>{/if}
            {@render underline(on)}
          </button>
          {#if c.cell.kind === "conversation" && c.owner !== parent}
            <Button variant="bare" size="xs" icon class="h-4! w-4! opacity-0 group-hover:opacity-100 focus-visible:opacity-100" aria-label={`Close ${c.label}`} onclick={() => ssh.closeConversation(c.owner)}><Icon name="x" size={14} /></Button>
          {/if}
        </div>
      {/each}
    </div>

    {#if hosts.length || terminals.length}
      <div class="flex shrink-0 items-stretch gap-3 font-mono text-sm" data-strip-machines>
        {#each hosts as c (c.key)}
          {@const on = activeKey === c.key}
          <button
            bind:this={refs[c.key]}
            type="button"
            role="tab"
            aria-selected={on}
            tabindex={on ? 0 : -1}
            class="{cellText} {on ? 'text-ink' : 'text-faint hover:text-ink'}"
            data-strip-cell={c.cell.key}
            onclick={() => pick(c)}
            onkeydown={(e) => cellKey(e, c)}
          >
            {c.label}
            {@render underline(on)}
          </button>
        {/each}
        {#each terminals as c, i (c.key)}
            {@const sh = c.cell.name}
            {@const on = activeKey === c.key}
            <div
              bind:this={tabEls[i]}
              animate:flip={{ duration: dragging >= 0 ? flipMs : 0 }}
              role="group"
              class="group relative flex h-8 shrink-0 items-center gap-0.5 {on ? 'text-ink' : 'text-faint hover:text-ink'}"
              class:opacity-60={dragging === i}
              style="touch-action:none"
              onpointerdown={(e) => dragStart(i, e)}
              onpointermove={dragMove}
              onpointerup={dragEnd}
              onpointercancel={dragEnd}
              oncontextmenu={(e) => openTabMenu(c.owner, sh, e)}
            >
              <button
                bind:this={refs[c.key]}
                type="button"
                role="tab"
                aria-selected={on}
                tabindex={on ? 0 : -1}
                class="inline-flex h-8 max-w-56 items-center truncate whitespace-nowrap"
                data-strip-cell={c.cell.key}
                onclick={() => pick(c)}
                ondblclick={(e) => startRename(c.owner, sh, e)}
                onkeydown={(e) => cellKey(e, c)}
              >
                {c.label}
              </button>
              <Button
                variant="bare"
                size="xs"
                icon
                class="h-4! w-4! opacity-0 transition-[opacity,color] group-hover:opacity-100 hover:text-bad focus-visible:opacity-100"
                title={terms.isReviewTab(sh) ? "close the review pane" : terms.isDevTab(sh) ? "stop this session's dev processes" : "close shell"}
                aria-label={terms.isReviewTab(sh) ? "close review" : terms.isDevTab(sh) ? "stop dev" : "close shell"}
                onclick={() => terms.closeShell(c.owner, sh)}><Icon name="x" size={14} /></Button
              >
              {@render underline(on)}
            </div>
        {/each}
      </div>
    {/if}
  </div>

  <div class="flex shrink-0 items-center">
    <Button icon size="xs" aria-label="Add tab" aria-haspopup="menu" aria-expanded={!!addAt} title="Add tab" onclick={(e) => (addAt = under(e))}><Icon name="plus" size={14} /></Button>
  </div>
  <div class="ml-auto flex shrink-0 items-center gap-2">
    {#if changesInSide}<button bind:this={panelButton} type="button" class="grid size-7 place-items-center rounded-sm text-faint hover:bg-sel hover:text-ink focus-visible:outline-2 focus-visible:outline-accent" aria-label="Panels" title="Show or hide panels" aria-haspopup="menu" aria-expanded={!!panelAt} onclick={(e) => { const r = e.currentTarget.getBoundingClientRect(); panelAt = panelAt ? null : { x: Math.max(4, r.right - 224), y: r.bottom + 4 }; }}>
      <Icon name="menu" class="size-4" />
    </button>{/if}
    {#if widgets > 0 && !changesInSide}
      <Button size="xs" class="num" title="Show or hide widgets (w)" data-dock-opener aria-label="Widgets {widgets}" onclick={() => ssh.dockKey(session)}>
        Widgets {widgets}{#if widgetDot}<span class="text-magenta" aria-label="A new widget"><Icon name="circle-dot" size={14} /></span>{/if}
      </Button>
    {/if}
  </div>
</div>

{#if addAt || tabMenu || panelAt}
<div use:portal>
{#if addAt}
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => (addAt = null)} oncontextmenu={(e) => { e.preventDefault(); addAt = null; }}></div>
  <div class="fixed z-50 w-52 max-w-[calc(100vw-8px)] rounded-lg border border-edge bg-panel p-1 shadow-xl" style="left:{addAt.x}px;top:{addAt.y}px" role="menu" bind:this={addMenu} tabindex="-1" onkeydown={menuKey}>
    {@render addItems()}
  </div>
{/if}

{#if tabMenu}
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => (tabMenu = null)} oncontextmenu={(e) => { e.preventDefault(); tabMenu = null; }}></div>
  {#if tabMenu.mode === "rename"}
    <form
      bind:this={menuEl}
      class="fixed z-50 w-64 rounded-lg border border-edge bg-panel p-3 shadow-xl"
      style="left:{tabMenu.x}px;top:{tabMenu.y}px"
      onsubmit={(e) => {
        e.preventDefault();
        commitRename();
      }}
    >
      <label class="label mb-1.5 block text-faint" for="tab-rename">Tab name</label>
      <input
        id="tab-rename"
        bind:this={renameEl}
        bind:value={renameText}
        maxlength="40"
        spellcheck="false"
        onkeydown={(e) => {
          if (e.key !== "Escape") return;
          e.preventDefault();
          e.stopPropagation(); // abandon the edit, never leave the session
          tabMenu = null;
        }}
        class="h-8 w-full rounded-md border border-edge bg-canvas px-2.5 text-ink focus:border-accent focus-visible:outline-none!"
      />
      <div class="mt-1.5 text-sm text-faint">Empty resets it to the default name.</div>
      <div class="mt-3 flex justify-end gap-2">
        <Button size="xs" onclick={() => (tabMenu = null)}>Cancel</Button>
        <Button variant="primary" size="xs" type="submit">Save</Button>
      </div>
    </form>
  {:else}
    <div bind:this={menuEl} class="fixed z-50 min-w-[10rem] rounded-lg border border-edge bg-panel p-1 shadow-xl" style="left:{tabMenu.x}px;top:{tabMenu.y}px" role="menu">
      <div class="label truncate px-2 pt-1 pb-1.5 text-faint">{terms.labelFor(tabMenu.owner, tabMenu.tab)}</div>
      <MenuItem icon="✎" onclick={() => tabMenu && startRename(tabMenu.owner, tabMenu.tab)}>Rename…</MenuItem>
      <div class="my-1 h-px bg-edge/60"></div>
      <MenuItem
        variant="danger"
        icon="×"
        onclick={() => {
          const menu = tabMenu;
          tabMenu = null;
          if (menu) terms.closeShell(menu.owner, menu.tab);
        }}
      >
        {terms.isReviewTab(tabMenu.tab) ? "Close review" : "Close shell"}
      </MenuItem>
    </div>
  {/if}
{/if}

{#if panelAt}
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => panelAt = null}></div>
  <div class="fixed z-50 w-56 rounded-md border border-edge bg-panel p-1 shadow-xl" style="left:{panelAt.x}px;top:{panelAt.y}px" role="menu" aria-label="Panels" bind:this={panelMenu} tabindex="-1" onkeydown={menuKey}>
    {#each availablePanels as panel (panel.id)}
      {@const shown = !!groupOf(view.dock, panel.id) && !view.dockHidden}
      <MenuItem role="menuitemcheckbox" aria-label={panel.title} aria-checked={shown} icon={shown ? '✓' : ''} title={panel.title} onclick={() => togglePanel(panel.id)}>{panel.label}</MenuItem>
    {/each}
  </div>
{/if}
</div>
{/if}
