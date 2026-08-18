<script lang="ts">
  import { onDestroy } from "svelte";
  import { store, type ProjectInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { displayName } from "$lib/slug";
  import {
    buildRows,
    moveProject,
    moveGroup,
    toLayout,
    dropIndex,
    groupRowZone,
    liftedIndex,
    type Row,
    type ProjectTarget,
  } from "$lib/sidebarlayout";
  import NavRow from "./NavRow.svelte";
  import SidebarGroupRow from "./SidebarGroupRow.svelte";
  import Button from "./Button.svelte";
  import MenuItem from "./MenuItem.svelte";
  import Modal from "./Modal.svelte";

  // The project switcher, moved out of the old Rail panel onto the shared
  // NavRow. Reads the store directly (leaf component) — see Sidebar.svelte.
  //
  // The panel draws ONE list in which a FOLDER is a row beside the projects, not
  // a section under them: drag a project onto a folder to file it there, drag it
  // out to a gap to un-file it, drag a folder to move it, members and all. Both
  // facts are config.toml — `[[project]].group` plus the `[[project]]` order and
  // each `[[group]]`'s position — and neither has any effect on what the daemon
  // does. The arrangement maths lives in $lib/sidebarlayout.

  // pending holds the arrangement a drop just applied, until the daemon's push
  // catches up. Without it the row snaps back to its old place for the frame
  // between the drop and the reload, which reads as "the drag didn't take".
  let pending = $state<Row<ProjectInfo>[] | null>(null);
  const rows = $derived(pending ?? buildRows(store.projects, store.groups));

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
  // The element that opened the menu, so focus can return to it on Escape. Kept
  // from the event rather than a bind:this — Button is a component, and binding
  // it would hand back the instance, not the node.
  let addTrigger: HTMLElement | null = null;

  function openAddMenu(e: MouseEvent) {
    addTrigger = e.currentTarget as HTMLElement;
    const r = addTrigger.getBoundingClientRect();
    addMenu = { x: Math.min(r.left, window.innerWidth - 180), y: r.bottom + 4 };
  }

  function closeAddMenu(refocus = false) {
    addMenu = null;
    if (refocus) addTrigger?.focus();
  }

  // Escape closes the menu, on the CAPTURE phase and swallowed: App.svelte's
  // global handler is on window too, and without this the key would fall through
  // to a view shortcut with a menu still open on top of it.
  $effect(() => {
    if (!addMenu) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.preventDefault();
      e.stopPropagation();
      closeAddMenu(true);
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  });

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
  // folder) live in $lib/sidebarlayout so they can be tested without a DOM.
  const DRAG_THRESHOLD = 4;

  type DragKind = "project" | "group";
  let listEl = $state<HTMLDivElement | null>(null);
  let press: { kind: DragKind; name: string; x: number; y: number } | null = null;
  let drag = $state<{ kind: DragKind; name: string } | null>(null);
  /** Where the dragged PROJECT would land. */
  let target = $state<ProjectTarget | null>(null);
  /** Which top-level gap the dragged FOLDER would land in. */
  let groupTarget = $state<number | null>(null);
  // A completed drag ends in a pointerup that the browser may follow with a
  // click on the row's primary button. Without this, every reorder would also
  // toggle the project's scope (or collapse the folder it landed in).
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
    target = null;
    groupTarget = null;
  }

  function onPointerMove(e: PointerEvent) {
    if (!press) return;
    if (!drag) {
      // A threshold, so a slightly shaky click is still a click.
      if (Math.hypot(e.clientX - press.x, e.clientY - press.y) < DRAG_THRESHOLD) return;
      drag = { kind: press.kind, name: press.name };
    }
    if (drag.kind === "project") target = resolveProjectDrop(e.clientY);
    else groupTarget = resolveGroupDrop(e.clientY);
  }

  async function onPointerUp() {
    stopTracking();
    const d = drag;
    const toProject = target;
    const toGroup = groupTarget;
    press = null;
    drag = null;
    target = null;
    groupTarget = null;
    if (!d) return;
    suppressClick = true;
    setTimeout(() => (suppressClick = false), 0);

    if (d.kind === "project" && toProject) void tryArrangement(moveProject(rows, d.name, dropped(toProject, d.name)));
    else if (d.kind === "group" && toGroup !== null) {
      const src = rows.findIndex((r) => r.kind === "group" && r.group.name === d.name);
      void tryArrangement(moveGroup(rows, d.name, liftedIndex(toGroup, src < 0 ? null : src)));
    }
  }

  /**
   * Re-base a drop target from the LIVE list the indicator was drawn against to
   * the post-lift list moveProject works in. Only a drag that ends BELOW its own
   * starting row in the SAME list needs it — see liftedIndex.
   */
  function dropped(to: ProjectTarget, name: string): ProjectTarget {
    if (to.kind === "into") return to;
    if (to.kind === "top") {
      const src = rows.findIndex((r) => r.kind === "project" && r.project.name === name);
      return { kind: "top", index: liftedIndex(to.index, src < 0 ? null : src) };
    }
    const row = rows.find((r) => r.kind === "group" && r.group.name === to.group);
    const src = row?.kind === "group" ? row.projects.findIndex((p) => p.name === name) : -1;
    return { kind: "group", group: to.group, index: liftedIndex(to.index, src < 0 ? null : src) };
  }

  /**
   * Persist a new arrangement, holding it on screen meanwhile. Shared by the
   * drop handler and the keyboard moves so the two cannot drift.
   */
  async function applyArrangement(next: Row<ProjectInfo>[]) {
    pending = next;
    await store.setProjectLayout(toLayout(next));
    pending = null;
  }

  /**
   * Apply only a REAL change. A drop back where it started, or a nudge at the
   * end of a list, must not write: the layout is the whole arrangement, so it
   * would touch config.toml (and reload the daemon) for nothing.
   */
  function tryArrangement(next: Row<ProjectInfo>[]) {
    if (JSON.stringify(toLayout(rows)) === JSON.stringify(toLayout(next))) return;
    return applyArrangement(next);
  }

  /** Which gap — or which folder — the pointer is over. */
  function resolveProjectDrop(y: number): ProjectTarget {
    const tops = Array.from(listEl?.querySelectorAll<HTMLElement>("[data-toprow]") ?? []);
    if (tops.length === 0) return { kind: "top", index: 0 };

    // Inside an open folder's member list: this is a placement among ITS rows.
    for (const el of tops) {
      const g = el.dataset.group;
      const members = g ? el.querySelector<HTMLElement>("[data-members]") : null;
      if (!members) continue;
      const r = members.getBoundingClientRect();
      if (r.height > 0 && y >= r.top && y <= r.bottom) {
        return { kind: "group", group: g!, index: dropIndex(rects(members.querySelectorAll("[data-row]")), y) };
      }
    }
    // On a folder's own row: its middle band files the project INTO it, its
    // edges are the gaps either side (which the top-level pass below resolves).
    for (const el of tops) {
      const g = el.dataset.group;
      const head = g ? el.querySelector<HTMLElement>("[data-head]") : null;
      if (!head) continue;
      const r = head.getBoundingClientRect();
      if (y >= r.top && y <= r.bottom && groupRowZone({ top: r.top, height: r.height }, y) === "into") {
        return { kind: "into", group: g! };
      }
    }
    return { kind: "top", index: dropIndex(rects(tops), y) };
  }

  /** Which top-level gap a dragged folder is over. */
  function resolveGroupDrop(y: number): number {
    return dropIndex(rects(listEl?.querySelectorAll("[data-toprow]") ?? []), y);
  }

  function rects(els: ArrayLike<Element>): { top: number; height: number }[] {
    return Array.from(els).map((el) => {
      const r = el.getBoundingClientRect();
      return { top: r.top, height: r.height };
    });
  }

  function openProject(name: string) {
    if (suppressClick) return;
    nav.toggleProjectScope(name);
  }

  function toggleGroup(name: string, collapsed: boolean) {
    if (suppressClick) return;
    void store.setGroupCollapsed(name, !collapsed);
  }

  // --- keyboard arranging -------------------------------------------------------
  // The pointer drag is otherwise the ONLY way to arrange the panel — the project
  // form deliberately has no group field — so the same moves are bound on the
  // focused row: ⌥↑/⌥↓ to move it, ⌥→ to file it into the nearest folder, ⌥← to
  // take it back out.

  function nudgeProject(e: KeyboardEvent, name: string, group: string, index: number) {
    if (!e.altKey) return;
    const inGroup = group !== "";
    let next: Row<ProjectInfo>[] | null = null;
    switch (e.key) {
      case "ArrowUp":
      case "ArrowDown": {
        const to = index + (e.key === "ArrowUp" ? -1 : 1);
        next = moveProject(rows, name, inGroup ? { kind: "group", group, index: to } : { kind: "top", index: to });
        break;
      }
      case "ArrowRight": {
        if (inGroup) return; // already filed; ⌥→ has nothing further to do
        const g = nearestGroup(index);
        if (!g) return;
        next = moveProject(rows, name, { kind: "into", group: g });
        break;
      }
      case "ArrowLeft": {
        if (!inGroup) return;
        // Out of the folder and immediately after it, so the row stays where the
        // eye last saw it.
        const at = rows.findIndex((r) => r.kind === "group" && r.group.name === group);
        next = moveProject(rows, name, { kind: "top", index: at + 1 });
        break;
      }
      default:
        return;
    }
    e.preventDefault();
    if (next) void tryArrangement(next);
  }

  /** The folder nearest a top-level row: the closest one above, else below. */
  function nearestGroup(index: number): string | null {
    for (let i = index - 1; i >= 0; i--) {
      const r = rows[i];
      if (r.kind === "group") return r.group.name;
    }
    for (let i = index + 1; i < rows.length; i++) {
      const r = rows[i];
      if (r.kind === "group") return r.group.name;
    }
    return null;
  }

  function nudgeGroup(e: KeyboardEvent, name: string, index: number) {
    if (!e.altKey || (e.key !== "ArrowUp" && e.key !== "ArrowDown")) return;
    e.preventDefault();
    void tryArrangement(moveGroup(rows, name, index + (e.key === "ArrowUp" ? -1 : 1)));
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
    onkeydown={(e) => nudgeProject(e, p.name, group, index)}
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
    {@const gap = drag?.kind === "group" ? groupTarget : target?.kind === "top" ? target.index : null}
    <!-- Capped so a long project list can never squeeze Activity out of the
         column; the list scrolls inside itself instead. -->
    <div bind:this={listEl} class="max-h-[38vh] overflow-auto" class:select-none={!!drag}>
      <ul>
        {#each rows as row, i (row.kind === "group" ? "g:" + row.group.name : "p:" + row.project.name)}
          {#if gap === i}{@render dropLine()}{/if}
          {#if row.kind === "project"}
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <!-- The pointerdown is a drag ENHANCEMENT on a row whose own control
                 is a real <button> (NavRow's), which keeps the keyboard and AT
                 path intact; the same moves are on ⌥+arrow there. -->
            <li
              data-toprow
              data-row
              class:opacity-40={drag?.kind === "project" && drag.name === row.project.name}
              onpointerdown={(e) => startPress(e, "project", row.project.name)}
            >
              {@render projectRow(row.project, "", i)}
            </li>
          {:else}
            {@const g = row.group}
            {@const collapsed = !!g.collapsed}
            <li data-toprow data-group={g.name}>
              <SidebarGroupRow
                label={g.label || g.name}
                count={row.projects.length}
                {collapsed}
                dragging={drag?.kind === "group" && drag.name === g.name}
                dropTarget={target?.kind === "into" && target.group === g.name}
                ontoggle={() => toggleGroup(g.name, collapsed)}
                onkeydown={(e) => nudgeGroup(e, g.name, i)}
                onrename={() => (dialog = { mode: "rename", name: g.name, value: g.label || g.name })}
                onremove={() => askRemoveGroup(g.name, g.label || g.name)}
                onpointerdown={(e) => startPress(e, "group", g.name)}
              />
              {#if !collapsed}
                <ul class="pl-3" data-members={g.name}>
                  {#each row.projects as p, j (p.name)}
                    {#if target?.kind === "group" && target.group === g.name && target.index === j}
                      {@render dropLine()}
                    {/if}
                    <!-- svelte-ignore a11y_no_static_element_interactions -->
                    <li
                      data-row
                      class:opacity-40={drag?.kind === "project" && drag.name === p.name}
                      onpointerdown={(e) => startPress(e, "project", p.name)}
                    >
                      {@render projectRow(p, g.name, j)}
                    </li>
                  {/each}
                  {#if target?.kind === "group" && target.group === g.name && target.index === row.projects.length}
                    {@render dropLine()}
                  {/if}
                  <!-- An open, empty folder has no height of its own and could
                       never be hit; the placeholder exists only mid-drag so it
                       can still receive the row. -->
                  {#if row.projects.length === 0 && drag?.kind === "project"}
                    <li class="mx-2 h-6 rounded-md border border-dashed border-edge" aria-hidden="true"></li>
                  {/if}
                </ul>
              {/if}
            </li>
          {/if}
        {/each}
        {#if gap === rows.length}{@render dropLine()}{/if}
      </ul>
    </div>
  {/if}
</nav>

{#if addMenu}
  <!-- Backdrop: any click outside dismisses without falling through to the
       surface underneath. Same shape as the session context menu. -->
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => closeAddMenu()}></div>
  <div
    class="fixed z-50 min-w-[11rem] rounded-lg border border-edge bg-panel p-1 shadow-xl"
    style="left:{addMenu.x}px;top:{addMenu.y}px"
    role="menu"
  >
    <MenuItem
      icon="+"
      onclick={() => {
        closeAddMenu();
        nav.openOverlay("project", "");
      }}>New project</MenuItem
    >
    <MenuItem
      icon="▸"
      onclick={() => {
        closeAddMenu();
        dialog = { mode: "add", name: "", value: "" };
      }}>New group</MenuItem
    >
  </div>
{/if}

{#if dialog}
  <Modal title={dialog.mode === "add" ? "New group" : "Rename group"} width="420px" onClose={() => (dialog = null)}>
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
