<script lang="ts">
  import Button from "./Button.svelte";

  // A project FOLDER's row. It sits in the project list beside the projects
  // rather than heading a section below them, and a project row dropped on it is
  // filed inside — so it is a drop TARGET as much as a control.
  //
  // Hand-rolled rather than a NavRow because it is not a nav target: clicking it
  // discloses its members and it carries no glyph column of its own (the
  // triangle takes that slot). Its density is NavRow's h-7 verbatim so the list
  // reads as one ladder.

  let {
    label,
    count,
    collapsed = false,
    dragging = false,
    dropTarget = false,
    ontoggle,
    onkeydown,
    onrename,
    onremove,
    onpointerdown,
  }: {
    label: string;
    count: number;
    collapsed?: boolean;
    /** True while THIS row is the one being dragged. */
    dragging?: boolean;
    /** True while a dragged project would be filed INTO this folder. */
    dropTarget?: boolean;
    ontoggle: () => void;
    /** alt+arrow reorder, handled on the header's own control. */
    onkeydown?: (e: KeyboardEvent) => void;
    onrename: () => void;
    onremove: () => void;
    onpointerdown: (e: PointerEvent) => void;
  } = $props();
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<!-- The pointerdown is a drag ENHANCEMENT; the row's own control below is a real
     <button>, so the keyboard and AT path are unaffected. -->
<div
  data-head
  class="group/row flex h-7 w-full items-center rounded-md text-faint transition-colors hover:bg-sel/60 hover:text-ink"
  class:opacity-40={dragging}
  class:bg-accent-fill={dropTarget}
  class:text-accent-ink={dropTarget}
  {onpointerdown}
>
  <button
    class="flex h-full min-w-0 grow items-center gap-2 rounded-md px-2 text-left"
    aria-expanded={!collapsed}
    title={collapsed ? "expand group" : "collapse group"}
    onclick={ontoggle}
    {onkeydown}
  >
    <span class="w-3.5 shrink-0 text-center text-sm" aria-hidden="true">{collapsed ? "▸" : "▾"}</span>
    <span class="truncate font-medium">{label}</span>
    <!-- Always rendered, 0 included: a count that appears and vanishes as
         projects move in and out makes the whole list jump. -->
    <span class="num ml-auto shrink-0 pl-2 text-sm" class:text-faint={!dropTarget}>{count}</span>
  </button>
  <!-- data-drag-ignore: controls, not drag handles (see SidebarProjects). -->
  <span
    class="flex shrink-0 items-center pr-2 opacity-0 transition-opacity group-hover/row:opacity-100 focus-within:opacity-100"
    data-drag-ignore
  >
    <Button size="xs" icon title="rename group" aria-label="Rename {label}" onclick={onrename}>✎</Button>
    <!-- Deleting a folder never deletes a project — they return to the top
         level — but it is still irreversible arrangement, so it confirms. -->
    <Button size="xs" icon variant="danger" title="remove group" aria-label="Remove {label}" onclick={onremove}
      >✕</Button
    >
  </span>
</div>
