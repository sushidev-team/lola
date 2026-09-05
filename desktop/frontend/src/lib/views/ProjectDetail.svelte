<script lang="ts">
  import { store, type ProjectInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import StatusPill from "$lib/components/StatusPill.svelte";
  import LivePulse from "$lib/components/LivePulse.svelte";
  import Button from "$lib/components/Button.svelte";
  import Select from "$lib/components/Select.svelte";

  const project = $derived<ProjectInfo | undefined>(store.projectByName(nav.project));
  const sessions = $derived(store.sessionsForProject(nav.project));
  const shown = $derived(sessions.slice(0, 6));
  const moreCount = $derived(Math.max(0, sessions.length - 6));

  // Inline "new worktree" prompt.
  let worktreeOpen = $state(false);
  let branch = $state("");
  let useAgent = $state(true);
  let selectedAgent = $state("");

  // No local back()/breadcrumb any more — MainTopBar owns both for every view.

  async function startWorktree() {
    const b = branch.trim();
    if (!b) return;
    // openManual resolves to undefined on failure (store.act swallows the error
    // into a flash). Only tear the form down and navigate on success — a failed
    // spawn must keep the prompt and the typed branch so it can be retried.
    const r = await store.openManual({
      project: nav.project,
      branch: b,
      agent: useAgent,
      agentKind: useAgent && selectedAgent ? selectedAgent : undefined,
    });
    if (r === undefined) return;
    worktreeOpen = false;
    branch = "";
    selectedAgent = "";
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
      // Same overlay as "Edit project", deep-linked to its Filter tab — a
      // project IS the poll unit, so there is only one editor.
      run: () => nav.openOverlay("project", nav.project, "filter"),
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
      desc: "repo setup, Linear filter, labels and write-back",
      enabled: true,
      run: () => nav.openOverlay("project", nav.project, "repo"),
    },
  ]);
</script>

<!-- No header here any more: the back button and the "Projects ▸ Nori"
     breadcrumb are MainTopBar's, so this view only owns its content. -->
<div class="flex h-full min-h-0 flex-col p-4">
  <div class="min-h-0 flex-1 overflow-auto">
    <div class="mx-auto flex max-w-3xl flex-col gap-3">
      <!-- Status box -->
      <div class="rounded-[10px] border border-edge bg-panel/40 p-3">
        {#if project}
          <div class="selectable font-mono text-sm text-faint">
            path <span class="text-ink">{project.path || "(unset)"}</span>
            <span class="text-edge"> · </span>repo <span class="text-ink">{project.repo || "(none)"}</span>
            <span class="text-edge"> · </span>agent <span class="text-ink">{project.agent}</span>
            <span class="text-edge"> · </span>base <span class="text-ink">{project.defaultBranch || "(default)"}</span>
          </div>

          <div class="mt-2 text-sm">
            {#if project.pollsEnabled > 0}
              <span class="text-good">● on</span>
            {:else}
              <span class="text-faint">○ paused</span>
            {/if}
          </div>

          <div class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
            <span class={project.agentOk ? "text-good" : "text-bad"}>{project.agentOk ? "✓" : "✗"} agent</span>
            <span class="text-edge">·</span>
            <span class="num text-faint">{store.alive ? project.liveCounted : "—"} live</span>
            {#if project.needsYou > 0}
              <span class="text-edge">·</span><span class="num text-orange">{project.needsYou} need you</span>
            {/if}
            {#if project.ciRed > 0}
              <span class="text-edge">·</span><span class="num text-bad">{project.ciRed} ci-red</span>
            {/if}
          </div>

          {#if !project.agentOk && project.agentErr}
            <div class="mt-2 text-sm text-bad">agent not ready: {project.agentErr} — launch verbs disabled</div>
          {/if}
        {:else}
          <div class="text-sm text-faint">
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
                class="flex h-5 w-5 shrink-0 items-center justify-center rounded border border-edge text-sm font-medium {a.enabled
                  ? 'text-accent-ink group-hover:border-accent'
                  : 'text-faint'}">{a.key}</span
              >
              <span class="min-w-0 flex-1">
                <span class="block font-medium text-ink">{a.label}</span>
                <span class="block truncate text-sm text-faint">{a.desc}</span>
              </span>
              {#if !a.enabled && a.hint}
                <span class="shrink-0 text-sm text-warn">{a.hint}</span>
              {/if}
            </button>

            {#if a.key === "W" && worktreeOpen}
              <div class="mt-1.5 ml-8 flex flex-wrap items-center gap-2 rounded-[10px] border border-edge/60 bg-panel/60 p-2">
                <input
                  class="w-56 rounded border border-edge bg-canvas px-2 py-1.5 font-mono text-sm text-ink outline-none focus:border-accent placeholder:text-placeholder"
                  placeholder="branch name…"
                  bind:value={branch}
                  onkeydown={(e) => e.key === "Enter" && startWorktree()}
                />
                <span class="flex items-center gap-0.5 rounded-md border border-edge p-0.5">
                  <Button size="xs" selected={useAgent} onclick={() => (useAgent = true)}>Agent</Button>
                  <Button size="xs" selected={!useAgent} onclick={() => (useAgent = false)}>Shell</Button>
                </span>
                {#if useAgent}
                  <Select class="w-48 text-sm" bind:value={selectedAgent} aria-label="Coding agent">
                    <option value="">Project default ({project?.agent || "claude"})</option>
                    <option value="claude">claude</option>
                    <option value="codex">codex</option>
                    <option value="opencode">opencode</option>
                  </Select>
                {/if}
                <Button variant="primary" size="md" disabled={!branch.trim()} onclick={startWorktree}>
                  Start <span aria-hidden="true">›</span>
                </Button>
                <Button size="md" onclick={() => (worktreeOpen = false)}>Cancel</Button>
              </div>
            {/if}
          </div>
        {/each}
      </div>

      <!-- Live sessions strip -->
      <div class="rounded-[10px] border border-edge">
        <div class="flex items-baseline gap-2 border-b border-edge/60 px-3 py-2 text-lg text-ink">
          <span>Live sessions</span><span class="num text-sm text-faint">· {sessions.length}</span>
        </div>
        {#if sessions.length === 0}
          <div class="px-3 py-6 text-center text-sm text-faint">no live sessions in this project</div>
        {:else}
          <div class="divide-y divide-edge/30">
            {#each shown as s (s.id)}
              <button
                class="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-sel/50"
                onclick={() => openSession(s.id)}
              >
                <span class="shrink-0 font-mono text-sm text-faint">{s.issue || "—"}</span>
                <span class="min-w-0 flex-1 truncate text-ink">{s.title || s.branch || "(untitled)"}</span>
                <LivePulse agentState={s.agentState} />
                <StatusPill
                  agentState={s.agentState}
                  inputReason={s.inputReason}
                  delivery={s.delivery}
                  status={s.status}
                />
                <!-- Plain, not a PrBadge with onOpen: this row IS a <button>,
                     and a nested button is not parseable. -->
                {#if s.prNumber > 0}<span class="num shrink-0 text-sm text-magenta">#{s.prNumber}</span>{/if}
              </button>
            {/each}
            {#if moreCount > 0}
              <div class="p-1.5">
                <Button block onclick={() => nav.goCockpit(nav.project)}>Show {moreCount} more</Button>
              </div>
            {/if}
          </div>
        {/if}
      </div>
    </div>
  </div>
</div>
