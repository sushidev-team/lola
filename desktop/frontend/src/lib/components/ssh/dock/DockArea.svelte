<script lang="ts">
  // The SSH workspace's body: the timeline in its main slot, and widget panels around it.
  //
  // Everything here is arranged around one fact: WebKit reloads an iframe when it moves in the
  // DOM. A reload would lose the page's state, and the second load would trip the guard that
  // closes a widget for navigating. So panels never contain frames. The frames sit in one layer
  // of their own, in an each block that is only ever appended to and filtered (liveFrames), and
  // are laid over the panels by position. Dragging a tab changes numbers, never the DOM.
  //
  // Every rect comes from docklayout, which works from the measured size of this element, so
  // the panels and the frames over them cannot drift apart.
  //
  // z order inside this element's own stacking context: main and panels, then frames at 10,
  // sashes at 20, the drag preview at 30.
  import { tick, untrack, type Snippet } from "svelte";
  import { store } from "$lib/store.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { agentName, type SSHElement } from "$lib/ssh";
  import {
    MAIN_ID,
    PANEL_BORDER,
    PANEL_PAD,
    TAB_STRIP_H,
    activeTabs,
    allTabs,
    clampToDock,
    evenSplit,
    fits,
    groupOf,
    groups,
    hitTest,
    layout,
    liveFrames,
    moveTab,
    newPanelTarget,
    nextPanelTarget,
    panelSlot,
    resizeSplit,
    selectTab,
    shiftTab,
    tabIndexAt,
    type DockNode,
    type DropTarget,
    type Hit,
    type Rect,
    type Size,
  } from "$lib/docklayout";
  import MenuItem from "../../MenuItem.svelte";
  import SSHWidget from "../SSHWidget.svelte";
  import DockPanel from "./DockPanel.svelte";
  import DockSash from "./DockSash.svelte";

  // `covered`: a terminal or Changes is on show in the same cell. The conversation stays mounted
  // behind it, so a page keeps its state, but `visibility:hidden` on an ancestor does not hide a
  // frame: the shown frame's wrapper sets `visible`, and a child's `visible` wins, so the page
  // would paint over the terminal and take its clicks. Covered frames say hidden for themselves.
  let { sessionId, children, covered = false }: { sessionId: string; children: Snippet; covered?: boolean } = $props();

  const DRAWER_W = 440;
  const DOCK_BOTTOM_INSET = 8;
  const DRAG_START_PX = 4;
  const STRIP_SCROLL_BAND = 24;
  const STRIP_SCROLL_STEP = 16;
  const GHOST_W = 176;
  const GHOST_H = 28;

  const view = $derived(ssh.view(sessionId));
  const agent = $derived(agentName(store.sessionById(sessionId)?.agent));

  let host = $state<HTMLDivElement | null>(null);
  let bounds = $state<Size>({ w: 0, h: 0 });
  $effect(() => {
    const el = host;
    if (!el) return;
    const measure = () => {
      if (el.clientWidth !== bounds.w || el.clientHeight !== bounds.h) bounds = { w: el.clientWidth, h: el.clientHeight };
    };
    untrack(measure);
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => untrack(measure));
    ro.observe(el);
    return () => ro.disconnect();
  });

  const widgets = $derived(new Map(view.items.flatMap((i): [string, SSHElement][] => (i.element?.kind === "widget" ? [[i.element.id, i.element]] : []))));
  // Reads the items only: a change to the layout must not run this again.
  $effect(() => {
    view.items;
    untrack(() => view.syncDock());
  });

  const root = $derived(view.dock);
  const tabs = $derived(allTabs(root));
  const panels = $derived(groups(root));
  const measured = $derived(bounds.w > 0 && bounds.h > 0);
  // The timeline is the work, so a window too small for the layout never squeezes it. The tree
  // stays as it is, and comes back when the window grows.
  // A human hiding the dock takes this same path with the drawer shut: the frames stay in the
  // each below, hidden, and the main slot gets the full width. See SessionView.dockHidden.
  const compact = $derived(measured && tabs.length > 0 && (view.dockHidden || !fits(root, bounds)));
  $effect(() => {
    const c = compact;
    untrack(() => {
      view.dockCompact = c && !view.dockHidden;
      if (!c) view.dockDrawer = false;
    });
  });
  const geo = $derived(measured && !compact ? layout(root, bounds) : null);
  const drawerRect = $derived<Rect>({ x: Math.max(0, bounds.w - DRAWER_W), y: 0, w: Math.min(bounds.w, DRAWER_W), h: bounds.h });
  const drawerTab = $derived(tabs.includes(view.dockDrawerTab) ? view.dockDrawerTab : (activeTabs(root)[0] ?? ""));
  const drawerOpen = $derived(compact && view.dockDrawer && !view.dockHidden);
  const showing = $derived(compact ? (drawerOpen ? [drawerTab] : []) : activeTabs(root));

  // A panel that reaches the bottom stops where the composer does, so the two share a baseline.
  // The conversation's column ends 12px up, and a panel's own padding gives only 4 of those.
  function groundRect(r: Rect): Rect {
    return r.y + r.h >= bounds.h - 0.5 ? { ...r, h: Math.max(0, r.h - DOCK_BOTTOM_INSET) } : r;
  }
  function rectStyle(r: Rect): string {
    return `left:${r.x}px;top:${r.y}px;width:${r.w}px;height:${r.h}px`;
  }

  // A panel's card sits inside its rect, so a preview of the rect would read bigger than the
  // card that appears.
  function cardRect(r: Rect): Rect {
    return { x: r.x + PANEL_PAD, y: r.y + PANEL_PAD, w: Math.max(0, r.w - 2 * PANEL_PAD), h: Math.max(0, r.h - 2 * PANEL_PAD) };
  }

  // --- the drawer ---------------------------------------------------------------

  // Ahead of the app's own Esc, which would leave the focused session. Not from the terminal,
  // where Esc belongs to whatever runs in it, nor over a dialog or the tab menu.
  $effect(() => {
    if (!drawerOpen) return;
    const esc = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || menu || confirm.request || (e.target as Element | null)?.closest?.("[data-terminal-pane],[data-terminal-cell]")) return;
      e.preventDefault();
      e.stopImmediatePropagation();
      view.dockDrawer = false;
    };
    window.addEventListener("keydown", esc, true);
    return () => window.removeEventListener("keydown", esc, true);
  });

  function focusTab(tab: string) {
    host?.querySelector<HTMLElement>(`[data-dock-tab="${CSS.escape(tab)}"] [role="tab"]`)?.focus();
  }

  let drawerWasOpen = false;
  $effect(() => {
    const on = drawerOpen;
    untrack(() => {
      const was = drawerWasOpen;
      drawerWasOpen = on;
      if (on && !was) {
        void tick().then(() => focusTab(drawerTab));
      } else if (!on && was && (!document.activeElement || document.activeElement === document.body)) {
        const opener = host?.closest("[data-session-view]")?.querySelector<HTMLElement>("[data-dock-opener]");
        if (opener) opener.focus();
        else view.composerFocus++;
      }
    });
  });

  // --- frames ------------------------------------------------------------------

  let live = $state.raw<string[]>([]);
  const shownAt = new Map<string, number>();
  let clock = 0;
  $effect(() => {
    const on = showing;
    const all = tabs;
    if (!view.widgetsAllowed) {
      live = [];
      return;
    }
    for (const t of on) shownAt.set(t, ++clock);
    live = liveFrames(untrack(() => live), all, on, shownAt);
  });

  function frameRect(tab: string): Rect {
    if (compact) return panelSlot(drawerRect);
    const g = groupOf(root, tab);
    const r = g && geo?.nodes[g.id];
    return r ? panelSlot(groundRect(r)) : { x: 0, y: 0, w: 0, h: 0 };
  }

  // --- tab drag ----------------------------------------------------------------

  type TabDrag = { tab: string; x: number; y: number; hit: Hit | null; mark: Rect | null };
  let drag = $state.raw<TabDrag | null>(null);
  let pressing = $state(false);
  let resizing = $state("");
  let justDragged = false;
  const busy = $derived(pressing || !!drag || !!resizing);

  function hitAt(tab: string, clientX: number, clientY: number): Pick<TabDrag, "hit" | "mark"> {
    const box = host?.getBoundingClientRect();
    if (!box || !host) return { hit: null, mark: null };
    const others = (group: string) =>
      [...host!.querySelectorAll<HTMLElement>(`[data-dock-group="${CSS.escape(group)}"] [data-dock-tab]`)]
        .filter((el) => el.dataset.dockTab !== tab)
        .map((el) => {
          const r = el.getBoundingClientRect();
          return { left: r.left - box.left, right: r.right - box.left };
        });
    const at0 = clampToDock(bounds, clientX - box.left, clientY - box.top);
    const hit = at0 && hitTest(view.dock, bounds, tab, at0.x, at0.y, (g, x) => tabIndexAt(others(g), x));
    if (!hit) return { hit: null, mark: null };
    if (hit.index === undefined || hit.target.kind !== "tabs") return { hit, mark: null };
    const list = others(hit.target.group);
    const at = list[hit.index]?.left ?? list[list.length - 1]?.right ?? hit.preview.x + PANEL_PAD;
    const x = Math.max(hit.preview.x, Math.min(hit.preview.x + hit.preview.w - 2, at - 1));
    return { hit, mark: { x, y: hit.preview.y + PANEL_PAD + PANEL_BORDER, w: 2, h: TAB_STRIP_H } };
  }

  function startTabDrag(e: PointerEvent, tab: string) {
    if (e.button !== 0) return;
    const el = e.currentTarget as HTMLElement;
    const id = e.pointerId;
    const from = { x: e.clientX, y: e.clientY };
    let started = false;
    // From the press, not from the drag's start: a quick flick can be over a frame by its first
    // move, and a frame would swallow that move and every one after it.
    pressing = true;

    // On the window, as nothing is captured until this is really a drag. Capturing on the press
    // would send the click to this wrapper instead of the tab's button.
    const move = (ev: PointerEvent) => {
      if (ev.pointerId !== id) return;
      if (!started) {
        if (Math.hypot(ev.clientX - from.x, ev.clientY - from.y) < DRAG_START_PX) return;
        started = true;
        // Captured so it keeps coming, off the window's edge too.
        el.setPointerCapture(id);
        el.addEventListener("lostpointercapture", cancel);
      }
      autoScrollStrip(ev.clientX, ev.clientY);
      drag = { tab, x: ev.clientX, y: ev.clientY, ...hitAt(tab, ev.clientX, ev.clientY) };
    };
    const finish = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", cancel);
      el.removeEventListener("lostpointercapture", cancel);
      window.removeEventListener("keydown", esc, true);
      if (el.hasPointerCapture(id)) el.releasePointerCapture(id);
      drag = null;
      pressing = false;
      if (started) {
        // The click that ends a drag must not also select.
        justDragged = true;
        setTimeout(() => (justDragged = false));
      }
    };
    const up = (ev: PointerEvent) => {
      if (ev.pointerId !== id) return;
      const hit = started ? hitAt(tab, ev.clientX, ev.clientY).hit : null;
      finish();
      if (hit && !hit.refused) view.setDock(moveTab(view.dock, tab, hit.target));
    };
    const cancel = () => finish();
    const esc = (ev: KeyboardEvent) => {
      if (ev.key !== "Escape") return;
      ev.preventDefault();
      ev.stopImmediatePropagation();
      finish();
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", cancel);
    window.addEventListener("keydown", esc, true);
  }

  /** A strip too full to show its ends scrolls when a drag holds near one, or they are out of reach. */
  function autoScrollStrip(x: number, y: number) {
    const strip = document.elementsFromPoint(x, y).find((n) => n.matches("[role=tablist]"));
    if (!strip || strip.scrollWidth <= strip.clientWidth) return;
    const r = strip.getBoundingClientRect();
    if (x < r.left + STRIP_SCROLL_BAND) strip.scrollLeft -= STRIP_SCROLL_STEP;
    else if (x > r.right - STRIP_SCROLL_BAND) strip.scrollLeft += STRIP_SCROLL_STEP;
  }

  // --- sash --------------------------------------------------------------------

  let sashStart: DockNode | null = null;
  function sashKey(split: string, index: number) {
    return split + ":" + index;
  }

  // --- tabs --------------------------------------------------------------------

  function select(tab: string) {
    if (justDragged) return;
    view.dockDrawerTab = tab;
    view.setDock(selectTab(view.dock, tab));
  }
  const titleOf = (tab: string) => widgets.get(tab)?.title ?? "Widget";
  const faulted = (tab: string) => view.widgetFaults.has(tab);
  const unseen = (tab: string) => view.dockUnseen.has(tab);
  $effect(() => {
    for (const t of showing) if (view.dockUnseen.has(t)) view.dockUnseen.delete(t);
  });

  // A closed tab took focus with it, to the body, where the keyboard can do nothing. Only when
  // focus was on the strip or nowhere: a × clicked while typing elsewhere leaves that alone.
  function closeTab(tab: string) {
    const a = document.activeElement;
    const ours = !a || a === document.body || !!a.closest("[data-dock-group],[data-dock-menu]");
    const group = groupOf(view.dock, tab)?.id;
    view.closeWidget(tab);
    if (!ours) return;
    void tick().then(() => {
      const next = compact ? drawerTab : (groups(view.dock).find((g) => g.id === group)?.active ?? activeTabs(view.dock)[0]);
      if (next && allTabs(view.dock).includes(next)) focusTab(next);
      else view.composerFocus++;
    });
  }

  // --- tab menu ----------------------------------------------------------------

  // Either the actions of one tab, or the list of a strip whose tabs do not all fit.
  let menu = $state<{ tab: string; list: string[] | null; x: number; y: number } | null>(null);
  let menuEl = $state<HTMLDivElement | null>(null);
  let menuByKey = false;
  // The first item for a keyboard, the menu itself for a mouse: a ring on a row nobody went to
  // only reads as noise. The arrows work from either.
  $effect(() => {
    const el = menuEl;
    if (!el) return;
    (menuByKey ? el.querySelector<HTMLElement>('[role="menuitem"]:not(:disabled)') : el)?.focus();
  });
  const MENU_W = 200;
  const MENU_ROW = 28;
  let menuOpener: HTMLElement | null = null;
  function place(rows: number, x: number, y: number) {
    const h = Math.min(rows * MENU_ROW + 8, window.innerHeight * 0.6);
    return { x: Math.max(4, Math.min(x, window.innerWidth - MENU_W - 4)), y: Math.max(4, Math.min(y, window.innerHeight - h - 4)) };
  }
  function openMenu(tab: string, x: number, y: number, byKey: boolean) {
    menuOpener = document.activeElement as HTMLElement | null;
    menuByKey = byKey;
    menu = { tab, list: null, ...place(compact ? 2 : 5, x, y) };
  }
  function openList(list: string[], x: number, y: number, byKey: boolean) {
    menuOpener = document.activeElement as HTMLElement | null;
    menuByKey = byKey;
    menu = { tab: "", list, ...place(list.length, x - MENU_W, y) };
  }
  const right = $derived(menu && !menu.list && !compact ? newPanelTarget(root, menu.tab, "right", bounds) : null);
  const below = $derived(menu && !menu.list && !compact ? newPanelTarget(root, menu.tab, "bottom", bounds) : null);
  const nextPanel = $derived(menu && !menu.list && !compact ? nextPanelTarget(root, menu.tab) : null);
  function moveTo(target: DropTarget | null) {
    const m = menu;
    menu = null;
    if (m && target) view.setDock(moveTab(view.dock, m.tab, target));
  }
  function closeFromMenu() {
    const m = menu;
    if (m) closeTab(m.tab);
    menu = null;
  }
  function reloadFromMenu() {
    const m = menu;
    menu = null;
    if (m) view.reloadWidget(m.tab);
  }
  function pickFromList(tab: string) {
    menu = null;
    select(tab);
    void tick().then(() => focusTab(tab));
  }
  function menuKey(e: KeyboardEvent) {
    e.stopPropagation();
    if (e.key === "Escape") {
      e.preventDefault();
      menu = null;
      menuOpener?.focus();
      return;
    }
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const items = [...(menuEl?.querySelectorAll<HTMLElement>('[role="menuitem"]:not(:disabled)') ?? [])];
    const at = items.indexOf(document.activeElement as HTMLElement);
    const down = e.key === "ArrowDown";
    items[at < 0 ? (down ? 0 : items.length - 1) : (at + (down ? 1 : -1) + items.length) % items.length]?.focus();
  }
</script>

<div bind:this={host} class="relative isolate min-h-0 min-w-0 overflow-hidden bg-canvas {busy ? 'select-none' : ''}" data-dock data-dock-compact={compact ? "" : undefined}>
  <!-- The timeline. One node for the life of the workspace, or the terminal parked inside it
       would remount. A size change is all it ever sees. -->
  <div class="absolute isolate grid grid-cols-[minmax(0,1fr)] grid-rows-[minmax(0,1fr)] overflow-hidden" style={geo ? rectStyle(geo.nodes[MAIN_ID]) : "inset:0"} data-dock-main>
    {@render children()}
  </div>

  {#if geo}
    {#each panels as g, i (g.id)}
      <DockPanel
        id={g.id}
        rect={groundRect(geo.nodes[g.id])}
        tabs={g.tabs}
        active={g.active}
        {agent}
        gate={!view.widgetsAllowed && i === 0}
        {titleOf}
        {faulted}
        {unseen}
        dragging={drag?.tab ?? ""}
        pulse={view.dockPulse}
        onselect={select}
        onclose={closeTab}
        onallow={() => (view.widgetsAllowed = true)}
        onshift={(t, d) => view.setDock(shiftTab(view.dock, t, d))}
        onmenu={openMenu}
        onlist={openList}
        ondragstart={startTabDrag}
      />
    {/each}
  {:else if drawerOpen}
    <DockPanel
      id="drawer"
      rect={drawerRect}
      {tabs}
      active={drawerTab}
      {agent}
      gate={!view.widgetsAllowed}
      {titleOf}
      {faulted}
      {unseen}
      pulse={view.dockPulse}
      drawer
      onselect={select}
      onclose={closeTab}
      onallow={() => (view.widgetsAllowed = true)}
      onmenu={openMenu}
      onlist={openList}
      onhide={() => (view.dockDrawer = false)}
    />
  {/if}

  <!-- See the note at the top. Never wrap this each in anything that can remount it, and never
       let its order change. Hidden with visibility, so a background page keeps its real size
       and a chart that measures itself is not drawn at zero. -->
  <div class="pointer-events-none absolute inset-0 z-10" data-dock-frames>
    {#each live as id (id)}
      {@const element = widgets.get(id)}
      {@const on = showing.includes(id)}
      {#if element}
        <div
          class="absolute overflow-hidden rounded-b-[7px] {on && !busy && !covered ? 'pointer-events-auto' : ''}"
          style="{rectStyle(frameRect(id))};visibility:{on && !covered ? 'visible' : 'hidden'}"
          data-dock-frame={id}
        >
          <SSHWidget {sessionId} {element} {agent} active={on && !covered} />
        </div>
      {/if}
    {/each}
  </div>

  {#if geo}
    {#each geo.sashes as s (sashKey(s.split, s.index))}
      <DockSash
        sash={s}
        active={resizing === sashKey(s.split, s.index)}
        onstart={() => {
          sashStart = view.dock;
          resizing = sashKey(s.split, s.index);
        }}
        onmove={(d) => {
          if (sashStart) view.setDock(resizeSplit(sashStart, s.split, s.index, d, bounds), false);
        }}
        onend={(commit) => {
          if (commit) view.persistDock();
          else if (sashStart) view.setDock(sashStart, false);
          sashStart = null;
          resizing = "";
        }}
        onstep={(d) => view.setDock(resizeSplit(view.dock, s.split, s.index, d, bounds))}
        oneven={() => view.setDock(evenSplit(view.dock, s.split, s.index, bounds))}
      />
    {/each}
  {/if}

  {#if drag}
    <div class="absolute inset-0 z-30 cursor-grabbing" data-dock-dragging>
      {#if drag.hit && !drag.hit.refused}
        <div class="pointer-events-none absolute rounded-lg border border-accent/50 bg-accent/15 transition-[left,top,width,height] duration-100" style={rectStyle(cardRect(drag.hit.preview))} data-dock-preview></div>
      {/if}
      {#if drag.mark}
        <div class="pointer-events-none absolute bg-accent" style={rectStyle(drag.mark)} data-dock-mark></div>
      {/if}
    </div>
  {/if}
</div>

{#if drag}
  <!-- GHOST_W and GHOST_H keep it on screen near the window's edge, where a drag ends up most. -->
  <div class="pointer-events-none fixed z-50 max-w-44 truncate rounded border border-magenta/30 bg-panel px-2 py-0.5 text-sm shadow-xl {drag.hit?.refused ? 'text-warn' : drag.hit ? 'text-ink' : 'text-faint'}" style="left:{Math.max(0, Math.min(drag.x + 10, window.innerWidth - GHOST_W))}px;top:{Math.max(0, Math.min(drag.y + 10, window.innerHeight - GHOST_H))}px" data-dock-ghost>{drag.hit?.refused ? "No room" : titleOf(drag.tab)}</div>
{/if}

{#if menu}
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => (menu = null)} oncontextmenu={(e) => { e.preventDefault(); menu = null; }}></div>
  <div bind:this={menuEl} class="fixed z-50 max-h-[60vh] overflow-y-auto rounded-lg border border-edge bg-panel p-1 shadow-xl outline-none!" style="left:{menu.x}px;top:{menu.y}px;width:{MENU_W}px" role="menu" tabindex="-1" onkeydown={menuKey} data-dock-menu>
    {#if menu.list}
      {#each menu.list as t (t)}
        <MenuItem onclick={() => pickFromList(t)}>{titleOf(t)}</MenuItem>
      {/each}
    {:else}
      {#if !compact}
        <MenuItem disabled={!right} title={right ? undefined : "No room"} onclick={() => moveTo(right)}>Split right</MenuItem>
        <MenuItem disabled={!below} title={below ? undefined : "No room"} onclick={() => moveTo(below)}>Split down</MenuItem>
        {#if nextPanel}<MenuItem onclick={() => moveTo(nextPanel)}>Next panel</MenuItem>{/if}
      {/if}
      {#if view.widgetsAllowed}<MenuItem onclick={reloadFromMenu}>Reload</MenuItem>{/if}
      <MenuItem onclick={closeFromMenu}>Close</MenuItem>
    {/if}
  </div>
{/if}
