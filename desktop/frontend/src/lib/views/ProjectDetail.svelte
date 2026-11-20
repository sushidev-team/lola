<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import { store, type ProjectInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import Button from "$lib/components/Button.svelte";
  import MenuItem from "$lib/components/MenuItem.svelte";
  import ProjectGit from "$lib/components/ProjectGit.svelte";
  import { projectActions, openShell } from "$lib/projectActions";
  import { waitingQueue } from "$lib/queue";
  import { ssh } from "$lib/ssh.svelte";

  const waiting = $derived(nav.sidebarOpen ? 0 : waitingQueue(store.rosterSessions).length);
  const daemonOk = $derived(store.alive && store.status?.runtimeOk && store.status?.linearOk);
  // The Git workspace stays mounted (hidden, with its timers paused) after a switch to Code, so
  // coming back is instant. While hidden it holds the project it was last showing.
  let { active = true }: { active?: boolean } = $props();
  let heldProject = $state("");
  $effect(() => { if (active) heldProject = nav.project; });
  const current = $derived(active ? nav.project : heldProject);
  const project = $derived<ProjectInfo | undefined>(store.projectByName(current));
  // Daemon updates replace ProjectInfo objects. Keep the Git navigation input
  // scalar so unchanged identity does not retrigger its project-reset effect.
  const projectName = $derived(project?.name ?? "");
  let actionsMenu = $state<HTMLDetailsElement>();
  $effect(() => {
    current;
    if (actionsMenu) actionsMenu.open = false;
  });

  function runAction(action: () => void) {
    if (actionsMenu) actionsMenu.open = false;
    action();
  }
</script>

<div class="project-workspace">
  {#if project}
    {#snippet heading()}
      <div class="project-heading">
        {#if !nav.sidebarOpen}<Button icon aria-label="Show sidebar" title="Show sidebar (⌘B)" onclick={() => nav.toggleSidebar()}><Icon name="chevrons-right" size={16} /></Button>{/if}
        {#if waiting > 0}<Button size="xs" class="text-orange!" title={`${waiting} waiting on you (⌘J)`} aria-label={`${waiting} waiting on you`} onclick={() => ssh.nextWaiting()}><Icon name="circle-alert" size={15} />{waiting}</Button>{/if}
        {#if !nav.sidebarOpen && store.connected && !daemonOk}<Button icon class={store.alive ? "text-warn!" : "text-bad!"} aria-label={store.alive ? "Daemon degraded" : "Daemon down"} title={`${store.alive ? 'Daemon degraded' : 'Daemon down'} · Open doctor`} onclick={() => nav.openOverlay("doctor")}><Icon name="triangle-alert" size={16} /></Button>{/if}
        <Button icon aria-label="Back to previous view" title="Back to previous view (⌘⇧G)" onclick={() => nav.returnFromGit()}><Icon name="chevron-left" size={16} /></Button>
        <h1 title={`${store.displayNameFor(project!.name)} · ${project!.repo || project!.path}`} class="project-title">{store.displayNameFor(project!.name)}</h1>
      </div>
    {/snippet}
    {#snippet actions()}
      <div class="project-actions no-drag">
        <Button icon class="project-search" aria-label="Search commands" title="Search commands (⌘K)" onclick={() => nav.openOverlay("commands")}><Icon name="search" size={16} /></Button>
        <Button icon class="project-shell" aria-label="Open shell here" title="Open shell here" onclick={() => void openShell(project!.name)} disabled={!project!.path}><Icon name="terminal" size={16} /></Button>
        <Button variant="primary" class="project-new" aria-label="New session…" title="New session" onclick={() => nav.newSession(project!.name)} disabled={!project!.path}><Icon name="plus" size={16} /><span class="project-action-label">New session…</span></Button>
        <details class="relative" bind:this={actionsMenu}>
          <summary class="project-menu-trigger" aria-label="Project actions"><Icon name="ellipsis" size={18} /></summary>
          <div role="menu" class="absolute right-0 z-50 grid w-56 rounded-lg border border-edge bg-panel p-1 shadow-xl">
            <MenuItem disabled={!project!.path} onclick={() => runAction(() => void openShell(project!.name))}>Open shell here</MenuItem>
            <MenuItem onclick={() => runAction(() => nav.openOverlay("commands"))}>Search commands</MenuItem>
            {#each projectActions(project!).filter(a => !["open", "new", "shell"].includes(a.id)) as a (a.id)}
              <MenuItem disabled={!a.enabled} onclick={() => runAction(a.run)}>{a.label}</MenuItem>
            {/each}
            <MenuItem onclick={() => runAction(() => nav.openOverlay("project", current, "filter"))}>Polls</MenuItem>
          </div>
        </details>
        <Button icon class="project-aside-toggle" selected={nav.asideOpen} aria-pressed={nav.asideOpen} aria-label="Right sidebar" title={nav.asideOpen ? "Hide right sidebar (N)" : "Show right sidebar (N)"} onclick={() => nav.toggleAside()}><Icon name="panel-right" size={16} /></Button>
      </div>
    {/snippet}
    <ProjectGit project={projectName} {heading} {actions} {active} windowInset={!nav.sidebarOpen} />
  {:else}<p class="p-6 text-faint">Project {current || "(none)"} not found{store.alive ? "" : " — daemon offline"}.</p>{/if}
</div>

<style>
  .project-workspace { display:grid; height:100%; min-height:0; min-width:0; grid-template-columns:minmax(0,1fr); grid-template-rows:minmax(0,1fr); container-type:inline-size; }
  .project-heading { display:flex; align-items:center; gap:6px; min-width:0; flex:1; }
  .project-title { min-width:60px; max-width:280px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-size:15px; line-height:20px; font-weight:600; }
  .project-actions { display:flex; align-items:center; gap:4px; flex-shrink:0; }
  .project-actions :global(.project-new) { font-size:13px; }
  .project-menu-trigger { display:grid; height:28px; width:28px; place-items:center; list-style:none; border-radius:6px; cursor:pointer; color:var(--color-faint); }
  .project-menu-trigger::-webkit-details-marker { display:none; }
  .project-menu-trigger:hover { background:var(--color-sel); color:var(--color-ink); }
  @container (max-width:1100px) { .project-action-label { display:none; } .project-actions :global(.project-new) { width:28px; padding:0; } .project-title { max-width:220px; } }
  @container (max-width:950px) { .project-actions :global(.project-shell),.project-actions :global(.project-search) { display:none; } .project-title { max-width:180px; } }
</style>
