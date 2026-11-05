<script lang="ts">
  import { store, type ProjectInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import StatusPill from "$lib/components/StatusPill.svelte";
  import LivePulse from "$lib/components/LivePulse.svelte";
  import Button from "$lib/components/Button.svelte";
  import ProjectKnowledge from "$lib/components/ProjectKnowledge.svelte";
  import ProjectGoals from "$lib/components/ProjectGoals.svelte";
  import { projectActions, openSession, openShell } from "$lib/projectActions";
  import { sessionMenu } from "$lib/sessionmenu.svelte";

  const project = $derived<ProjectInfo | undefined>(store.projectByName(nav.project));
  const sessions = $derived(store.sessionsForProject(nav.project));
  const shown = $derived(sessions.slice(0, 6));
  const moreCount = $derived(Math.max(0, sessions.length - 6));

  // Inline "broadcast" prompt: one message to every live agent of the project.
  let broadcastOpen = $state(false);
  let broadcastText = $state("");
  let broadcasting = $state(false);
  // The last reply, shown under the prompt so the delivered/deferred split
  // outlives the flash.
  let broadcastResult = $state("");

  // No local back()/breadcrumb any more — MainTopBar owns both for every view.

  async function sendBroadcast() {
    const msg = broadcastText.trim();
    if (!msg || broadcasting) return;
    broadcasting = true;
    try {
      const r = await store.broadcast(nav.project, msg);
      if (r === undefined) return; // failed: keep the text so it can be retried
      broadcastResult = r.message || "";
      broadcastText = "";
    } finally {
      broadcasting = false;
    }
  }

</script>

<div class="h-full min-h-0 overflow-auto p-6">
  <div class="mx-auto grid max-w-6xl gap-6">
    {#if project}
      <header class="grid gap-3 border-b border-edge pb-5">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <h1 class="text-xl font-semibold text-ink">{store.displayNameFor(project.name)}</h1>
          <div class="flex items-center gap-2">
            <Button onclick={() => void openShell(project!.name)} disabled={!project.path}>Open shell here</Button>
            <Button variant="primary" onclick={() => nav.newSession(project!.name)} disabled={!project.path}>New session…</Button>
            <details class="relative">
              <summary class="cursor-pointer list-none rounded px-3 py-1.5 hover:bg-sel" aria-label="Project actions">⋯</summary>
              <div class="absolute right-0 z-20 grid w-56 rounded-lg border border-edge bg-panel p-1 shadow-xl">
                {#each projectActions(project).filter(a => !["open","new","shell","ticket","pr"].includes(a.id)) as a (a.id)}
                  <button class="rounded px-3 py-2 text-left text-sm hover:bg-sel disabled:opacity-40" disabled={!a.enabled} onclick={a.run}>{a.label}</button>
                {/each}
                <button class="rounded px-3 py-2 text-left text-sm hover:bg-sel" onclick={() => { broadcastOpen=!broadcastOpen; broadcastResult=""; }}>Broadcast</button>
                <button class="rounded px-3 py-2 text-left text-sm hover:bg-sel" onclick={() => nav.openOverlay("project",nav.project,"filter")}>Polls</button>
              </div>
            </details>
          </div>
        </div>
        <p class="break-all text-sm text-faint"><span class="font-mono">{project.defaultBranch || "Default branch"} · {project.path}</span> · {project.repo || "No GitHub repository"}</p>
        <div class="flex gap-3 text-sm text-faint"><span>{project.pollsEnabled ? "Automatic pickup on" : "Automatic pickup paused"}</span><span>{sessions.length} sessions</span>{#if project.needsYou}<span class="text-orange">{project.needsYou} need you</span>{/if}</div>
        {#if project.lastError}<p role="alert" class="text-sm text-bad">Poll error: {project.lastError}</p>{/if}
        {#if !project.agentOk && project.agentErr}<p class="text-sm text-warn">agent not ready: {project.agentErr}. Shell sessions are still available.</p>{/if}
      </header>
      {#if broadcastOpen}
        <form class="grid gap-2 border-b border-edge pb-4" onsubmit={(e) => { e.preventDefault(); void sendBroadcast(); }}>
          <label class="grid gap-2 text-sm">Message every live agent<textarea class="min-h-20 rounded border border-edge bg-panel p-3" bind:value={broadcastText} maxlength="4000" aria-label="Broadcast message" placeholder="e.g. Rebase on main before pushing…"></textarea></label>
          <div class="flex gap-2"><Button type="submit" loading={broadcasting} disabled={!broadcastText.trim()}>Send to agents</Button><Button onclick={() => broadcastOpen=false}>Cancel</Button></div>
          {#if broadcastResult}<p role="status" class="text-sm text-faint">{broadcastResult}</p>{/if}
        </form>
      {/if}
      <section aria-label="Project sessions">
        <div class="mb-3 flex items-center justify-between"><h2 class="text-lg">Sessions</h2><Button onclick={() => nav.goCockpit(nav.project)}>Open cockpit</Button></div>
        {#if sessions.length === 0}
          <div class="grid justify-items-start gap-3 border-y border-edge py-10">
            <p class="text-ink">No sessions yet</p><p class="text-sm text-faint">Open a shell in this checkout, start an agent, or pick work from Linear or GitHub.</p>
            <div class="flex flex-wrap gap-2"><Button variant="primary" disabled={!project.path} onclick={() => nav.newSession(nav.project)}>New session…</Button><Button disabled={!project.path} onclick={() => void openShell(nav.project)}>Open shell here</Button></div>
          </div>
        {:else}
          <div class="divide-y divide-edge border-y border-edge">
            {#each shown as s (s.id)}
              <button class="flex w-full items-center gap-3 px-2 py-4 text-left hover:bg-sel/50" onclick={() => openSession(s.id)} oncontextmenu={(e) => sessionMenu.open(s.id,e)}>
                <LivePulse agentState={s.agentState} />
                <span class="min-w-0 flex-1"><span class="block truncate font-medium">{s.title || s.branch || "Session"}</span><span class="block truncate text-sm text-faint">{s.issue ? s.issue + " · " : ""}{s.location === "main" ? "Main checkout" : s.branch || "Worktree"} · {s.agentState === "shell" ? "Shell" : s.agent}</span></span>
                <StatusPill agentState={s.agentState} inputReason={s.inputReason} delivery={s.delivery} status={s.status} />
                {#if s.prNumber > 0}<span class="text-sm text-magenta">#{s.prNumber}</span>{/if}
              </button>
            {/each}
            {#if moreCount}<Button block onclick={() => nav.goCockpit(nav.project)}>Show {moreCount} more</Button>{/if}
          </div>
        {/if}
        <div class="mt-4 flex gap-3"><Button onclick={() => nav.goTicketPicker(nav.project)}>Start a ticket</Button><Button disabled={!project.repoConfigured} title={!project.repoConfigured ? "set a GitHub repo to list PRs" : undefined} onclick={() => nav.goPRPicker(nav.project)}>Open a PR</Button></div>
      </section>
      <div class="grid gap-3"><ProjectGoals project={nav.project} /><ProjectKnowledge project={nav.project} /></div>
    {:else}<p class="text-faint">Project {nav.project || "(none)"} not found{store.alive ? "" : " — daemon offline"}.</p>{/if}
  </div>
</div>
