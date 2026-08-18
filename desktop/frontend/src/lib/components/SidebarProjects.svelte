<script lang="ts">
  import { onDestroy } from "svelte";
  import { store, type ProjectInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { displayName } from "$lib/slug";
  import { buildSections, moveProject, moveGroup, toLayout, dropIndex, type Section } from "$lib/sidebarlayout";
  import NavRow from "./NavRow.svelte";
  import SidebarGroupRow from "./SidebarGroupRow.svelte";
  import Button from "./Button.svelte";
  import MenuItem from "./MenuItem.svelte";
  import Modal from "./Modal.svelte";

  // The project switcher, moved out of the old Rail panel onto the shared
  // NavRow. Reads the store directly (leaf component) — see Sidebar.svelte.
  //
  // Projects are arranged in two ways, both persisted in config.toml and both
  // driven from here: they can be filed under a [[group]] (a folder) and they
  // can be dragged into any order. The order is the [[project]] array's, which
  // is what the TUI renders too — so a drag here reorders both surfaces.

  // pending holds the arrangement a drop just applied, until the daemon's push
  // catches up. Without it the row snaps back to its old place for the frame
  // between the drop and the reload, which reads as "the drag didn't take".
  let pending = $state<Section<ProjectInfo>[] | null>(null);
  const sections = $derived(pending ?? buildSections(store.projects, store.groups));
  const topSection = $derived(sections.find((s) => !s.group));
  const groupSections = $derived(sections.filter((s) => s.group));

  function pollDot(name: string): { glyph: string; cls: string; faint: boolean } {
    const ps = (store.status?.polls ?? []).find((p) => p.name === name);
    if (!ps) return { glyph: "·", cls: "text-faint", faint: true };
    if (ps.lastError) return { glyph: "●", cls: "text-bad", faint: false };
    if (ps.enabled) return { glyph: "●", cls: "text-good", faint: false };
    return { glyph: "○", cls: "text-faint", faint: true };
  }

  // --- add menu ---------------------------------------------------------------
  // Fixed-positioned off the button's own rect rather than absolutely positioned
  // inside the header: the sidebar body scrolls and clips, and a popover that
  // can be cut in half is worse than one that floats.
  let addMenu = $state<{ x: number; y: number } | null>(null);

  function openAddMenu(e: MouseEvent) {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    addMenu = { x: Math.min(r.left, window.innerWidth - 180), y: r.bottom + 4 };
  }

  // --- group add / rename -----------------------------------------------------
  let dialog = $state<{ mode: "add" | "rename"; name: string; value: string } | null>(null);

  async function submitDialog() {
    const d = dialog;
    if (!d) return;
    const value = d.value.trim();
    if (!value) return;
    dialog = null;
    if (d.mode === "add") await store.addGroup(value);
    else await store.renameGroup(d.name, value);
  }

  function askRemoveGroup(name: string, label: string) {
    confirm.ask({
      title: "Remove group?",
      body: `Remove the group "${label}".`,
      // Said plainly because the opposite is the reasonable fear: this is the
      // one "remove" in the sidebar that costs nothing but the folder.
      detail: "Its projects move back to the top level — nothing is deleted.",
      confirmLabel: "Remove",
      onConfirm: () => void store.removeGroup(name),
    });
  }

  // --- drag to arrange ---------------------------------------------------------
  // Pointer events rather than HTML5 drag-and-drop: this ships inside a
  // WKWebView, where dragover/dragimage behaviour differs from the Chrome the
  // dev server runs, and the decisions worth getting right (which gap, which
  // section) live in $lib/sidebarlayout so they can be tested without a DOM.
  const DRAG_THRESHOLD = 4;

  type DragKind = "project" | "group";
  let listEl = $state<HTMLDivElement | null>(null);
  let press: { kind: DragKind; name: string; x: number; y: number } | null = null;
  let drag = $state<{ kind: DragKind; name: string } | null>(null);
  let dropProject = $state<{ group: string; index: number } | null>(null);
  let dropGroup = $state<number | null>(null);
  // A completed drag ends in a pointerup that the browser may follow with a
  // click on the row's primary button. Without this, every reorder would also
  // toggle the project's scope (or collapse the group it landed in).
  let suppressClick = false;

  function startPress(e: PointerEvent, kind: DragKind, name: string) {
    if (e.button !== 0) return;
    // The trailing controls (settings, hub, rename, remove) are not drag
    // handles; a press on one of them stays a plain click.
    if ((e.target as HTMLElement).closest("[data-drag-ignore]")) return;
    press = { kind, name, x: e.clientX, y: e.clientY };
    window.addEventListener("pointermove", onPointerMove);
    window.addEventListener("pointerup", onPointerUp);
    // pointercancel, not just pointerup: the OS takes the pointer away on a
    // gesture, a system dialog or a lost capture, and no pointerup follows.
    // Without it the move listener and the half-drag state would both survive,
    // leaving the next unrelated mouse move dragging a row.
    window.addEventListener("pointercancel", cancelDrag);
  }

  function stopTracking() {
    window.removeEventListener("pointermove", onPointerMove);
    window.removeEventListener("pointerup", onPointerUp);
    window.removeEventListener("pointercancel", cancelDrag);
  }

  /** Abandon a drag WITHOUT writing anything — the arrangement is unchanged. */
  function cancelDrag() {
    stopTracking();
    press = null;
    drag = null;
    dropProject = null;
    dropGroup = null;
  }

  function onPointerMove(e: PointerEvent) {
    if (!press) return;
    if (!drag) {
      // A threshold, so a slightly shaky click is still a click.
      if (Math.hypot(e.clientX - press.x, e.clientY - press.y) < DRAG_THRESHOLD) return;
      drag = { kind: press.kind, name: press.name };
    }
    if (drag.kind === "project") dropProject = resolveProjectDrop(e.clientY);
    else dropGroup = resolveGroupDrop(e.clientY);
  }

  async function onPointerUp() {
    stopTracking();
    const d = drag;
    const toProject = dropProject;
    const toGroup = dropGroup;
    press = null;
    drag = null;
    dropProject = null;
    dropGroup = null;
    if (!d) return;
    suppressClick = true;
    setTimeout(() => (suppressClick = false), 0);

    const base = sections;
    let next: Section<ProjectInfo>[] | null = null;
    if (d.kind === "project" && toProject) next = moveProject(base, d.name, toProject.group, toProject.index);
    else if (d.kind === "group" && toGroup !== null) next = moveGroup(base, d.name, toGroup);
    // A drop back where it started is not a config write. The layout is the
    // whole arrangement, so writing it would touch config.toml (and reload the
    // daemon) every time someone nudged a row.
    if (!next || sameArrangement(base, next)) return;

    await applyArrangement(next);
  }

  /**
   * Persist a new arrangement, holding it on screen meanwhile. Shared by the
   * drop handler and the keyboard reorder so the two cannot drift.
   */
  async function applyArrangement(next: Section<ProjectInfo>[]) {
    pending = next;
    await store.setProjectLayout(toLayout(next));
    pending = null;
  }

  /**
   * alt+↑/↓ on a focused row moves it, so arranging the sidebar does not require
   * a pointer. It moves WITHIN the row's own section; changing a project's group
   * from the keyboard is the project form's Group field.
   */
  function nudgeProject(e: KeyboardEvent, group: string, index: number, name: string) {
    const dir = arrowDir(e);
    if (dir === 0) return;
    e.preventDefault();
    void tryArrangement(moveProject(sections, name, group, index + dir));
  }

  function nudgeGroup(e: KeyboardEvent, index: number, name: string) {
    const dir = arrowDir(e);
    if (dir === 0) return;
    e.preventDefault();
    void tryArrangement(moveGroup(sections, name, index + dir));
  }

  /** 0 unless this is the alt+arrow chord, in which case -1 (up) or +1 (down). */
  function arrowDir(e: KeyboardEvent): -1 | 0 | 1 {
    if (!e.altKey) return 0;
    if (e.key === "ArrowUp") return -1;
    if (e.key === "ArrowDown") return 1;
    return 0;
  }

  /** Apply only a REAL change — a nudge at either end of a section is a no-op. */
  function tryArrangement(next: Section<ProjectInfo>[]) {
    if (sameArrangement(sections, next)) return;
    return applyArrangement(next);
  }

  function sameArrangement(a: Section<ProjectInfo>[], b: Section<ProjectInfo>[]): boolean {
    return JSON.stringify(toLayout(a)) === JSON.stringify(toLayout(b));
  }

  /** Which section, and which gap inside it, the pointer is over. */
  function resolveProjectDrop(y: number): { group: string; index: number } | null {
    const zones = Array.from(listEl?.querySelectorAll<HTMLElement>("[data-zone]") ?? []);
    if (zones.length === 0) return null;
    let hit = zones.find((z) => {
      const r = z.getBoundingClientRect();
      return y >= r.top && y <= r.bottom;
    });
    // Above the first zone or below the last: take the nearest, so overshooting
    // the list still lands somewhere rather than cancelling the drag.
    if (!hit) hit = zones.reduce((best, z) => (zoneDistance(z, y) < zoneDistance(best, y) ? z : best));

    const group = hit.dataset.zone ?? "";
    // A collapsed group draws no rows, so its header IS the zone and its first
    // slot is the only landing place that means anything.
    if (hit.dataset.collapsed === "true") return { group, index: 0 };
    const rows = Array.from(hit.querySelectorAll<HTMLElement>("[data-row]")).map((el) => {
      const r = el.getBoundingClientRect();
      return { top: r.top, height: r.height };
    });
    return { group, index: dropIndex(rows, y) };
  }

  /** Which gap between GROUPS the pointer is over. */
  function resolveGroupDrop(y: number): number {
    const zones = Array.from(listEl?.querySelectorAll<HTMLElement>("[data-group-zone]") ?? []);
    return dropIndex(
      zones.map((z) => {
        const r = z.getBoundingClientRect();
        return { top: r.top, height: r.height };
      }),
      y,
    );
  }

  function zoneDistance(el: HTMLElement, y: number): number {
    const r = el.getBoundingClientRect();
    return y < r.top ? r.top - y : y - r.bottom;
  }

  function openProject(name: string) {
    if (suppressClick) return;
    nav.toggleProjectScope(name);
  }

  function toggleGroup(name: string, collapsed: boolean) {
    if (suppressClick) return;
    void store.setGroupCollapsed(name, !collapsed);
  }

  onDestroy(stopTracking);
</script>

{#snippet projectRow(p: ProjectInfo, group: string, index: number)}
  {@const d = pollDot(p.name)}
  {@const active = nav.scoped && nav.project === p.name}
  <!-- Mirrors nav.toggleProjectScope's own condition: a row is drawn active
       on the project hub too (scope outlives goDetail), and there the click
       still navigates rather than clearing. -->
  {@const clears = active && nav.view === "cockpit"}
  <NavRow
    label={displayName(p)}
    glyph={d.glyph}
    glyphCls={d.cls}
    dim={d.faint}
    {active}
    title={clears ? "clear the project filter" : "scope the cockpit to this project"}
    onclick={() => openProject(p.name)}
    onkeydown={(e) => nudgeProject(e, group, index, p.name)}
  >
    {#snippet badges()}
      <!-- Bare glyph counts on purpose: the triage row named "Needs You"
           counts needs_input ONLY, while ProjectInfo.needsYou is the
           wider attention set. Two different numbers must never appear
           under one name, so this one never spells itself out. -->
      <!-- The glyph is aria-hidden and the meaning is spelled out in an
           sr-only sibling: `title` on a <span> is a mouse-only tooltip
           that no screen reader announces, so "3!" was reaching AT as the
           bare string "3!". -->
      {#if p.needsYou > 0}<span class="num text-sm text-orange" title="{p.needsYou} need you"
          ><span aria-hidden="true">{p.needsYou}!</span><span class="sr-only">{p.needsYou} need you</span></span
        >{/if}
      {#if p.ciRed > 0}<span class="num text-sm text-bad" title="{p.ciRed} failing CI"
          ><span aria-hidden="true">{p.ciRed}✕</span><span class="sr-only">{p.ciRed} failing CI</span></span
        >{/if}
    {/snippet}
    {#snippet actions()}
      <!-- data-drag-ignore: these are controls, not drag handles. -->
      <span class="flex items-center" data-drag-ignore>
        <Button
          size="xs"
          icon
          title="project settings"
          aria-label="{displayName(p)} settings"
          onclick={() => nav.openOverlay("project", p.name)}
        >
          <svg
            viewBox="0 0 24 24"
            class="h-3.5 w-3.5"
            fill="none"
            stroke="currentColor"
            stroke-width="1.9"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <circle cx="12" cy="12" r="3" />
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
          </svg>
        </Button>
        <Button
          size="xs"
          icon
          title="open project hub"
          aria-label="{displayName(p)} hub"
          onclick={() => nav.goDetail(p.name)}>›</Button
        >
      </span>
    {/snippet}
  </NavRow>
{/snippet}

<!-- A 2px accent rule in the gap the drop would land in. It replaces no row and
     takes no height of its own (-my-px), so the list does not shuffle under the
     pointer while the indicator moves. -->
{#snippet dropLine()}
  <li class="-my-px h-0.5 rounded-full bg-accent" aria-hidden="true"></li>
{/snippet}

{#snippet projectList(group: string, projects: ProjectInfo[])}
  <ul>
    {#each projects as p, i (p.name)}
      {#if dropProject?.group === group && dropProject.index === i}{@render dropLine()}{/if}
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <!-- The pointerdown is a drag ENHANCEMENT on a row whose own control is a
           real <button> (NavRow's), which keeps the keyboard and AT path intact;
           the reorder itself is also reachable there via alt+arrow. -->
      <li
        data-row
        class:opacity-40={drag?.kind === "project" && drag.name === p.name}
        onpointerdown={(e) => startPress(e, "project", p.name)}
      >
        {@render projectRow(p, group, i)}
      </li>
    {/each}
    {#if dropProject?.group === group && dropProject.index === projects.length}{@render dropLine()}{/if}
    <!-- An empty section has no height and could never be hit, which would make
         a project impossible to drag OUT of a folder (or into an empty one).
         The placeholder exists only while a drag is in flight. -->
    {#if projects.length === 0 && drag?.kind === "project"}
      <li class="mx-2 h-6 rounded-md border border-dashed border-edge" aria-hidden="true"></li>
    {/if}
  </ul>
{/snippet}

<nav class="min-w-0 px-3 pt-3.5" aria-label="Projects">
  <div class="flex items-center px-2 pb-1">
    <h2 class="label text-faint">Projects</h2>
    <Button
      size="xs"
      icon
      class="ml-auto opacity-0 transition-opacity group-hover/side:opacity-100 focus-visible:opacity-100"
      title="add a project or a group"
      aria-label="Add"
      aria-haspopup="menu"
      onclick={openAddMenu}>+</Button
    >
  </div>

  {#if store.projects.length === 0 && store.groups.length === 0}
    <!-- A dashed drop-zone-ish placeholder, not a <Button>: it is an empty STATE
         that happens to be clickable, and its dashed border and 3-line height are
         not part of the button ladder. -->
    <button
      class="w-full rounded-md border border-dashed border-edge px-2 py-3 text-center text-sm text-faint transition-colors hover:border-accent hover:text-accent-ink"
      onclick={() => nav.openOverlay("project", "")}>No projects — add one</button
    >
  {:else}
    <!-- Capped so a long project list can never squeeze Activity out of the
         column; the list scrolls inside itself instead. The cap is on this
         wrapper rather than on a <ul>, because the list is now several sections
         and they scroll as one. -->
    <div bind:this={listEl} class="max-h-[38vh] overflow-auto" class:select-none={!!drag}>
      <div data-zone="">
        {@render projectList("", topSection?.projects ?? [])}
      </div>

      {#each groupSections as s, gi (s.group!.name)}
        {@const g = s.group!}
        {@const collapsed = !!g.collapsed}
        {#if dropGroup === gi && drag?.kind === "group"}{@render dropLine()}{/if}
        <div data-zone={g.name} data-group-zone data-collapsed={collapsed}>
          <SidebarGroupRow
            label={g.label || g.name}
            count={s.projects.length}
            {collapsed}
            dragging={drag?.kind === "group" && drag.name === g.name}
            ontoggle={() => toggleGroup(g.name, collapsed)}
            onrename={() => (dialog = { mode: "rename", name: g.name, value: g.label || g.name })}
            onremove={() => askRemoveGroup(g.name, g.label || g.name)}
            onpointerdown={(e) => startPress(e, "group", g.name)}
            onkeydown={(e) => nudgeGroup(e, gi, g.name)}
          />
          {#if collapsed}
            <!-- Collapsed: the whole group is one drop slot, so the indicator
                 sits under its header rather than among rows that aren't drawn. -->
            {#if dropProject?.group === g.name}
              <ul>{@render dropLine()}</ul>
            {/if}
          {:else}
            <div class="pl-3">
              {@render projectList(g.name, s.projects)}
            </div>
          {/if}
        </div>
      {/each}
      {#if dropGroup === groupSections.length && drag?.kind === "group"}{@render dropLine()}{/if}
    </div>
  {/if}
</nav>

{#if addMenu}
  <!-- Backdrop: any click outside dismisses without falling through to the
       surface underneath. Same shape as the session context menu. -->
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => (addMenu = null)}></div>
  <div
    class="fixed z-50 min-w-[11rem] rounded-lg border border-edge bg-panel p-1 shadow-xl"
    style="left:{addMenu.x}px;top:{addMenu.y}px"
    role="menu"
  >
    <MenuItem
      icon="+"
      onclick={() => {
        addMenu = null;
        nav.openOverlay("project", "");
      }}>New project</MenuItem
    >
    <MenuItem
      icon="▸"
      onclick={() => {
        addMenu = null;
        dialog = { mode: "add", name: "", value: "" };
      }}>New group</MenuItem
    >
  </div>
{/if}

{#if dialog}
  <Modal
    title={dialog.mode === "add" ? "New group" : "Rename group"}
    width="420px"
    onClose={() => (dialog = null)}
  >
    <label class="block">
      <span class="label mb-1 block text-faint">Name</span>
      <input
        class="w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder"
        placeholder="Clients"
        bind:value={dialog.value}
        onkeydown={(e) => {
          // Escape is handled here rather than by App.svelte's global handler:
          // this is not a nav overlay, and that handler bails while a field has
          // focus (see its typing() guard).
          if (e.key === "Escape") dialog = null;
          if (e.key === "Enter") void submitDialog();
        }}
      />
    </label>
    {#snippet footer()}
      <div class="flex justify-end gap-2">
        <Button onclick={() => (dialog = null)}>Cancel</Button>
        <Button variant="primary" disabled={!dialog?.value.trim()} onclick={() => void submitDialog()}>
          {dialog?.mode === "add" ? "Create" : "Rename"}
        </Button>
      </div>
    {/snippet}
  </Modal>
{/if}
