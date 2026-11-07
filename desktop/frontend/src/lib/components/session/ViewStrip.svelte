<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  // The strip under the top bar. Three groups, and only the middle one can move or scroll:
  //
  //   Conversation  Terminal  Changes     $ prod-api  zsh  review  +            Widgets 2 •
  //   └ fixed, never moves ───────────┘   └ mono, machine things, past a fade ┘         └ fixed ┘
  //
  // `+` sits right after the tabs it adds to, and keeps its place when they scroll past the fade.
  //
  // The first group's position is what spatial memory rides on, so a host arriving must not push
  // it. A real tablist with arrow keys. It is hand-rolled rather than <Button> for the same reason
  // Tabs.svelte is: a role="tab" with a roving tabindex and a drawn underline is not a button.
  //
  // Colour is never the only signal for the active cell: the underline carries it, in ink.
  //
  // The second group keeps what the old strip's shell tabs had: drag to sort, double-click or
  // right-click to rename, × on hover or focus. A host's `$` cell has no ×, because removing a
  // host and its record is a decision that belongs to the host bar, behind a confirmation.
  import { tick } from "svelte";
  import { flip } from "svelte/animate";
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { OVERVIEW_PANEL, CHANGES_PANEL, sessionPanelTitle, widgetTabLabel } from "$lib/sessionpanels";
  import { groupOf } from "$lib/docklayout";
  import { terms, isChangesTab } from "$lib/terms.svelte";
  import { feedback } from "$lib/feedback.svelte";
  import { changes } from "$lib/changes.svelte";
  import { sshNeedsHuman } from "$lib/ssh";
  import { isAgentless, changesOffered, stripCells, type StripCell } from "$lib/sessionview";
  import { widthOf } from "./measure";
  import Button from "../Button.svelte";
  import MenuItem from "../MenuItem.svelte";

  let { session, changesInSide = false }: { session: SessionInfo; changesInSide?: boolean } = $props();

  const id = $derived(session.id);
  const view = $derived(ssh.view(id));
  const shells = $derived(terms.shellsFor(id));
  const cells = $derived(stripCells(session, shells));
  const selected = $derived(ssh.currentCell(session));
  const current = $derived(terms.isDevTab(selected) ? "terminal" : changesInSide && selected === "changes" ? "conversation" : selected);
  const views = $derived(cells.views.filter(c => !changesInSide || c.kind !== 'changes'));
  const all = $derived([...views, ...cells.machines]);
  // Visible terminals retain their order; drag indices must account for hidden dev tabs.
  const terminals = $derived(cells.machines.filter((c): c is Extract<StripCell, { name: string }> => c.kind !== "host"));
  const draftCount = $derived(feedback.count(id));
  const stat = $derived(changes.of(id));
  const waitsElsewhere = $derived(sshNeedsHuman(session) && current !== "conversation");

  const widgets = $derived(view.knownWidgets.length);
  const widgetDot = $derived(view.dockUnseen.size > 0);

  let width = $state(0);
  // 0 means not measured yet, which is the wide layout. Under 900 of main column the second
  // group folds into one menu, so the first group never gives up its place.
  const narrow = $derived(width > 0 && width < 876);

  function viewLabel(c: StripCell): string {
    if (c.kind === "conversation") return "Conversation";
    if (c.kind === "changes") return "Changes";
    return isAgentless(session) ? "Shell" : "Terminal";
  }
  function machineLabel(c: StripCell): string {
    if (c.kind === "host") return `$ ${c.label}`;
    if (c.kind === "dev") return `dev: ${terms.labelFor(id, c.name)}`;
    if (c.kind === "review") return "review";
    return c.kind === "shell" ? terms.labelFor(id, c.name) : "";
  }

  let refs = $state<Record<string, HTMLElement | undefined>>({});
  async function pick(c: StripCell, focus = false) {
    ssh.selectCell(id, c);
    if (focus) {
      await tick();
      refs[c.key]?.focus();
    }
  }
  function cellKey(e: KeyboardEvent, c: StripCell) {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    e.stopPropagation();
    const at = all.findIndex((x) => x.key === c.key);
    const next = all[(at + (e.key === "ArrowRight" ? 1 : -1) + all.length) % all.length];
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
    terms.moveTab(id, shells.indexOf(terminals[dragFrom].name), shells.indexOf(terminals[to].name));
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

  type TabMenu = { tab: string; x: number; y: number; mode: "menu" | "rename" };
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
  function openTabMenu(tab: string, e: MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    tabMenu = { tab, x: e.clientX, y: e.clientY, mode: "menu" };
  }
  function startRename(tab: string, at?: MouseEvent) {
    renameText = terms.labelFor(id, tab);
    tabMenu = { tab, x: at ? at.clientX : (tabMenu?.x ?? 0), y: at ? at.clientY : (tabMenu?.y ?? 0), mode: "rename" };
  }
  function commitRename() {
    const tab = tabMenu?.tab;
    tabMenu = null;
    if (tab) terms.rename(id, tab, renameText);
  }

  // --- + and ⋯ N ------------------------------------------------------------------

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
  $effect(() => { if (panelAt && panelMenu) panelMenu.querySelector<HTMLButtonElement>('button')?.focus(); });
  function panelKey(event: KeyboardEvent) {
    event.stopPropagation();
    const items = [...(panelMenu?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])];
    if (!items.length || !['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const at = items.indexOf(document.activeElement as HTMLButtonElement);
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (at + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
    items[next].focus();
  }
  let panelButton = $state<HTMLButtonElement | null>(null);
  let panelAt = $state<{ x: number; y: number } | null>(null);
  $effect(() => {
    if (!panelAt) return;
    const escape = (e: KeyboardEvent) => { if (e.key === "Escape") { e.preventDefault(); e.stopImmediatePropagation(); panelAt = null; panelButton?.focus(); } };
    window.addEventListener("keydown", escape, true);
    return () => window.removeEventListener("keydown", escape, true);
  });
  let addAt = $state<{ x: number; y: number } | null>(null);
  let foldAt = $state<{ x: number; y: number } | null>(null);
  function under(e: MouseEvent) {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    return { x: Math.max(4, Math.min(r.right - 208, window.innerWidth - 212)), y: r.bottom + 4 };
  }
  function run(fn: () => void) {
    addAt = null;
    foldAt = null;
    fn();
  }
  const cellText = "relative inline-flex h-8 shrink-0 items-center gap-1.5 whitespace-nowrap transition-colors";
</script>

{#snippet underline(on: boolean)}
  {#if on}<span class="absolute inset-x-0 -bottom-px h-0.5 bg-ink" aria-hidden="true"></span>{/if}
{/snippet}

{#snippet addItems()}
  <MenuItem trailing="⌘T" disabled={!session.worktree} title={session.worktree ? undefined : "This session has no folder"} onclick={() => run(() => terms.newShell(id, session.worktree))}>New shell</MenuItem>
  {#if session.location !== "temporary"}
    <MenuItem onclick={() => run(() => ssh.connectHost(id))}>Connect to host…</MenuItem>
  {/if}
  {#if session.devCommands?.length}
    <MenuItem onclick={() => run(() => store.dev(id, !session.devActive))}>{session.devActive ? "Stop dev commands" : "Dev commands"}</MenuItem>
  {/if}
{/snippet}

<div class="relative z-10 grid h-8 min-w-0 grid-cols-[auto_minmax(0,max-content)_auto_minmax(0,1fr)_auto] items-stretch gap-x-6 border-b border-edge/60 bg-canvas px-3 select-none" {@attach widthOf((w) => (width = w))} data-view-strip>
  <div role="tablist" aria-label="Session views" class="contents">
    <div class="flex items-stretch gap-4">
      {#each views as c (c.key)}
        {@const on = current === c.key}
        <button
          bind:this={refs[c.key]}
          type="button"
          role="tab"
          aria-selected={on}
          tabindex={on ? 0 : -1}
          class="{cellText} {on ? 'font-medium text-ink' : 'text-faint hover:text-ink'}"
          data-strip-cell={c.key}
          onclick={() => pick(c)}
          onkeydown={(e) => cellKey(e, c)}
        >
          {viewLabel(c)}
          {#if c.kind === "conversation" && waitsElsewhere}<span class="text-orange" aria-label="A card is waiting"><Icon name="circle-alert" size={14} /></span>{/if}
          {#if c.kind === "changes" && stat && !narrow && stat.add + stat.del > 0}<span class="num text-sm font-normal text-faint" data-changes-stat>+{stat.add} −{stat.del}</span>{/if}
          {#if c.kind === "changes" && draftCount > 0}<span class="num text-sm font-normal text-faint"><span aria-hidden="true"><Icon name="circle-dot" size={14} /></span> {draftCount}</span>{/if}
          {@render underline(on)}
        </button>
      {/each}
    </div>

    {#if narrow}
      <div class="flex items-stretch">
        {#if cells.machines.length}
          <Button size="xs" class="my-auto font-mono!" aria-haspopup="menu" aria-expanded={!!foldAt} aria-label="{cells.machines.length} more terminals" onclick={(e) => (foldAt = under(e))}><Icon name="ellipsis" size={14} /> <span class="num">{cells.machines.length}</span></Button>
        {/if}
      </div>
    {:else}
      <div class="flex min-w-0 items-stretch gap-3 overflow-x-auto font-mono text-sm [mask-image:linear-gradient(to_right,black_calc(100%-24px),transparent)] [scrollbar-width:none]" data-strip-machines>
        {#each cells.machines.filter((c) => c.kind === "host") as c (c.key)}
          {@const on = current === c.key}
          <button
            bind:this={refs[c.key]}
            type="button"
            role="tab"
            aria-selected={on}
            tabindex={on ? 0 : -1}
            class="{cellText} {on ? 'text-ink' : 'text-faint hover:text-ink'}"
            data-strip-cell={c.key}
            onclick={() => pick(c)}
            onkeydown={(e) => cellKey(e, c)}
          >
            {machineLabel(c)}
            {@render underline(on)}
          </button>
        {/each}
        {#each terminals as c, i (c.key)}
            {@const sh = c.name}
            {@const on = current === sh}
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
              oncontextmenu={(e) => openTabMenu(sh, e)}
            >
              <button
                bind:this={refs[c.key]}
                type="button"
                role="tab"
                aria-selected={on}
                tabindex={on ? 0 : -1}
                class="inline-flex h-8 max-w-56 items-center truncate whitespace-nowrap"
                data-strip-cell={c.key}
                onclick={() => pick(c)}
                ondblclick={(e) => startRename(sh, e)}
                onkeydown={(e) => cellKey(e, c)}
              >
                {machineLabel(c)}
              </button>
              <Button
                variant="bare"
                size="xs"
                icon
                class="h-4! w-4! opacity-0 transition-[opacity,color] group-hover:opacity-100 hover:text-bad focus-visible:opacity-100"
                title={terms.isReviewTab(sh) ? "close the review pane" : terms.isDevTab(sh) ? "stop this session's dev processes" : "close shell"}
                aria-label={terms.isReviewTab(sh) ? "close review" : terms.isDevTab(sh) ? "stop dev" : "close shell"}
                onclick={() => terms.closeShell(id, sh)}><Icon name="x" size={14} /></Button
              >
              {@render underline(on)}
            </div>
        {/each}
      </div>
    {/if}
  </div>

  <div class="-ml-4 flex items-center">
    <Button icon size="xs" aria-label="Add a terminal or a host" aria-haspopup="menu" aria-expanded={!!addAt} title="New shell, connect to a host" onclick={(e) => (addAt = under(e))}><Icon name="plus" size={14} /></Button>
  </div>
  <div aria-hidden="true"></div>
  <div class="flex items-center gap-2">
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

{#if addAt}
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => (addAt = null)} oncontextmenu={(e) => { e.preventDefault(); addAt = null; }}></div>
  <div class="fixed z-50 w-52 rounded-lg border border-edge bg-panel p-1 shadow-xl" style="left:{addAt.x}px;top:{addAt.y}px" role="menu">
    {@render addItems()}
  </div>
{/if}

{#if foldAt}
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => (foldAt = null)} oncontextmenu={(e) => { e.preventDefault(); foldAt = null; }}></div>
  <div class="fixed z-50 w-52 rounded-lg border border-edge bg-panel p-1 shadow-xl" style="left:{foldAt.x}px;top:{foldAt.y}px" role="menu">
    {#each cells.machines as c (c.key)}
      <MenuItem icon={current === c.key ? "•" : ""} onclick={() => run(() => void pick(c))}><span class="font-mono text-sm">{machineLabel(c)}</span></MenuItem>
    {/each}
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
      <div class="label truncate px-2 pt-1 pb-1.5 text-faint">{terms.labelFor(id, tabMenu.tab)}</div>
      <MenuItem icon="✎" onclick={() => startRename(tabMenu?.tab ?? "")}>Rename…</MenuItem>
      <div class="my-1 h-px bg-edge/60"></div>
      <MenuItem
        variant="danger"
        icon="×"
        onclick={() => {
          const tab = tabMenu?.tab;
          tabMenu = null;
          if (tab) terms.closeShell(id, tab);
        }}
      >
        {terms.isReviewTab(tabMenu.tab) ? "Close review" : "Close shell"}
      </MenuItem>
    </div>
  {/if}
{/if}

{#if panelAt}
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => panelAt = null}></div>
  <div class="fixed z-50 w-56 rounded-md border border-edge bg-panel p-1 shadow-xl" style="left:{panelAt.x}px;top:{panelAt.y}px" role="menu" aria-label="Panels" bind:this={panelMenu} tabindex="-1" onkeydown={panelKey}>
    {#each availablePanels as panel (panel.id)}
      {@const shown = !!groupOf(view.dock, panel.id) && !view.dockHidden}
      <MenuItem role="menuitemcheckbox" aria-label={panel.title} aria-checked={shown} icon={shown ? '✓' : ''} title={panel.title} onclick={() => togglePanel(panel.id)}>{panel.label}</MenuItem>
    {/each}
  </div>
{/if}
