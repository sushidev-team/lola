<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import { nav } from "$lib/nav.svelte";
  import { reflowGridRows } from "$lib/reflow";
  import LolaLogo from "./LolaLogo.svelte";
  import Button from "./Button.svelte";
  import SidebarSessionTools from "./SidebarSessionTools.svelte";
  import SidebarResize from "./SidebarResize.svelte";
  let { width = 320, maxWidth = 480 }: { width?: number; maxWidth?: number } = $props();
  import SidebarProjects from "./SidebarProjects.svelte";
  import SidebarStatus from "./SidebarStatus.svelte";
  import SidebarVersion from "./SidebarVersion.svelte";
  import SidebarActivity from "./SidebarActivity.svelte";
  import SidebarRoster from "./SidebarRoster.svelte";
  import { toggleGitWorkspace } from "$lib/workspacenav";

  let sessionsArea = $state<HTMLDivElement>();

  const MODES = [
    { id: "code", label: "Code" },
    { id: "git", label: "Git" },
  ];

  // Code and Git are two workspaces over the same projects. The switch sits beside the logo so it
  // is one reach away from anywhere; Git remembers where Code was (nav.returnFromGit).
  function pickMode(id: string) {
    if (id === "git") { if (!nav.inGit) toggleGitWorkspace(); }
    else if (nav.inGit) nav.returnFromGit();
    else if (nav.view !== "cockpit") nav.goCockpit(nav.scoped ? nav.project : "");
  }

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
  class="sidebar group/side relative grid h-full min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] overflow-hidden border-r border-edge bg-canvas"
  style="grid-template-rows:44px minmax(0,1fr) 44px"
  aria-label="Sidebar"
  inert={!nav.sidebarOpen}
  {@attach reflowGridRows}
>
  <!-- 1. Brand row + window drag band. The pl-[82px] clears the inset traffic
       lights (roughly x 13–73, y 10–26). NOTHING may be placed left of that
       padding, ever. Every control
       in this row is a real <button>, so app.css's `.no-drag, button, a, input…`
       rule opts it out of the drag region automatically — a clickable <div>
       here would be neither draggable nor reliably clickable. -->
  <div class="drag flex min-w-0 items-center gap-2 overflow-hidden pr-3 pl-[82px]">
    <LolaLogo class="h-[18px] w-auto min-w-0 shrink" />
    <div role="group" aria-label="Workspace" class="ml-1 flex shrink-0 items-center rounded-md bg-panel p-0.5">
      {#each MODES as m (m.id)}
        <button
          type="button"
          aria-pressed={(m.id === "git") === nav.inGit}
          class="h-5 rounded px-2 text-sm transition-colors {(m.id === 'git') === nav.inGit ? 'bg-sel font-medium text-ink' : 'text-faint hover:text-ink'}"
          title={m.id === "git" ? "Git workspace (⌘⇧G)" : "Sessions and code"}
          onclick={() => pickMode(m.id)}>{m.label}</button
        >
      {/each}
    </div>
    <Button
      icon
      class="ml-auto opacity-0 transition-opacity group-hover/side:opacity-100 focus-visible:opacity-100"
      title="hide sidebar (⌘B)"
      aria-label="Hide sidebar"
      onclick={() => nav.toggleSidebar()}><Icon name="chevrons-left" size={14} /></Button
    >
  </div>

  <!-- Projects on the left, the sessions of the chosen project (or all) beside them. Both are always
       mounted, so each keeps its scroll and folders. -->
  <div class="grid min-h-0 min-w-0" style="grid-template-columns:minmax(168px,38%) minmax(0,1fr)">
    <div class="grid min-h-0 min-w-0 border-r border-edge" style="grid-template-rows:minmax(0,1fr) auto auto">
      <div class="min-h-0 overflow-auto"><SidebarProjects /></div>
      <SidebarActivity />
      <SidebarVersion />
    </div>
    <div bind:this={sessionsArea} class="grid min-h-0 min-w-0" style="grid-template-rows:auto minmax(0,1fr)" data-sidebar-sessions>
      <SidebarSessionTools contextTarget={sessionsArea} />
      <div class="min-h-0 overflow-auto"><SidebarRoster /></div>
    </div>
  </div>

  <!-- 3. Utility row -->
  <SidebarStatus />
  <SidebarResize {width} {maxWidth} />
</aside>
