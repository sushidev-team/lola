<script lang="ts">
  // The Team view: the team hub's chores, who works them, what they shipped,
  // and the org's devices. Reads `hub` (polled only while this view is on
  // screen) and splits the board with the pure helpers in $lib/hubboard.
  //
  // Nothing here decides who may claim what. `local.claimable` / `local.reason`
  // are the daemon's answer for this Mac, a claim or release is re-checked by
  // the daemon when pressed, and a refusal is shown in the daemon's words.
  import { onDestroy, onMount } from "svelte";
  import Button from "$lib/components/Button.svelte";
  import Chevron from "$lib/components/Chevron.svelte";
  import HubSyncButton from "$lib/components/hub/HubSyncButton.svelte";
  import Disclosure from "$lib/components/Disclosure.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import Select from "$lib/components/Select.svelte";
  import TimeAgo from "$lib/components/TimeAgo.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { hub, connectionState } from "$lib/hub.svelte";
  import { nav } from "$lib/nav.svelte";
  import { store } from "$lib/store.svelte";
  import {
    boardSections,
    claimProjects,
    deliveryClass,
    deliveryLabel,
    deviceOnline,
    fixAllItems,
    groupByAdvisory,
    groupByRepo,
    hubReason,
    itemTitle,
    kindIcon,
    kindLabel,
    leaseLeft,
    localSession,
    onlineCount,
    packageLine,
    severityClass,
    severityCounts,
    severityFill,
    severityLabel,
    severityTally,
    workGroups,
    type BoardGroup,
    type WorkGroup,
  } from "$lib/hubboard";
  import type { HubBoardItem } from "@bindings/internal/protocol";
  import { ConfigService } from "@bindings/desktop";

  let release: (() => void) | null = null;
  /** Projects that take chores on this Mac; null = not read (or unreadable). */
  let choreProjects = $state<number | null>(null);
  onMount(() => {
    release = hub.watch({ board: true });
    ConfigService.HubProjects()
      .then((ps) => (choreProjects = (ps ?? []).filter((p) => p.hubChores).length))
      .catch(() => (choreProjects = null));
  });
  onDestroy(() => release?.());

  const st = $derived(hub.status);
  const conn = $derived(connectionState(st));
  const sections = $derived(boardSections(hub.items));
  const devices = $derived(hub.org?.devices ?? []);
  const online = $derived(onlineCount(devices));
  const orgName = $derived(st?.org || hub.org?.org || "Team hub");

  const toneCls: Record<string, string> = {
    good: "border-good/40 bg-good/10 text-good",
    bad: "border-bad/40 bg-bad/10 text-bad",
    warn: "border-warn/40 bg-warn/10 text-warn",
  };

  /** The project a person picked for an item several projects could run. */
  let picks = $state<Record<string, string>>({});
  /** The daemon's refusal of the last claim/release, per item. */
  let rowErrors = $state<Record<string, string>>({});

  const projectName = (p: string) => store.displayNameFor(p) || p;

  function openSettings() {
    nav.openOverlay("settings", "", "hub");
  }

  function setError(id: string, msg: string) {
    rowErrors = { ...rowErrors, [id]: msg };
  }

  function askClaim(it: HubBoardItem) {
    setError(it.id, "");
    const choices = claimProjects(it);
    const project = choices.length ? (picks[it.id] ?? "") : (it.local?.project ?? "");
    if (choices.length && !project) {
      setError(it.id, "Choose the project to run it in.");
      return;
    }
    const where = project ? projectName(project) : "its project";
    confirm.ask({
      title: "Claim this chore?",
      body: `Claim ${itemTitle(it)} and start a session in ${where}?`,
      detail: "The team sees this Mac working it. A coding agent starts in a new worktree and opens a pull request.",
      confirmLabel: "Claim",
      onConfirm: () => void claim(it, project),
    });
  }

  async function claim(it: HubBoardItem, project: string) {
    const r = await hub.claim(it.id, project);
    if (r.ok) store.setFlash(`Claimed ${itemTitle(it)} in ${projectName(r.data.project)}`);
    else setError(it.id, r.error);
  }

  function askRelease(it: HubBoardItem) {
    setError(it.id, "");
    confirm.ask({
      title: "Release this chore?",
      body: `Release ${itemTitle(it)} back to the team?`,
      detail: "Another device may claim it, and this Mac skips it for 24 hours. The session keeps running, but the hub stops hearing from it.",
      confirmLabel: "Release",
      onConfirm: () => void doRelease(it),
    });
  }

  // Available rows are grouped per project (the default) or per advisory. The
  // choice is a per-viewer convenience, remembered in this browser only.
  const GROUP_KEY = "lola.team.groupBy";
  function readGroupMode(): "project" | "advisory" {
    try {
      return localStorage.getItem(GROUP_KEY) === "advisory" ? "advisory" : "project";
    } catch {
      return "project";
    }
  }
  let groupMode = $state<"project" | "advisory">(readGroupMode());
  function setGroupMode(m: "project" | "advisory") {
    groupMode = m;
    try {
      localStorage.setItem(GROUP_KEY, m);
    } catch {
      // the choice just is not remembered
    }
  }
  const groups = $derived(groupMode === "project" ? groupByRepo(sections.available) : groupByAdvisory(sections.available));
  let expanded = $state<Record<string, boolean>>({});
  const working = $derived(workGroups(sections.mine, sections.teammates, st));
  const finishedCount = $derived(sections.done.length + sections.failed.length + sections.escalated.length);
  const toggle = (key: string) => (expanded = { ...expanded, [key]: !expanded[key] });

  function askReleaseGroup(g: WorkGroup) {
    setError(g.key, "");
    if (g.items.length === 1) return askRelease(g.items[0]);
    confirm.ask({
      title: "Release these chores?",
      body: `Release ${g.items.length} chores back to the team?`,
      detail: "Another device may claim them, and this Mac skips them for 24 hours. The session keeps running, but the hub stops hearing from it.",
      confirmLabel: "Release all",
      onConfirm: () => void releaseGroup(g),
    });
  }

  async function releaseGroup(g: WorkGroup) {
    let done = 0;
    for (const it of g.items) {
      const r = await hub.release(it.id);
      if (r.ok) done++;
      else setError(g.key, r.error);
    }
    if (done) store.setFlash(`Released ${done} ${done === 1 ? "chore" : "chores"}`);
  }

  /** A project group's name: this Mac's project for the repository, else the repository. */
  function groupName(g: BoardGroup): string {
    if (groupMode === "advisory") return g.key;
    const p = g.items.find((it) => it.local?.project)?.local?.project;
    return p ? projectName(p) : g.key || "Unknown repository";
  }

  function askFixAll(g: BoardGroup) {
    const gid = `group:${g.key}`;
    setError(gid, "");
    const items = fixAllItems(g);
    if (items.length === 0) return;
    if (items.length === 1) return askClaim(items[0]);
    const choices = claimProjects(items[0]);
    const project = choices.length ? (picks[gid] ?? "") : (items[0].local?.project ?? "");
    if (choices.length && !project) {
      setError(gid, "Choose the project to run it in.");
      return;
    }
    const where = project ? projectName(project) : "its project";
    confirm.ask({
      title: "Fix all advisories?",
      body: `Start one agent for ${items.length} advisories in ${where}?`,
      detail: "Each advisory is claimed for this Mac. The agent fixes them together and opens one pull request.",
      confirmLabel: "Fix all",
      onConfirm: () => void fixAll(g, items, project),
    });
  }

  async function fixAll(g: BoardGroup, items: HubBoardItem[], project: string) {
    const r = await hub.claimMany(`group:${g.key}`, items.map((it) => it.id), project);
    if (r.ok) store.setFlash(`Claimed ${items.length} advisories in ${projectName(r.data.project)}`);
    else setError(`group:${g.key}`, r.error);
  }

  async function doRelease(it: HubBoardItem) {
    const r = await hub.release(it.id);
    if (r.ok) store.setFlash(r.data.message || `Released ${itemTitle(it)}`, r.data.queued ? "warn" : "good");
    else setError(it.id, r.error);
  }
</script>

{#snippet sevBar(items: HubBoardItem[], cls = "")}
  {@const tally = severityTally(items)}
  <span class="flex h-1.5 overflow-hidden rounded-full bg-edge/50 {cls}" role="img" aria-label={severityCounts(items) || "No severity"}>
    {#each tally as t (t.severity)}
      <span class="h-full {severityFill(t.severity)}" style:flex-grow={t.count}></span>
    {/each}
  </span>
{/snippet}

{#snippet sevChip(sev: string | undefined)}
  {#if sev}
    <span class="inline-flex shrink-0 items-center rounded-full border px-2 text-sm {severityClass(sev)}" data-severity={sev.toLowerCase()}>{severityLabel(sev)}</span>
  {/if}
{/snippet}

{#snippet rowError(it: HubBoardItem)}
  {#if rowErrors[it.id]}<p role="alert" class="col-span-full min-w-0 break-words text-sm text-bad">{hubReason(rowErrors[it.id])}</p>{/if}
{/snippet}

<!-- One advisory per line, columns aligned across the group:
     severity · id · package from → to · where · age · action. -->
{#snippet itemRow(it: HubBoardItem, where: string, action: import("svelte").Snippet<[HubBoardItem]> | null, note = "")}
  {@const place = where || (it.reporters > 1 ? `${it.reporters} reporters` : "")}
  <li class="team-row grid min-w-0 items-center gap-x-4 gap-y-1 py-2 pl-10 pr-3 hover:bg-panel/60" data-board-item={it.id}>
    <span class="flex w-[5.5rem] shrink-0">{@render sevChip(it.severity)}</span>
    <span class="flex min-w-0 items-center gap-1.5">
      <span class="shrink-0 text-faint" role="img" aria-label={kindLabel(it.kind)} title={kindLabel(it.kind)}><Icon name={kindIcon(it.kind)} size={13} /></span>
      <span class="min-w-0 truncate text-ink" data-item-title>{itemTitle(it)}</span>
    </span>
    <span class="min-w-0 truncate font-mono text-sm text-ink-2" title={packageLine(it)}>{packageLine(it)}</span>
    <span class="min-w-0 truncate text-sm text-faint" title={place}>{place}</span>
    <span class="hidden text-sm text-faint xl:inline"><TimeAgo at={it.firstSeen} hint="First reported" /></span>
    <span class="flex shrink-0 items-center justify-end gap-2">{#if action}{@render action(it)}{/if}</span>
    {#if note}<span class="col-span-full text-sm text-faint" data-claim-reason>{note}</span>{/if}
    {@render rowError(it)}
  </li>
{/snippet}

{#snippet claimAction(it: HubBoardItem)}
  {@const choices = claimProjects(it)}
  {@const claimable = !!it.local?.claimable}
  {#if claimable && choices.length}
    <Select aria-label="Project for {itemTitle(it)}" bind:value={picks[it.id]}>
      <option value="">Choose a project</option>
      {#each choices as p (p)}<option value={p}>{projectName(p)}</option>{/each}
    </Select>
  {/if}
  <Button size="xs" disabled={!claimable || hub.busy !== ""} loading={hub.busy === `claim:${it.id}`}
    title={claimable ? `Claim ${itemTitle(it)}` : it.local?.reason || "Not claimable on this Mac"}
    onclick={() => askClaim(it)}>Claim</Button>
{/snippet}

{#snippet releaseAction(it: HubBoardItem)}
  <Button variant="danger" size="xs" disabled={hub.busy !== ""} loading={hub.busy === `release:${it.id}`} onclick={() => askRelease(it)}>Release…</Button>
{/snippet}

{#snippet resultAction(it: HubBoardItem)}
  {#if it.outcome?.delivery}
    <span class="rounded px-1.5 text-sm {deliveryClass(it.outcome.delivery)}" data-delivery={it.outcome.delivery}>{deliveryLabel(it.outcome.delivery)}</span>
  {/if}
  {#if it.outcome?.prUrl}
    <Button size="xs" icon aria-label="Pull request" title={it.outcome.prUrl} onclick={() => store.openURL(it.outcome!.prUrl)}><Icon name="git-pull-request" size={14} /></Button>
  {/if}
  {#if it.escalation?.url}
    <Button size="xs" title={it.escalation.url} onclick={() => store.openURL(it.escalation!.url!)}>
      <Icon name="external-link" size={14} /> {it.escalation.identifier || "Linear issue"}
    </Button>
  {:else if it.escalation?.pending}
    <span class="text-sm text-warn">Escalation pending</span>
  {/if}
{/snippet}

{#snippet finished(rows: HubBoardItem[], empty: string)}
  {#if rows.length === 0}
    <p class="px-1 py-2 text-sm text-faint">{empty}</p>
  {:else}
    <ul class="mt-1 divide-y divide-edge/40">
      {#each rows as it (it.id)}
        <li class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 py-2" data-board-item={it.id}>
          <span class="grid min-w-0">
            <span class="flex min-w-0 items-center gap-1.5">
              <span class="min-w-0 truncate text-sm text-ink" data-item-title>{itemTitle(it)}</span>
              {#if it.package}<span class="min-w-0 truncate font-mono text-sm text-faint">{it.package}</span>{/if}
            </span>
            <span class="min-w-0 truncate text-sm text-faint">{it.repo}</span>
          </span>
          <span class="flex shrink-0 items-center gap-1.5">{@render resultAction(it)}</span>
        </li>
      {/each}
    </ul>
  {/if}
{/snippet}

{#snippet card(label: string, value: number, sub: import("svelte").Snippet | null, attr: string)}
  <div class="grid min-w-0 content-start gap-1.5 rounded-xl border border-edge bg-panel/40 px-4 py-3" data-team-card={attr}>
    <span class="label text-faint">{label}</span>
    <span class="num text-2xl font-medium leading-none text-ink">{value}</span>
    {#if sub}<span class="min-w-0 text-sm text-faint">{@render sub()}</span>{/if}
  </div>
{/snippet}

<div class="grid h-full min-h-0 grid-rows-[auto_minmax(0,1fr)]" data-team-view>
  <div class="flex min-w-0 flex-wrap items-center gap-3 border-b border-edge px-5 py-3">
    <span class="min-w-0 truncate text-base font-medium text-ink">{hub.joined ? orgName : "Team"}</span>
    {#if hub.joined}
      <span class="inline-flex items-center rounded-full border px-2 py-0.5 text-sm {toneCls[conn.tone]}" data-connection={conn.tone}>{conn.label}</span>
      {#if hub.org}<span class="num text-sm text-faint" data-online>{online} of {devices.length} {devices.length === 1 ? "device" : "devices"} online</span>{/if}
    {/if}
    <span class="ml-auto flex shrink-0 items-center gap-2">
      {#if hub.joined}<HubSyncButton />{/if}
      <Button size="md" onclick={openSettings}><Icon name="settings" size={14} /> Team hub settings</Button>
    </span>
  </div>

  <div class="min-h-0 overflow-auto">
    <div class="mx-auto grid w-full max-w-[1480px] gap-6 px-5 py-5">
      {#if hub.outdated}
        <div role="alert" class="rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">{hub.statusError}</div>
      {:else if !hub.loaded}
        <p role="status" class="text-sm text-faint">Checking the hub…</p>
      {:else if hub.statusError && !st}
        <div role="alert" class="rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">
          The lola daemon is not answering. {hub.statusError}
        </div>
      {:else if !hub.joined}
        <section class="mx-auto grid max-w-[520px] justify-items-center gap-3 py-12 text-center" data-team-empty>
          <span class="text-faint"><Icon name="users" size={28} /></span>
          <h2 class="text-base font-medium text-ink">Share chores with your team</h2>
          <p class="text-sm text-faint">One Mac fixes each security advisory and failing dependency bump. The team sees the pull request.</p>
          <Button variant="primary" size="md" onclick={openSettings}>Set up Team hub</Button>
        </section>
      {:else}
        {#if st?.lastError}
          <p role="alert" class="min-w-0 break-words text-sm text-bad">{st.lastError}{#if st.lastErrorAt} · <TimeAgo at={st.lastErrorAt} />{/if}</p>
        {/if}
        {#if choreProjects === 0}
          <div class="flex min-w-0 flex-wrap items-center gap-3 rounded-lg border border-edge px-3 py-2.5 text-sm" data-no-chore-projects>
            <span class="text-faint"><Icon name="folder" size={16} /></span>
            <span class="min-w-0 flex-1 text-ink">No project on this Mac takes chores yet.</span>
            <Button size="sm" variant="secondary" onclick={openSettings}>Choose projects</Button>
          </div>
        {/if}
        {#if hub.boardError}<div role="alert" class="rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">{hub.boardError}</div>{/if}

        {#if hub.board}
          {#snippet openSub()}{#if sections.available.length}{@render sevBar(sections.available, "mt-1.5 w-full")}<span class="mt-1.5 block truncate">{severityCounts(sections.available)}</span>{:else}Caught up{/if}{/snippet}
          {#snippet mineSub()}{working.filter((g) => g.mine).length} {working.filter((g) => g.mine).length === 1 ? "session" : "sessions"}{/snippet}
          {#snippet theirsSub()}{new Set(sections.teammates.map((it) => it.lease?.deviceName)).size} {new Set(sections.teammates.map((it) => it.lease?.deviceName)).size === 1 ? "device" : "devices"}{/snippet}
          {#snippet doneSub()}{sections.failed.length} failed · {sections.escalated.length} escalated{/snippet}
          <div class="grid grid-cols-2 gap-3 lg:grid-cols-4" data-team-summary>
            {@render card("Open", sections.available.length, openSub, "open")}
            {@render card("This Mac", sections.mine.length, mineSub, "mine")}
            {@render card("Teammates", sections.teammates.length, theirsSub, "teammates")}
            {@render card("Done", sections.done.length, doneSub, "done")}
          </div>
        {/if}

        <div class="grid min-w-0 items-start gap-6 xl:grid-cols-[minmax(0,1fr)_340px]">
          <div class="grid min-w-0 gap-6">
            <section class="min-w-0 rounded-xl border border-edge" aria-labelledby="team-available">
              <header class="flex min-w-0 flex-wrap items-center gap-3 border-b border-edge px-4 py-3">
                <h2 id="team-available" class="text-base font-medium text-ink">Available <span class="num ml-1 text-faint">{sections.available.length}</span></h2>
                {#if sections.available.length > 0}
                  <span class="ml-auto flex items-center gap-0.5 rounded-lg border border-edge p-0.5" role="group" aria-label="Group available chores">
                    <Button size="xs" variant={groupMode === "project" ? "secondary" : "ghost"} aria-pressed={groupMode === "project"} onclick={() => setGroupMode("project")}>By project</Button>
                    <Button size="xs" variant={groupMode === "advisory" ? "secondary" : "ghost"} aria-pressed={groupMode === "advisory"} onclick={() => setGroupMode("advisory")}>By advisory</Button>
                  </span>
                {/if}
              </header>
              {#if !hub.board}
                <p class="px-4 py-4 text-sm text-faint">Loading the board…</p>
              {:else if sections.available.length === 0}
                <p class="flex items-center gap-2 px-4 py-6 text-sm text-faint" data-available-empty><Icon name="check" size={14} /> Nothing waiting. The team is caught up.</p>
              {:else}
                <ul class="divide-y divide-edge/50">
                  {#each groups as g (g.key)}
                    {@const gid = `group:${g.key}`}
                    {@const open = !!expanded[g.key]}
                    {@const fix = groupMode === "project" ? fixAllItems(g) : []}
                    {@const choices = fix.length > 1 ? claimProjects(fix[0]) : []}
                    <li class="min-w-0" data-board-group={g.key}>
                      <div class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-3 py-3">
                        <span class="flex min-w-0 items-center gap-3">
                          <Button size="sm" variant="ghost" aria-expanded={open} aria-controls={`${gid}-rows`} onclick={() => toggle(g.key)}>
                            <Chevron {open} />
                            <span class="min-w-0 truncate text-base font-medium text-ink" data-group-title>{groupName(g)}</span>
                          </Button>
                          <span class="grid min-w-0 gap-1">
                            <span class="flex min-w-0 items-center gap-2">
                              {@render sevChip(g.worst)}
                              <span class="num min-w-0 truncate text-sm text-faint" data-group-count>
                                {groupMode === "project" ? `${g.items.length} ${g.items.length === 1 ? "chore" : "chores"}` : `${g.items.length} ${g.items.length === 1 ? "repository" : "repositories"}`}{#if groupMode === "project" && g.items.length > 1}{` · ${severityCounts(g.items)}`}{/if}
                              </span>
                            </span>
                            {#if groupMode === "project" && groupName(g) !== g.key}<span class="min-w-0 truncate text-sm text-faint">{g.key}</span>{/if}
                          </span>
                        </span>
                        <span class="flex shrink-0 items-center gap-3">
                          {@render sevBar(g.items, "hidden w-28 md:flex")}
                          {#if choices.length}
                            <Select aria-label="Project for {groupName(g)}" bind:value={picks[gid]}>
                              <option value="">Choose a project</option>
                              {#each choices as p (p)}<option value={p}>{projectName(p)}</option>{/each}
                            </Select>
                          {/if}
                          {#if groupMode === "project"}
                            <Button variant="primary" size="sm" disabled={fix.length === 0 || hub.busy !== ""}
                              loading={hub.busy === `claim:${gid}` || (fix.length === 1 && hub.busy === `claim:${fix[0].id}`)}
                              title={fix.length ? `Start one agent for ${fix.length === 1 ? "this chore" : `${fix.length} advisories`}` : g.reason || "Not claimable on this Mac"}
                              onclick={() => askFixAll(g)}>{fix.length > 1 ? `Fix all ${fix.length}` : "Claim"}</Button>
                          {/if}
                        </span>
                        {#if g.reason}<p class="col-span-full flex items-center gap-1.5 pl-10 text-sm text-faint" data-group-reason><Icon name="lock" size={12} /> {g.reason}</p>{/if}
                        {#if rowErrors[gid]}<p role="alert" class="col-span-full min-w-0 break-words pl-10 text-sm text-bad">{hubReason(rowErrors[gid])}</p>{/if}
                      </div>
                      {#if open}
                        <ul id={`${gid}-rows`} class="divide-y divide-edge/30 border-t border-edge/40 bg-canvas/40 pb-1">
                          {#each g.items as it (it.id)}
                            {@render itemRow(it, groupMode === "project" ? "" : it.repo, claimAction, !it.local?.claimable && !g.reason ? (it.local?.reason ?? "") : "")}
                          {/each}
                        </ul>
                      {/if}
                    </li>
                  {/each}
                </ul>
              {/if}
            </section>

            <section class="min-w-0 rounded-xl border border-edge" aria-labelledby="team-progress">
              <header class="flex items-center gap-3 border-b border-edge px-4 py-3">
                <h2 id="team-progress" class="text-base font-medium text-ink">In progress <span class="num ml-1 text-faint">{sections.mine.length + sections.teammates.length}</span></h2>
              </header>
              {#if working.length === 0}
                <p class="px-4 py-6 text-sm text-faint">No one is working a chore right now.</p>
              {:else}
                <ul class="divide-y divide-edge/50">
                  {#each working as w (w.key)}
                    {@const open = !!expanded[w.key]}
                    {@const proj = w.items.find((it) => it.local?.project)?.local?.project}
                    <li class="min-w-0" data-work-group={w.key} data-mine={w.mine || undefined}>
                      <div class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-3 py-3">
                        <span class="flex min-w-0 items-center gap-3">
                          <Button size="sm" variant="ghost" aria-expanded={open} aria-controls={`${w.key}-rows`} onclick={() => toggle(w.key)}>
                            <Chevron {open} />
                            <span class="min-w-0 truncate text-base font-medium text-ink">{proj ? projectName(proj) : w.repo}</span>
                          </Button>
                          <span class="grid min-w-0 gap-1">
                            <span class="flex min-w-0 flex-wrap items-center gap-2 text-sm">
                              <span class="inline-flex items-center gap-1.5 rounded-full px-2 {w.mine ? 'bg-accent-fill text-accent-ink' : 'bg-pill-grey text-pill-grey-fg'}">
                                <span class="h-1.5 w-1.5 rounded-full {w.mine ? 'bg-accent' : 'bg-faint'}"></span>{w.mine ? "This Mac" : w.device}
                              </span>
                              <span class="num text-faint">{w.items.length} {w.items.length === 1 ? "chore" : "chores"}</span>
                              {#if w.expires}<span class="text-faint">· {leaseLeft(w.expires)}</span>{/if}
                            </span>
                            {#if proj}<span class="min-w-0 truncate text-sm text-faint">{w.repo}</span>{/if}
                          </span>
                        </span>
                        <span class="flex shrink-0 items-center gap-2">
                          {@render sevBar(w.items, "hidden w-28 md:flex")}
                          {#if w.mine}
                            {#if w.session}<Button size="sm" variant="secondary" onclick={() => nav.openSession(w.session)}><Icon name="terminal" size={14} /> Open session</Button>{/if}
                            <Button variant="danger" size="sm" disabled={hub.busy !== ""} onclick={() => askReleaseGroup(w)}>{w.items.length > 1 ? "Release all…" : "Release…"}</Button>
                          {/if}
                        </span>
                        {#if rowErrors[w.key]}<p role="alert" class="col-span-full min-w-0 break-words pl-10 text-sm text-bad">{hubReason(rowErrors[w.key])}</p>{/if}
                      </div>
                      {#if open}
                        <ul id={`${w.key}-rows`} class="divide-y divide-edge/30 border-t border-edge/40 bg-canvas/40 pb-1">
                          {#each w.items as it (it.id)}
                            {@render itemRow(it, "", w.mine ? releaseAction : null)}
                          {/each}
                        </ul>
                      {/if}
                    </li>
                  {/each}
                </ul>
              {/if}
            </section>
          </div>

          <aside class="grid min-w-0 gap-6">
            <section class="min-w-0 rounded-xl border border-edge" aria-labelledby="team-devices">
              <header class="flex items-center gap-3 border-b border-edge px-4 py-3">
                <h2 id="team-devices" class="text-base font-medium text-ink">Devices <span class="num ml-1 text-faint">{devices.length}</span></h2>
              </header>
              {#if hub.orgError}
                <p role="alert" class="px-4 py-3 text-sm text-warn">{hub.orgError}</p>
              {:else if !hub.org}
                <p class="px-4 py-3 text-sm text-faint">Loading devices…</p>
              {:else if devices.length === 0}
                <p class="px-4 py-3 text-sm text-faint">No devices have joined yet.</p>
              {:else}
                <ul class="divide-y divide-edge/40">
                  {#each devices as d (d.name)}
                    {@const up = deviceOnline(d)}
                    <li class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 px-4 py-2.5" data-device={d.name}>
                      <span class="grid min-w-0 gap-0.5">
                        <span class="flex min-w-0 items-center gap-2">
                          <span class="h-2 w-2 shrink-0 rounded-full {up ? 'bg-good' : 'bg-edge'}" role="img" aria-label={up ? "Online" : "Offline"}></span>
                          <span class="min-w-0 truncate text-ink">{d.name}</span>
                          {#if d.me}<span class="rounded bg-pill-grey px-1.5 text-sm text-pill-grey-fg">This Mac</span>{/if}
                        </span>
                        <span class="min-w-0 truncate pl-4 text-sm text-faint">
                          {#if d.me}Now{:else if d.lastSeen}<TimeAgo at={d.lastSeen} />{:else}Never{/if}{#if (d.agents ?? []).length} · {(d.agents ?? []).join(", ")}{/if}
                        </span>
                      </span>
                      <span class="grid justify-items-end">
                        <span class="num text-ink">{d.freeSlots}</span>
                        <span class="text-sm text-faint">free</span>
                      </span>
                    </li>
                  {/each}
                </ul>
              {/if}
            </section>

            <section class="min-w-0 rounded-xl border border-edge px-4 py-2" aria-label="Finished chores">
              <h2 class="pb-1 pt-1.5 text-base font-medium text-ink">Results <span class="num ml-1 text-faint">{finishedCount}</span></h2>
              <div class="grid gap-1">
                <Disclosure label="Done" hint={String(sections.done.length)}>
                  {@render finished(sections.done, "No finished chores yet.")}
                </Disclosure>
                <Disclosure label="Failed" hint={String(sections.failed.length)}>
                  {@render finished(sections.failed, "No failed chores.")}
                </Disclosure>
                <Disclosure label="Escalated" hint={String(sections.escalated.length)}>
                  {@render finished(sections.escalated, "Nothing escalated.")}
                </Disclosure>
              </div>
            </section>
          </aside>
        </div>
      {/if}
    </div>
  </div>
</div>

<style>
  /* severity · id · package · where · age · action */
  .team-row {
    grid-template-columns: 5.5rem minmax(9rem, 1.1fr) minmax(0, 1.4fr) minmax(0, 0.9fr) auto auto;
  }
  @media (min-width: 1280px) {
    .team-row {
      grid-template-columns: 5.5rem minmax(10rem, 1fr) minmax(0, 1.4fr) minmax(0, 0.9fr) 5rem auto;
    }
  }
</style>
