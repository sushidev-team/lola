<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import { nav } from "$lib/nav.svelte";
  import { reflowGridRows } from "$lib/reflow";
  import LolaLogo from "./LolaLogo.svelte";
  import Button from "./Button.svelte";
  import SidebarSessionTools from "./SidebarSessionTools.svelte";
  import SidebarResize from "./SidebarResize.svelte";
  let { width = 540, maxWidth = 760 }: { width?: number; maxWidth?: number } = $props();
  import SidebarProjects from "./SidebarProjects.svelte";
  import SidebarStatus from "./SidebarStatus.svelte";
  import SidebarVersion from "./SidebarVersion.svelte";
  import SidebarActivity from "./SidebarActivity.svelte";
  import SidebarRoster from "./SidebarRoster.svelte";
  import { toggleGitWorkspace } from "$lib/workspacenav";
  import { SIDEBAR_SPLIT } from "$lib/sidebarwidth";
  import Tabs from "./Tabs.svelte";

  let sessionsArea = $state<HTMLDivElement>();

  // Too narrow for two columns: Projects and Sessions become tabs over one column.
  const narrow = $derived(width < SIDEBAR_SPLIT);
  const TABS = [
    { id: "projects", label: "Projects" },
    { id: "sessions", label: "Sessions" },
  ];

  const MODES = [
    { id: "code", label: "Code" },
    { id: "git", label: "Git" },
  ];

  // Code and Git are two workspaces over the same projects. The switch sits beside the logo so it
  // is one reach away from anywhere; Git remembers where Code was (nav.returnFromGit).
  //
  // The switch answers first: the pill and the column layout follow the click at once, and the
  // (heavier) workspace change runs after that frame has painted, so it never holds the click back.
  let settle: ReturnType<typeof setTimeout>;
  const git = $derived(nav.gitMode);
  function pickMode(id: string) {
    if ((id === "git") === git) return;
    nav.pendingMode = id === "git" ? "git" : "code";
    requestAnimationFrame(() => setTimeout(() => {
      switchMode(id);
      // Nothing to open Git on (no repository): drop the loading shell at once.
      if (id === "git" && !nav.inGit) nav.pendingMode = null;
      // Hold the choice until the nav state agrees, so the pill never bounces back mid-load.
      clearTimeout(settle);
      settle = setTimeout(() => { nav.pendingMode = null; }, 400);
    }, 0));
  }
  function switchMode(id: string) {
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
    <div role="group" aria-label="Workspace" class="mode-switch ml-1 shrink-0" data-mode={git ? "git" : "code"}>
      <span class="mode-thumb" aria-hidden="true"></span>
      {#each MODES as m (m.id)}
        <button
          type="button"
          aria-pressed={(m.id === "git") === git}
          class="mode-btn"
          title={m.id === "git" ? "Git workspace (⌘⇧G)" : "Sessions and code"}
          onclick={() => pickMode(m.id)}><Icon name={m.id === "git" ? "git-branch" : "code"} size={13} /><span>{m.label}</span></button
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
  <div class="grid min-h-0 min-w-0" style="grid-template-rows:{narrow && !git ? 'auto ' : ''}minmax(0,1fr);grid-template-columns:{narrow || git ? 'minmax(0,1fr)' : 'minmax(168px,38%) minmax(0,1fr)'}">
    {#if narrow && !git}
      <Tabs tabs={TABS} active={nav.sidebarTab} quiet onSelect={(t) => (nav.sidebarTab = t === "projects" ? "projects" : "sessions")} />
    {/if}
    <div class="grid min-h-0 min-w-0 {git ? '' : narrow ? (nav.sidebarTab === 'projects' ? '' : 'hidden') : 'border-r border-edge'}" style="grid-template-rows:minmax(0,1fr) auto auto">
      <div class="min-h-0 overflow-auto"><SidebarProjects /></div>
      <SidebarActivity />
      <SidebarVersion />
    </div>
    <div bind:this={sessionsArea} class="grid min-h-0 min-w-0 {git || (narrow && nav.sidebarTab !== 'sessions') ? 'hidden' : ''}" style="grid-template-rows:auto minmax(0,1fr)" data-sidebar-sessions>
      <SidebarSessionTools contextTarget={sessionsArea} />
      <div class="min-h-0 overflow-auto"><SidebarRoster /></div>
    </div>
  </div>

  <!-- 3. Utility row -->
  <SidebarStatus />
  {#if !git}<SidebarResize {width} {maxWidth} />{/if}
</aside>

<style>
  .mode-switch { position:relative; display:grid; grid-template-columns:1fr 1fr; padding:2px; border-radius:9px; background:var(--color-panel); box-shadow:inset 0 0 0 1px color-mix(in srgb,var(--color-edge) 80%,transparent); }
  .mode-thumb { position:absolute; top:2px; bottom:2px; left:2px; width:calc(50% - 2px); border-radius:7px; background:linear-gradient(135deg,color-mix(in srgb,var(--color-accent) 85%,white),var(--color-accent)); box-shadow:0 1px 6px color-mix(in srgb,var(--color-accent) 45%,transparent); transition:transform 160ms cubic-bezier(.2,.9,.3,1.15),box-shadow 160ms; }
  .mode-switch[data-mode="git"] .mode-thumb { transform:translateX(100%); }
  .mode-btn { position:relative; z-index:1; display:flex; align-items:center; justify-content:center; gap:5px; height:22px; padding:0 9px; border-radius:7px; font-size:13px; color:var(--color-faint); }
  .mode-btn :global(svg) { transition:transform 200ms cubic-bezier(.2,.9,.3,1.15); }
  .mode-btn:hover { color:var(--color-ink); }
  .mode-btn[aria-pressed="true"] { color:var(--color-canvas); font-weight:600; }
  .mode-btn[aria-pressed="true"] :global(svg) { transform:scale(1.15) rotate(-8deg); }
  .mode-btn:focus-visible { outline:2px solid var(--color-accent); outline-offset:1px; }
  @media (prefers-reduced-motion:reduce) { .mode-thumb,.mode-btn,.mode-btn :global(svg) { transition:none; } }
</style>
