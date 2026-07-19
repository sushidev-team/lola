<script lang="ts">
  import { store, type ProjectInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import StatusPill from "$lib/components/StatusPill.svelte";

  const project = $derived<ProjectInfo | undefined>(store.projectByName(nav.project));
  const sessions = $derived(store.sessionsForProject(nav.project));
  const shown = $derived(sessions.slice(0, 6));
  const moreCount = $derived(Math.max(0, sessions.length - 6));

  // Inline "new worktree" prompt.
  let worktreeOpen = $state(false);
  let branch = $state("");
  let useAgent = $state(true);

  function back() {
    nav.goCockpit(nav.scoped ? nav.project : "");
  }

  function startWorktree() {
    const b = branch.trim();
    if (!b) return;
    void store.openManual({ project: nav.project, branch: b, agent: useAgent });
    worktreeOpen = false;
    branch = "";
    nav.goCockpit(nav.project);
  }

  function openSession(id: string) {
    nav.select(id);
    nav.goCockpit(nav.project);
  }

  type Action = {
    key: string;
    label: string;
    desc: string;
    enabled: boolean;
    hint?: string;
    run: () => void;
  };

  const actions = $derived<Action[]>([
    {
      key: "P",
      label: "Open a PR",
      desc: "pick an open pull request and launch an agent on it",
      enabled: !!project?.repoConfigured,
      hint: "set a GitHub repo to list PRs",
      run: () => nav.goPRPicker(nav.project),
    },
    {
      key: "T",
      label: "Start a ticket",
      desc: "pick a Linear issue and spawn a session for it",
      enabled: true,
      run: () => nav.goTicketPicker(nav.project),
    },
    {
      key: "W",
      label: "New worktree",
      desc: "branch off and open a fresh worktree (agent or shell)",
      enabled: true,
      run: () => (worktreeOpen = !worktreeOpen),
    },
    {
      key: "L",
      label: "Polls",
      desc: "edit the Linear filters that auto-spawn work",
      enabled: true,
      run: () => nav.openOverlay("poll", nav.project),
    },
    {
      key: "S",
      label: "Sessions",
      desc: "open the cockpit scoped to this project",
      enabled: true,
      run: () => nav.goCockpit(nav.project),
    },
    {
      key: "E",
      label: "Edit project",
      desc: "path, repo, agent and base-branch settings",
      enabled: true,
      run: () => nav.openOverlay("project", nav.project),
    },
  ]);
</script>

<div class="flex h-full min-h-0 flex-col p-4">
  <!-- header: back + breadcrumb -->
  <div class="mb-3 flex items-center gap-3">
    <button class="rounded px-2 py-1 text-xs text-faint hover:text-accent" onclick={back}>← back</button>
    <div class="text-sm text-faint">
      <button class="text-faint hover:text-accent" onclick={() => nav.goHome()}>lola</button>
      <span class="text-edge">▸</span>
      <span class="text-ink">{nav.project || "(no project)"}</span>
    </div>
  </div>

  <div class="min-h-0 flex-1 overflow-auto">
    <div class="mx-auto flex max-w-3xl flex-col gap-3">
      <!-- Status box -->
      <div class="rounded-[10px] border border-edge bg-panel/40 p-3">
        {#if project}
          <div class="font-mono text-[11px] text-faint">
            path <span class="text-ink">{project.path || "(unset)"}</span>
            <span class="text-edge"> · </span>repo <span class="text-ink">{project.repo || "(none)"}</span>
            <span class="text-edge"> · </span>agent <span class="text-ink">{project.agent}</span>
            <span class="text-edge"> · </span>base <span class="text-ink">{project.defaultBranch || "(default)"}</span>
          </div>

          <div class="mt-2 text-xs">
            {#if project.pollsEnabled > 0}
              <span class="text-good">● on</span>
            {:else}
              <span class="text-faint">○ paused</span>
            {/if}
          </div>

          <div class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
            <span class={project.agentOk ? "text-good" : "text-bad"}>{project.agentOk ? "✓" : "✗"} agent</span>
            <span class="text-edge">·</span>
            <span class="text-faint tabular-nums">{store.alive ? project.liveCounted : "—"} live</span>
            {#if project.needsYou > 0}
              <span class="text-edge">·</span><span class="text-orange tabular-nums">{project.needsYou} need you</span>
            {/if}
            {#if project.ciRed > 0}
              <span class="text-edge">·</span><span class="text-bad tabular-nums">{project.ciRed} ci-red</span>
            {/if}
          </div>

          {#if !project.agentOk && project.agentErr}
            <div class="mt-2 text-[11px] text-bad">agent not ready: {project.agentErr} — launch verbs disabled</div>
          {/if}
        {:else}
          <div class="text-xs text-faint">
            project <span class="font-mono text-ink">{nav.project || "(none)"}</span> not found{store.alive
              ? ""
              : " — daemon offline"}.
          </div>
        {/if}
      </div>

      <!-- Actions -->
      <div class="flex flex-col gap-1.5">
        {#each actions as a (a.key)}
          <div class="flex flex-col">
            <button
              class="group flex items-center gap-3 rounded-[10px] border border-edge px-3 py-2 text-left transition-colors {a.enabled
                ? 'hover:border-accent hover:bg-sel/50'
                : 'cursor-not-allowed opacity-40'}"
              disabled={!a.enabled}
              onclick={a.run}
            >
              <span
                class="flex h-5 w-5 shrink-0 items-center justify-center rounded border border-edge text-[11px] font-semibold {a.enabled
                  ? 'text-accent group-hover:border-accent'
                  : 'text-faint'}">{a.key}</span
              >
              <span class="min-w-0 flex-1">
                <span class="block text-xs font-medium text-ink">{a.label}</span>
                <span class="block truncate text-[11px] text-faint">{a.desc}</span>
              </span>
              {#if !a.enabled && a.hint}
                <span class="shrink-0 text-[11px] text-warn">{a.hint}</span>
              {/if}
            </button>

            {#if a.key === "W" && worktreeOpen}
              <div class="mt-1.5 ml-8 flex flex-wrap items-center gap-2 rounded-[10px] border border-edge/60 bg-panel/60 p-2">
                <input
                  class="w-56 rounded border border-edge bg-canvas px-2 py-1 font-mono text-xs text-ink outline-none focus:border-accent"
                  placeholder="branch name…"
                  bind:value={branch}
                  onkeydown={(e) => e.key === "Enter" && startWorktree()}
                />
                <span class="flex items-center gap-0.5 rounded border border-edge p-0.5 text-[11px]">
                  <button
                    class="rounded px-1.5 py-[1px]"
                    class:bg-accent={useAgent}
                    class:text-canvas={useAgent}
                    class:text-faint={!useAgent}
                    onclick={() => (useAgent = true)}>agent</button
                  >
                  <button
                    class="rounded px-1.5 py-[1px]"
                    class:bg-accent={!useAgent}
                    class:text-canvas={!useAgent}
                    class:text-faint={useAgent}
                    onclick={() => (useAgent = false)}>shell</button
                  >
                </span>
                <button
                  class="rounded bg-accent/20 px-3 py-1 text-xs text-accent hover:bg-accent/30 disabled:opacity-40"
                  disabled={!branch.trim()}
                  onclick={startWorktree}>start ›</button
                >
                <button class="px-2 py-1 text-xs text-faint hover:text-ink" onclick={() => (worktreeOpen = false)}
                  >cancel</button
                >
              </div>
            {/if}
          </div>
        {/each}
      </div>

      <!-- Live sessions strip -->
      <div class="rounded-[10px] border border-edge">
        <div class="flex items-center gap-2 border-b border-edge/60 px-3 py-1.5 text-xs font-semibold text-ink">
          <span>Live sessions</span><span class="text-faint">· {sessions.length}</span>
        </div>
        {#if sessions.length === 0}
          <div class="px-3 py-6 text-center text-xs text-faint">no live sessions in this project</div>
        {:else}
          <div class="divide-y divide-edge/30">
            {#each shown as s (s.id)}
              <button
                class="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs hover:bg-sel/50"
                onclick={() => openSession(s.id)}
              >
                <span class="shrink-0 font-mono text-[11px] text-faint">{s.issue || "—"}</span>
                <span class="min-w-0 flex-1 truncate text-ink">{s.title || s.branch || "(untitled)"}</span>
                <StatusPill status={s.status} />
                {#if s.prNumber > 0}<span class="shrink-0 text-[11px] text-magenta">#{s.prNumber}</span>{/if}
              </button>
            {/each}
            {#if moreCount > 0}
              <button
                class="w-full px-3 py-1.5 text-left text-[11px] text-faint hover:text-accent"
                onclick={() => nav.goCockpit(nav.project)}>… {moreCount} more</button
              >
            {/if}
          </div>
        {/if}
      </div>
    </div>
  </div>
</div>
