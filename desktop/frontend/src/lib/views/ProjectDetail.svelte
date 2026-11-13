<script lang="ts">
  import { untrack, tick } from "svelte";
  import Icon from "$lib/components/Icon.svelte";
  import { store, sortSessions, type ProjectInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import StatusPill from "$lib/components/StatusPill.svelte";
  import LivePulse from "$lib/components/LivePulse.svelte";
  import Button from "$lib/components/Button.svelte";
  import MenuItem from "$lib/components/MenuItem.svelte";
  import TimeAgo from "$lib/components/TimeAgo.svelte";
  import ProjectKnowledge from "$lib/components/ProjectKnowledge.svelte";
  import ProjectGoals from "$lib/components/ProjectGoals.svelte";
  import ProjectGit from "$lib/components/ProjectGit.svelte";
  import { projectActions, openSession, openShell } from "$lib/projectActions";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { waitingQueue } from "$lib/queue";
  import { ssh } from "$lib/ssh.svelte";
  import { fmtTokens } from "$lib/usage";

  const waiting = $derived(nav.sidebarOpen ? 0 : waitingQueue(store.rosterSessions).length);
  const daemonOk = $derived(store.alive && store.status?.runtimeOk && store.status?.linearOk);
  const project = $derived<ProjectInfo | undefined>(store.projectByName(nav.project));
  // Daemon updates replace ProjectInfo objects. Keep the Git navigation input
  // scalar so unchanged identity does not retrigger its project-reset effect.
  const projectName = $derived(project?.name ?? "");
  const sessions = $derived(sortSessions(store.rosterSessions).filter(s => s.project === nav.project));
  const shown = $derived(sessions.slice(0, 8));
  const moreCount = $derived(Math.max(0, sessions.length - shown.length));
  const members = $derived(store.projects.filter(p => p.stack === nav.project));
  const isStack = $derived(project?.kind === "stack");
  const spend = $derived(store.status?.usage?.projects?.find(p => p.name === nav.project));
  const canGit = $derived(!!project?.path && !isStack);
  const tab = $derived(canGit ? nav.projectTab : "overview");
  $effect(() => { if (canGit && tab === "git") nav.rememberGitProject(projectName); });

  let broadcastOpen = $state(false);
  let broadcastText = $state("");
  let broadcasting = $state(false);
  let broadcastResult = $state("");
  let actionsMenu = $state<HTMLDetailsElement>();
  let generation = 0;
  $effect(() => {
    nav.project;
    untrack(() => {
      generation++;
      broadcastOpen = false;
      broadcastText = "";
      broadcastResult = "";
      broadcasting = false;
      if (actionsMenu) actionsMenu.open = false;
    });
    return () => { generation++; };
  });

  function runAction(action: () => void) {
    if (actionsMenu) actionsMenu.open = false;
    action();
  }
  function showSessions() {
    nav.setTriage("");
    nav.sidebarQuery = "";
    nav.goCockpit(nav.project);
  }
  async function sendBroadcast() {
    const msg = broadcastText.trim();
    if (!msg || broadcasting) return;
    const current = generation;
    broadcasting = true;
    try {
      const r = await store.broadcast(nav.project, msg);
      if (r === undefined || current !== generation) return;
      broadcastResult = r.message || "";
      broadcastText = "";
    } finally {
      if (current === generation) broadcasting = false;
    }
  }
  async function selectTab(next: "overview" | "git") {
    nav.projectTab = next;
    // The unified toolbar moves into the selected panel; focus its new tab.
    await tick();
    document.getElementById(`project-tab-${next}`)?.focus();
  }
  function tabKey(e: KeyboardEvent) {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key) || !canGit) return;
    e.preventDefault();
    const next = e.key === "Home" ? "overview" : e.key === "End" ? "git" : tab === "overview" ? "git" : "overview";
    void selectTab(next);
  }
</script>

<div class="project-workspace" class:git-tab={tab === "git"}>
  {#if project}
    {#snippet heading()}
      <div class="project-heading">
        {#if !nav.sidebarOpen}<Button icon aria-label="Show sidebar" title="Show sidebar (⌘B)" onclick={() => nav.toggleSidebar()}><Icon name="chevrons-right" size={16} /></Button>{/if}
        {#if waiting > 0}<Button size="xs" class="text-orange!" title={`${waiting} waiting on you (⌘J)`} aria-label={`${waiting} waiting on you`} onclick={() => ssh.nextWaiting()}><Icon name="circle-alert" size={15} />{waiting}</Button>{/if}
        {#if !nav.sidebarOpen && store.connected && !daemonOk}<Button icon class={store.alive ? "text-warn!" : "text-bad!"} aria-label={store.alive ? "Daemon degraded" : "Daemon down"} title={`${store.alive ? 'Daemon degraded' : 'Daemon down'} · Open doctor`} onclick={() => nav.openOverlay("doctor")}><Icon name="triangle-alert" size={16} /></Button>{/if}
        <Button icon aria-label={tab === "git" ? "Back to previous view" : "All projects"} title={tab === "git" ? "Back to previous view (⌘⇧G)" : "All projects"} onclick={() => tab === "git" ? nav.returnFromGit() : nav.goHome()}><Icon name="chevron-left" size={16} /></Button>
        <h1 title={`${store.displayNameFor(project!.name)} · ${project!.repo || project!.path}`} class="project-title">{store.displayNameFor(project!.name)}</h1>
        <div class="project-tabs no-drag" role="tablist" tabindex="-1" aria-label="Project workspace" onkeydown={tabKey}>
          <Button id="project-tab-overview" class="project-tab" role="tab" aria-label="Overview" title="Project overview" aria-selected={tab === "overview"} aria-controls="project-overview" tabindex={tab === "overview" ? 0 : -1} selected={tab === "overview"} onclick={() => void selectTab("overview")}><Icon name="panels-top-left" size={15} /><span class="tab-label">Overview</span></Button>
          {#if canGit}<Button id="project-tab-git" class="project-tab" role="tab" aria-label="Git" title="Git workspace (⌘⇧G to switch back)" aria-selected={tab === "git"} aria-controls="project-git" tabindex={tab === "git" ? 0 : -1} selected={tab === "git"} onclick={() => void selectTab("git")}><Icon name="git-branch" size={15} /><span class="tab-label">Git</span></Button>{/if}
        </div>
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
            {#each projectActions(project!).filter(a => !["open", "new", "shell", "ticket", "pr"].includes(a.id)) as a (a.id)}
              <MenuItem disabled={!a.enabled} onclick={() => runAction(a.run)}>{a.label}</MenuItem>
            {/each}
            <MenuItem onclick={() => runAction(() => { nav.projectTab = "overview"; broadcastOpen = !broadcastOpen; broadcastResult = ""; })}>Broadcast</MenuItem>
            <MenuItem onclick={() => runAction(() => nav.openOverlay("project", nav.project, "filter"))}>Polls</MenuItem>
          </div>
        </details>
      </div>
    {/snippet}
    {#if tab === "git"}
      <div id="project-git" class="min-h-0 min-w-0" role="tabpanel" aria-labelledby="project-tab-git">
        <ProjectGit project={projectName} {heading} {actions} windowInset={!nav.sidebarOpen} />
      </div>
    {:else}
      <header class="project-header drag" class:window-inset={!nav.sidebarOpen} aria-label="Project toolbar">{@render heading()}{@render actions()}</header>
      <div id="project-overview" class="project-overview" role="tabpanel" aria-labelledby="project-tab-overview">
        <div class="overview-content">
          {#if !store.alive}<p class="text-sm text-faint" role="status">Daemon offline. Session information is from the last update.</p>{/if}
          {#if project.lastError}<p role="alert" class="text-sm text-bad">Poll error: {project.lastError}</p>{/if}
          {#if !project.agentOk && project.agentErr}<p class="text-sm text-warn">agent not ready: {project.agentErr}. Shell sessions are still available.</p>{/if}
          <dl class="project-stats" aria-label="Project statistics">
            <div><dt>Sessions</dt><dd>{sessions.length}</dd></div>
            <div><dt>Occupying slots</dt><dd>{project.liveCounted ?? 0}</dd></div>
            <div><dt>Need you</dt><dd class:text-orange={project.needsYou > 0}>{project.needsYou ?? 0}</dd></div>
            <div><dt>Open PRs</dt><dd>{project.openPrs ?? 0}</dd></div>
            {#if spend}<div><dt>Tokens today</dt><dd title={`${spend.tokens.toLocaleString()} tokens`}>{fmtTokens(spend.tokens)}</dd></div>{/if}
          </dl>
          {#if broadcastOpen}
            <form class="broadcast" onsubmit={(e) => { e.preventDefault(); void sendBroadcast(); }}>
              <label class="grid gap-2 text-sm">Message every live agent<textarea class="min-h-20 rounded border border-edge bg-panel p-3" bind:value={broadcastText} maxlength="4000" aria-label="Broadcast message" placeholder="What should everyone working on this project know?"></textarea></label>
              <div class="flex gap-2"><Button type="submit" loading={broadcasting} disabled={!broadcastText.trim()}>Send to agents</Button><Button onclick={() => broadcastOpen = false}>Cancel</Button></div>
              {#if broadcastResult}<p role="status" class="text-sm text-faint">{broadcastResult}</p>{/if}
            </form>
          {/if}
          <div class="overview-columns">
            <div class="overview-main">
              <section aria-label="Project sessions">
                <div class="section-heading"><h2>Sessions <span class="text-faint font-normal">{sessions.length}</span></h2><Button onclick={showSessions}>View all sessions<Icon name="arrow-right" size={14} /></Button></div>
                {#if sessions.length === 0}
                  <div class="sessions-empty">
                    <Icon name="terminal" size={24} class="text-faint" />
                    <h3 class="font-medium text-ink">No sessions yet</h3>
                    <p class="max-w-md text-sm text-faint">Start an agent, open a shell, or pick work from Linear or GitHub. Sessions and their progress will appear here.</p>
                    <Button variant="secondary" disabled={!project.path} onclick={() => nav.newSession(nav.project)}>New session…</Button>
                  </div>
                {:else}
                  <div class="session-list">
                    {#each shown as s (s.id)}
                      <Button variant="bare" block class="project-session" onclick={() => openSession(s.id)} oncontextmenu={(e) => sessionMenu.open(s.id, e)}>
                        <LivePulse agentState={s.agentState} />
                        <span class="min-w-0 flex-1 text-left">
                          <span class="block truncate font-medium">{s.title || s.branch || "Session"}</span>
                          <span class="block truncate text-sm text-faint">{s.issue ? s.issue + " · " : ""}{s.location === "main" ? "Main checkout" : s.branch || "Worktree"} · {s.agentState === "shell" ? "Shell" : s.agent}</span>
                        </span>
                        <span class="session-status"><StatusPill agentState={s.agentState} inputReason={s.inputReason} delivery={s.delivery} status={s.status} />{#if s.prNumber > 0}<span class="text-sm text-magenta">#{s.prNumber}</span>{/if}</span>
                        <Icon name="chevron-right" size={14} class="text-faint" />
                      </Button>
                    {/each}
                    {#if moreCount}<Button block onclick={showSessions}>Show {moreCount} more sessions<Icon name="arrow-right" size={14} /></Button>{/if}
                  </div>
                {/if}
                <div class="session-shortcuts"><Button onclick={() => nav.goTicketPicker(nav.project)}>Start a ticket</Button><Button disabled={!project.repoConfigured} title={!project.repoConfigured ? "set a GitHub repo to list PRs" : undefined} onclick={() => nav.goPRPicker(nav.project)}>Open a PR</Button></div>
              </section>
              {#if isStack}
                <section aria-label="Stack repositories">
                  <div class="section-heading"><h2>Repositories</h2></div>
                  {#each members as member (member.name)}
                    <Button block variant="bare" class="repository-row" onclick={() => nav.goDetail(member.name, "git")}>
                      <Icon name="git-branch" size={16} class="text-faint" />
                      <span class="min-w-0 flex-1 text-left"><span class="block truncate font-medium">{store.displayNameFor(member.name)}</span><span class="block truncate text-sm text-faint">{member.repo || member.path}</span></span>
                      <Icon name="chevron-right" size={14} class="text-faint" />
                    </Button>
                  {:else}<p class="text-sm text-faint">Add repositories to this stack from the project sidebar.</p>{/each}
                </section>
              {/if}
              <div class="grid gap-3"><ProjectGoals project={nav.project} /><ProjectKnowledge project={nav.project} /></div>
            </div>
            <aside class="project-facts" aria-label="Project information">
              {#if !isStack}
                <section>
                  <div class="section-heading"><h2>Repository</h2>{#if canGit}<Button size="xs" onclick={() => void selectTab("git")}>Open Git<Icon name="arrow-right" size={14} /></Button>{/if}</div>
                  <dl class="facts-list">
                    <div><dt>Default branch</dt><dd class="flex items-center gap-1.5"><Icon name="git-branch" size={14} />{project.defaultBranch || "Not configured"}</dd></div>
                    <div><dt>Checkout</dt><dd class="break-all">{project.path || "Not configured"}</dd></div>
                    <div><dt>GitHub</dt><dd class="break-all">{project.repo || "Not configured"}</dd></div>
                  </dl>
                </section>
              {/if}
              <section>
                <div class="section-heading"><h2>Automation</h2><Button icon size="xs" aria-label="Configure automation" onclick={() => nav.openOverlay("project", nav.project, "filter")}><Icon name="settings" size={14} /></Button></div>
                <dl class="facts-list">
                  <div><dt>Coding agent</dt><dd>{project.agent || "claude"}</dd></div>
                  <div><dt>Issue pickup</dt><dd>{project.pollsEnabled ? "Enabled" : "Paused"}</dd></div>
                  <div><dt>Last checked</dt><dd><TimeAgo at={project.lastRun} fallback="Not yet checked" /></dd></div>
                  {#if project.ciRed > 0}<div><dt>Failing CI</dt><dd class="text-bad">{project.ciRed} {project.ciRed === 1 ? "session" : "sessions"}</dd></div>{/if}
                </dl>
              </section>
            </aside>
          </div>
        </div>
      </div>
    {/if}
  {:else}<p class="p-6 text-faint">Project {nav.project || "(none)"} not found{store.alive ? "" : " — daemon offline"}.</p>{/if}
</div>

<style>
  .project-workspace { display:grid; height:100%; min-height:0; min-width:0; grid-template-columns:minmax(0,1fr); grid-template-rows:48px minmax(0,1fr); container-type:inline-size; }
  .project-workspace.git-tab { grid-template-rows:minmax(0,1fr); }
  .project-header { min-width:0; display:flex; align-items:center; gap:12px; height:48px; padding:0 12px; border-bottom:1px solid var(--color-edge); }
  .project-header.window-inset { padding-left:82px; }
  .project-heading { display:flex; align-items:center; gap:6px; min-width:0; flex:1; }
  .project-title { min-width:60px; max-width:280px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-size:15px; line-height:20px; font-weight:600; }
  .project-tabs { display:flex; align-items:center; gap:2px; flex-shrink:0; margin-left:10px; padding-left:12px; border-left:1px solid var(--color-edge); }
  .project-tabs :global(.project-tab) { height:30px; font-size:13px; padding-inline:9px; }
  .project-actions { display:flex; align-items:center; gap:4px; flex-shrink:0; }
  .project-actions :global(.project-new) { font-size:13px; }
  .project-menu-trigger { display:grid; height:28px; width:28px; place-items:center; list-style:none; border-radius:6px; cursor:pointer; color:var(--color-faint); }
  .project-menu-trigger::-webkit-details-marker { display:none; }
  .project-menu-trigger:hover { background:var(--color-sel); color:var(--color-ink); }
  @container (max-width:1100px) { .project-action-label { display:none; } .project-actions :global(.project-new) { width:28px; padding:0; } .project-title { max-width:220px; } }
  @container (max-width:950px) { .tab-label { display:none; } .project-tabs { margin-left:4px; padding-left:6px; } .project-tabs :global(.project-tab) { width:30px; padding:0; } .project-actions :global(.project-shell),.project-actions :global(.project-search) { display:none; } .project-title { max-width:180px; } }
  .project-overview { min-height: 0; overflow: auto; }
  .overview-content { display: grid; gap: 20px; max-width: 1440px; margin-inline: auto; padding: 20px; font-size:14px; }
  .project-stats { display: flex; flex-wrap: wrap; gap: 16px 36px; padding-bottom: 16px; border-bottom: 1px solid var(--color-edge); }
  .project-stats > div { display: grid; gap: 5px; }
  .project-stats dt { font-size: 13px; color: var(--color-faint); }
  .project-stats dd { font-size: 18px; line-height: 24px; font-weight: 500; font-variant-numeric: tabular-nums; }
  .overview-columns { display: grid; grid-template-columns: minmax(0, 1fr) minmax(210px, 0.4fr); gap: 24px; }
  .overview-main { display: grid; align-content: start; gap: 20px; min-width: 0; }
  .section-heading { display: flex; align-items: center; justify-content: space-between; min-height: 28px; gap: 12px; margin-bottom: 12px; }
  .section-heading h2 { font-size: 15px; line-height: 20px; font-weight: 600; }
  .section-heading h2 span { margin-left: 6px; font-size: 13px; }
  .session-list { display: grid; border-top: 1px solid var(--color-edge); }
  .session-list :global(.project-session) { height: auto; min-height: 56px; gap: 10px; padding: 8px; border-radius: 0; border-bottom: 1px solid var(--color-edge); }
  .session-list :global(.project-session:hover), :global(.repository-row:hover) { background: var(--color-sel); }
  .session-status { display: flex; flex-wrap: wrap; justify-content: end; align-items: center; gap: 6px; }
  .session-shortcuts { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 12px; }
  .sessions-empty { display: grid; justify-items: start; gap: 12px; border-block: 1px solid var(--color-edge); padding: 28px 4px; }
  .project-facts { display: grid; align-content: start; gap: 20px; min-width: 0; border-left: 1px solid var(--color-edge); padding-left: 24px; }
  .facts-list { display: grid; gap: 12px; }
  .facts-list > div { display: grid; gap: 4px; }
  .facts-list dt { color: var(--color-faint); font-size: 13px; }
  .facts-list dd { font-size: 14px; line-height: 20px; }
  .broadcast { display: grid; gap: 12px; padding-bottom: 24px; border-bottom: 1px solid var(--color-edge); }
  :global(.repository-row) { height: auto; min-height: 56px; gap: 12px; padding: 10px 8px; }
  @container (max-width: 780px) {
    .overview-content { padding: 20px 16px; }
    .overview-columns { grid-template-columns: minmax(0, 1fr); gap: 20px; }
    .project-facts { grid-template-columns: repeat(2, minmax(0, 1fr)); border-left: 0; border-top: 1px solid var(--color-edge); padding-left: 0; padding-top: 24px; }
  }
  @container (max-width: 450px) {
    .project-stats { gap: 16px 24px; }
    .project-facts { grid-template-columns: minmax(0, 1fr); }
    .session-status { max-width: 100px; }
  }
</style>
