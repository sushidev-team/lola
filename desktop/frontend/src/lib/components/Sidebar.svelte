<script lang="ts">
  import { nav } from "$lib/nav.svelte";
  import { reflowGridRows } from "$lib/reflow";
  import LolaLogo from "./LolaLogo.svelte";
  import Button from "./Button.svelte";
  import SidebarTriage from "./SidebarTriage.svelte";
  import SidebarProjects from "./SidebarProjects.svelte";
  import SidebarStatus from "./SidebarStatus.svelte";
  import SidebarVersion from "./SidebarVersion.svelte";
  import SidebarActivity from "./SidebarActivity.svelte";
  import SidebarRoster from "./SidebarRoster.svelte";
  import SidebarWaiting from "./SidebarWaiting.svelte";
  import Tabs from "./Tabs.svelte";

  const TABS = [
    { id: "sessions", label: "Sessions" },
    { id: "projects", label: "Projects" },
  ];

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
  <div class="drag flex min-w-0 items-center gap-2 overflow-hidden pr-3 pl-[82px]">
    <LolaLogo class="h-[18px] w-auto min-w-0 shrink" />
    <Button
      icon
      class="ml-auto opacity-0 transition-opacity group-hover/side:opacity-100 focus-visible:opacity-100"
      title="hide sidebar (b)"
      aria-label="Hide sidebar"
      onclick={() => nav.toggleSidebar()}>«</Button
    >
  </div>

  <!-- Both tabs stay mounted and the other one is hidden, so switching keeps each one's scroll
       and its folders. Projects is the old body, unmoved: folders and drag are untouched. -->
  <div class="grid min-h-0 min-w-0" style="grid-template-rows:auto minmax(0,1fr) auto auto">
    <Tabs tabs={TABS} active={nav.sidebarTab} quiet onSelect={(t) => nav.setSidebarTab(t === "projects" ? "projects" : "sessions")} />
    <div class="grid min-h-0 min-w-0 {nav.sidebarTab === 'sessions' ? '' : 'hidden'}" style="grid-template-rows:minmax(0,1fr) auto">
      <div class="min-h-0 overflow-auto"><SidebarRoster /></div>
      <SidebarWaiting />
    </div>
    <div class="grid min-h-0 min-w-0 {nav.sidebarTab === 'projects' ? '' : 'hidden'}" style="grid-template-rows:minmax(0,1fr) auto">
      <div class="min-h-0 overflow-auto"><SidebarTriage /><SidebarProjects /></div>
      <SidebarActivity />
    </div>
    <SidebarVersion />
  </div>

  <!-- 3. Utility row -->
  <SidebarStatus />
</aside>
