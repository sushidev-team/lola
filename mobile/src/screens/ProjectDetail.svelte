<script lang="ts">
  import { untrack } from "svelte";
  import Icon from "$lib/components/Icon.svelte";
  import type { IconName } from "$lib/icons";
  import { store, type SessionInfo } from "$lib/store.svelte";
  import LargeTitle from "@mobile/lib/components/LargeTitle.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import ListRow from "@mobile/lib/components/ListRow.svelte";
  import ProjectSections from "@mobile/lib/components/ProjectSections.svelte";
  import SectionHeader from "@mobile/lib/components/SectionHeader.svelte";
  import InboxRow from "@mobile/lib/components/InboxRow.svelte";
  import StatusChip from "@mobile/lib/components/StatusChip.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { copy, fill } from "@mobile/lib/copy";
  import {
    ActionRunner,
    SESSIONS_SHOWN,
    pollNow,
    projectActions,
    setPolling,
    type ActionId,
  } from "@mobile/lib/projects.svelte";
  import { router } from "@mobile/lib/router.svelte";
  import { rowModel } from "@mobile/lib/inbox.svelte";
  import { can } from "@mobile/lib/tiers.svelte";

  // One project: its facts, the actions a phone may take, its sessions. The
  // "New worktree" form is the `newSession` sheet, which reads this project
  // from the route.
  const name = $derived(router.top.name === "tab" ? (router.top.project ?? "") : "");
  const project = $derived(store.projectByName(name));
  const title = $derived(store.displayNameFor(name));
  const sessions = $derived<SessionInfo[]>(store.sessionsForProject(name));
  let showAll = $state(false);
  const shown = $derived(showAll ? sessions : sessions.slice(0, SESSIONS_SHOWN));
  const actions = $derived(projectActions(project));
  // The resolved agent: the project's own, else Claude (config.AgentForProject; the payload carries no workspace default).
  const agentName = $derived.by(() => {
    const kind = project?.agent || "claude";
    return copy.agents[kind as keyof typeof copy.agents] ?? kind;
  });

  const pollingOn = $derived((project?.pollsEnabled ?? 0) > 0);
  const runner = new ActionRunner();

  // Status chips in Sentence case; a zero count draws nothing.
  const chips = $derived.by(() => {
    if (!project) return [];
    const out: { id: string; label: string; tone: "grey" | "urgent" | "broken" }[] = [];
    const live = store.alive ? project.liveCounted : 0;
    if (live > 0) out.push({ id: "running", label: fill(copy.projects.chips.running, { n: live }), tone: "grey" });
    if (project.needsYou > 0) out.push({ id: "needs", label: fill(copy.projects.chips.needs, { n: project.needsYou }), tone: "urgent" });
    if (project.ciRed > 0) out.push({ id: "ciFailed", label: fill(copy.projects.chips.ciFailed, { n: project.ciRed }), tone: "broken" });
    return out;
  });

  // One "New session" row: the sheet's From PR / From ticket segments reach the pickers,
  // so the separate PR and ticket rows are not drawn. Immediate rows act at once.
  const NAV_ICON: Partial<Record<ActionId, IconName>> = {
    worktree: "plus",
  };
  const navActions = $derived(actions.filter((a) => a.id in NAV_ICON));
  const nowActions = $derived(actions.filter((a) => !(a.id in NAV_ICON) && a.id !== "prs" && a.id !== "tickets"));

  const NOTICE = {
    warn: "border-warn/40 bg-warn/10 text-warn",
    good: "border-good/40 bg-good/10 text-good",
  } as const;

  function act(id: ActionId): void {
    switch (id) {
      case "prs":
      case "tickets":
        router.replace({ name: "tab", tab: "projects", project: name, pick: id === "prs" ? "prs" : "tickets" });
        break;
      case "worktree":
        router.open("newSession");
        break;
      case "pollOnce":
        void runner.run(id, () => pollNow(name));
        break;
      case "polling":
        void runner.run(id, () => setPolling(name, !pollingOn));
        break;
    }
  }

  // A project switch clears the previous project's notice and expansion.
  $effect(() => {
    void name;
    untrack(() => {
      runner.clear();
      showAll = false;
    });
  });
</script>

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <LargeTitle title={title} back={{ label: copy.projects.back, onclick: () => router.back() }} />

  {#if project?.pathOk && can("projectGit")}
    <ProjectSections project={name} value="sessions" />
  {/if}

  <div class="grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)] content-start gap-4 overflow-y-auto overscroll-contain pb-4">
    {#if !project}
      <p class="px-8 py-12 text-center text-callout text-faint">
        {store.connected ? copy.projects.gone.body : copy.projects.connecting.status}
      </p>
    {:else}
      {#if runner.notice}
        <div
          class="mx-4 flex items-center gap-2 rounded-xl border pl-3.5 text-subhead {NOTICE[runner.notice.tone]}"
          role="status"
        >
          <span class="min-w-0 flex-1 py-2">{runner.notice.text}</span>
          <TouchButton
            icon
            aria-label="Dismiss"
            class={runner.notice.tone === "warn" ? "text-warn!" : "text-good!"}
            onclick={() => runner.clear()}><Icon name="x" size={14} /></TouchButton
          >
        </div>
      {/if}

      {#if chips.length > 0}
        <div class="flex flex-wrap gap-2 px-4">
          {#each chips as c (c.id)}
            <StatusChip label={c.label} tone={c.tone} />
          {/each}
        </div>
      {/if}

      {#if navActions.length > 0}
        <ListGroup>
          {#each navActions as a (a.id)}
            <ListRow
              label={a.label}
              detail={!a.enabled ? a.hint : undefined}
              icon={NAV_ICON[a.id]}
              chevron
              disabled={!a.enabled || runner.busy !== ""}
              onclick={() => act(a.id)}
            />
          {/each}
        </ListGroup>
      {/if}

      <SectionHeader title={copy.projects.sessions} count={sessions.length} />
      {#if sessions.length === 0}
        <p class="px-4 py-6 text-center text-callout text-faint">{copy.projects.noSessions.body}</p>
      {:else}
        <!-- The Inbox's own row: a leading state dot, the title, the issue (the project is the screen), age and chevron. -->
        <div>
          {#each shown as s (s.id)}
            <InboxRow
              model={rowModel(s)}
              projectLabel=""
              onopen={() => router.push({ name: "session", id: s.id, seg: "conversation" })}
            />
          {/each}
        </div>
        {#if !showAll && sessions.length > SESSIONS_SHOWN}
          <div class="p-4">
            <TouchButton wide onclick={() => (showAll = true)}>{copy.inbox.showAll}</TouchButton>
          </div>
        {/if}
      {/if}
      <ListGroup>
        <ListRow label={copy.projects.rows.agent} value={agentName} />
        {#if project.defaultBranch}<ListRow label={copy.projects.rows.base} value={project.defaultBranch} />{/if}
      </ListGroup>

      {#if nowActions.length > 0}
        <ListGroup>
          {#each nowActions as a (a.id)}
            <ListRow
              label={a.label}
              detail={!a.enabled ? a.hint : undefined}
              value={runner.busy === a.id ? "…" : undefined}
              disabled={!a.enabled || runner.busy !== ""}
              onclick={() => act(a.id)}
            />
          {/each}
        </ListGroup>
      {/if}

    {/if}
  </div>
</div>
