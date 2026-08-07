<script lang="ts">
  import { nav } from "$lib/nav.svelte";
  import { reflowGridRows } from "$lib/reflow";
  import LolaLogo from "./LolaLogo.svelte";
  import Button from "./Button.svelte";
  import SidebarTriage from "./SidebarTriage.svelte";
  import SidebarProjects from "./SidebarProjects.svelte";
  import SidebarStatus from "./SidebarStatus.svelte";
  import ActivityFeed from "./ActivityFeed.svelte";

  // Sidebar makes ZERO store reads. It is pure layout; every child reads
  // $lib/store.svelte / $lib/nav.svelte itself. A container that computes rows
  // and passes them down does NOT re-render on the async daemon push in the
  // production WKWebView — that is the documented failure that once left the
  // cockpit saying "select a session" forever.
  //
  // Collapse is the parent's grid track going to 0px plus overflow-hidden +
  // inert here. It is never an {#if}: a new mount boundary in App's template is
  // what froze the template effect before.
</script>

<aside
  class="group/side grid h-full min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] overflow-hidden border-r border-edge bg-canvas"
  style="grid-template-rows:44px minmax(0,1fr) 44px"
  aria-label="Sidebar"
  inert={!nav.sidebarOpen}
  {@attach reflowGridRows}
>
  <!-- 1. Brand row + window drag band. The pl-[82px] clears the inset traffic
       lights (InvisibleTitleBarHeight is 36; the lights sit at roughly x 13–73,
       y 10–26). NOTHING may be placed left of that padding, ever. Every control
       in this row is a real <button>, so app.css's `.no-drag, button, a, input…`
       rule opts it out of the drag region automatically — a clickable <div>
       here would be neither draggable nor reliably clickable. -->
  <div class="drag flex min-w-0 items-center gap-2 overflow-hidden pr-4 pl-[82px]">
    <LolaLogo class="h-[18px] w-auto min-w-0 shrink" />
    <Button
      icon
      class="ml-auto opacity-0 transition-opacity group-hover/side:opacity-100 focus-visible:opacity-100"
      title="hide sidebar (b)"
      aria-label="Hide sidebar"
      onclick={() => nav.toggleSidebar()}>«</Button
    >
  </div>

  <!-- 2. The scrolling body: Triage, Projects, Activity. It is ONE `1fr` track
       that scrolls internally, so the brand row and the status row above and
       below it stay pinned — the reason this is not five tracks on the <aside>
       itself. With five tracks, a full project list at the enforced 560px
       MinHeight (desktop/main.go) squeezed Activity below its own heading and it
       vanished entirely, heading included, with no scrollbar to recover it.
       Activity's track carries a 7rem FLOOR so it can never collapse again: once
       the three sections need more room than there is, this box overflows and
       scrolls instead of silently eating the last one.

       A GRID, not a flex column: the Activity wrapper below is itself a flex
       container, and WebKit does not stretch a display:flex child inside a flex
       column (it collapses to content width). Grid tracks stretch reliably. -->
  <div
    class="grid min-h-0 min-w-0 overflow-x-hidden overflow-y-auto"
    style="grid-template-rows:auto auto minmax(7rem,1fr)"
  >
    <SidebarTriage />
    <SidebarProjects />

    <!-- Activity. flex + flex-1 (NOT a nested fr grid): the child is a plain
         block, which is the pattern Panel.svelte already proves in WKWebView. A
         second nested `fr` track is the collapse risk reflow.ts documents. -->
    <div class="flex min-h-0 min-w-0 flex-col px-4 pt-3.5">
      <h2 class="label px-2 pb-1 text-faint">Activity</h2>
      <div class="min-h-0 flex-1 overflow-auto px-2"><ActivityFeed /></div>
    </div>
  </div>

  <!-- 3. Utility row -->
  <SidebarStatus />
</aside>
