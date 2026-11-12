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
    severityLabel,
    type BoardGroup,
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

{#snippet headline(it: HubBoardItem)}
  <span class="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1">
    <span class="shrink-0 self-center text-faint" role="img" aria-label={kindLabel(it.kind)} title={kindLabel(it.kind)}>
      <Icon name={kindIcon(it.kind)} size={14} />
    </span>
    <span class="min-w-0 truncate font-medium text-ink" data-item-title>{itemTitle(it)}</span>
    {#if it.severity}
      <span class="inline-flex shrink-0 items-center rounded-full border px-2 text-sm {severityClass(it.severity)}" data-severity={it.severity.toLowerCase()}>
        {severityLabel(it.severity)}
      </span>
    {/if}
  </span>
{/snippet}

{#snippet facts(it: HubBoardItem)}
  <span class="flex min-w-0 flex-wrap items-baseline gap-x-1.5 text-sm text-faint">
    {#if packageLine(it)}<span class="font-mono text-ink-2">{packageLine(it)}</span><span aria-hidden="true">·</span>{/if}
    <span class="min-w-0 truncate">{it.repo}</span>
    {#if it.reporters > 0}<span aria-hidden="true">·</span><span>{it.reporters} {it.reporters === 1 ? "reporter" : "reporters"}</span>{/if}
    <span aria-hidden="true">·</span><TimeAgo at={it.firstSeen} hint="First reported" />
  </span>
{/snippet}

{#snippet availableRow(it: HubBoardItem, groupReason: string)}
  {@const choices = claimProjects(it)}
  {@const claimable = !!it.local?.claimable}
  <li class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 py-2.5 pl-9 pr-3" data-board-item={it.id}>
    <span class="grid min-w-0 gap-1">
      {@render headline(it)}
      {@render facts(it)}
      {#if !claimable && it.local?.reason && !groupReason}<span class="text-sm text-faint" data-claim-reason>{it.local.reason}</span>{/if}
    </span>
    <span class="flex shrink-0 items-center gap-2">
      {#if claimable && choices.length}
        <Select aria-label="Project for {itemTitle(it)}" bind:value={picks[it.id]}>
          <option value="">Choose a project</option>
          {#each choices as p (p)}<option value={p}>{projectName(p)}</option>{/each}
        </Select>
      {/if}
      <Button
        size="xs"
        disabled={!claimable || hub.busy !== ""}
        loading={hub.busy === `claim:${it.id}`}
        title={claimable ? `Claim ${itemTitle(it)}` : it.local?.reason || "Not claimable on this Mac"}
        onclick={() => askClaim(it)}>Claim</Button
      >
    </span>
    {@render rowError(it)}
  </li>
{/snippet}

{#snippet rowError(it: HubBoardItem)}
  {#if rowErrors[it.id]}<p role="alert" class="col-start-1 col-end-3 min-w-0 break-words text-sm text-bad">{hubReason(rowErrors[it.id])}</p>{/if}
{/snippet}

{#snippet finished(rows: HubBoardItem[], empty: string)}
  {#if rows.length === 0}
    <p class="px-1 py-2 text-sm text-faint">{empty}</p>
  {:else}
    <ul class="mt-2 divide-y divide-edge/40 rounded-[10px] border border-edge">
      {#each rows as it (it.id)}
        <li class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-3 py-2.5" data-board-item={it.id}>
          <span class="grid min-w-0 gap-1">{@render headline(it)}{@render facts(it)}</span>
          <span class="flex shrink-0 flex-wrap items-center justify-end gap-2">
            {#if it.outcome?.delivery}
              <span class="rounded px-1.5 text-sm {deliveryClass(it.outcome.delivery)}" data-delivery={it.outcome.delivery}>{deliveryLabel(it.outcome.delivery)}</span>
            {/if}
            {#if it.outcome?.prUrl}
              <Button size="xs" title={it.outcome.prUrl} onclick={() => store.openURL(it.outcome!.prUrl)}>
                <Icon name="git-pull-request" size={14} /> Pull request
              </Button>
            {/if}
            {#if it.escalation?.url}
              <Button size="xs" title={it.escalation.url} onclick={() => store.openURL(it.escalation!.url!)}>
                <Icon name="external-link" size={14} /> {it.escalation.identifier || "Linear issue"}
              </Button>
            {:else if it.escalation?.pending}
              <span class="text-sm text-warn">Escalation pending</span>
            {/if}
          </span>
        </li>
      {/each}
    </ul>
  {/if}
{/snippet}

<div class="grid h-full min-h-0 grid-rows-[auto_minmax(0,1fr)]" data-team-view>
  <div class="flex min-w-0 flex-wrap items-center gap-3 border-b border-edge px-4 py-3">
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
    <div class="mx-auto grid w-full max-w-[1040px] gap-8 px-4 py-5">
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

        <section aria-labelledby="team-available">
          <div class="mb-2 flex min-w-0 flex-wrap items-center gap-3">
            <h2 id="team-available" class="label text-faint">Available <span class="num">{sections.available.length}</span></h2>
            {#if sections.available.length > 0}
              <span class="ml-auto flex items-center gap-1" role="group" aria-label="Group available chores">
                <Button size="xs" variant={groupMode === "project" ? "secondary" : "ghost"} aria-pressed={groupMode === "project"} onclick={() => setGroupMode("project")}>By project</Button>
                <Button size="xs" variant={groupMode === "advisory" ? "secondary" : "ghost"} aria-pressed={groupMode === "advisory"} onclick={() => setGroupMode("advisory")}>By advisory</Button>
              </span>
            {/if}
          </div>
          {#if !hub.board}
            <p class="text-sm text-faint">Loading the board…</p>
          {:else if sections.available.length === 0}
            <p class="text-sm text-faint" data-available-empty>Nothing waiting. The team is caught up.</p>
          {:else}
            <ul class="divide-y divide-edge/40 rounded-[10px] border border-edge">
              {#each groups as g (g.key)}
                {@const gid = `group:${g.key}`}
                {@const open = !!expanded[g.key]}
                {@const fix = groupMode === "project" ? fixAllItems(g) : []}
                {@const choices = fix.length > 1 ? claimProjects(fix[0]) : []}
                <li class="min-w-0" data-board-group={g.key}>
                  <div class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-3 py-2.5">
                    <span class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                      <Button size="sm" variant="ghost" aria-expanded={open} aria-controls={`${gid}-rows`}
                        onclick={() => (expanded = { ...expanded, [g.key]: !open })}>
                        <Chevron {open} />
                        <span class="min-w-0 truncate font-medium text-ink" data-group-title>{groupName(g)}</span>
                      </Button>
                      {#if g.worst}
                        <span class="inline-flex shrink-0 items-center rounded-full border px-2 text-sm {severityClass(g.worst)}">{severityLabel(g.worst)}</span>
                      {/if}
                      <span class="num text-sm text-faint" data-group-count>
                        {groupMode === "project" ? `${g.items.length} ${g.items.length === 1 ? "chore" : "chores"}` : `${g.items.length} ${g.items.length === 1 ? "repository" : "repositories"}`}{#if groupMode === "project" && g.items.length > 1}{` · ${severityCounts(g.items)}`}{/if}
                      </span>
                      {#if groupMode === "project" && groupName(g) !== g.key}<span class="min-w-0 truncate text-sm text-faint">{g.key}</span>{/if}
                    </span>
                    <span class="flex shrink-0 items-center gap-2">
                      {#if choices.length}
                        <Select aria-label="Project for {groupName(g)}" bind:value={picks[gid]}>
                          <option value="">Choose a project</option>
                          {#each choices as p (p)}<option value={p}>{projectName(p)}</option>{/each}
                        </Select>
                      {/if}
                      {#if groupMode === "project"}
                        <Button variant="primary" size="xs" disabled={fix.length === 0 || hub.busy !== ""}
                          loading={hub.busy === `claim:${gid}` || (fix.length === 1 && hub.busy === `claim:${fix[0].id}`)}
                          title={fix.length ? `Start one agent for ${fix.length === 1 ? "this chore" : `${fix.length} advisories`}` : g.reason || "Not claimable on this Mac"}
                          onclick={() => askFixAll(g)}>{fix.length > 1 ? `Fix all ${fix.length}` : "Claim"}</Button>
                      {/if}
                    </span>
                    {#if g.reason}<p class="col-start-1 col-end-3 pl-9 text-sm text-faint" data-group-reason>{g.reason}</p>{/if}
                    {#if rowErrors[gid]}<p role="alert" class="col-start-1 col-end-3 min-w-0 break-words pl-9 text-sm text-bad">{hubReason(rowErrors[gid])}</p>{/if}
                  </div>
                  {#if open}
                    <ul id={`${gid}-rows`} class="divide-y divide-edge/30 border-t border-edge/40">
                      {#each g.items as it (it.id)}
                        {@render availableRow(it, g.reason)}
                      {/each}
                    </ul>
                  {/if}
                </li>
              {/each}
            </ul>
          {/if}
        </section>

        <section aria-labelledby="team-progress">
          <h2 id="team-progress" class="label mb-2 text-faint">In progress <span class="num">{sections.mine.length + sections.teammates.length}</span></h2>
          {#if sections.mine.length + sections.teammates.length === 0}
            <p class="text-sm text-faint">No one is working a chore right now.</p>
          {:else}
            <ul class="divide-y divide-edge/40 rounded-[10px] border border-edge">
              {#each sections.mine as it (it.id)}
                {@const sid = localSession(it, st)}
                <li class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-3 py-2.5" data-board-item={it.id} data-mine>
                  <span class="grid min-w-0 gap-1">
                    {@render headline(it)}
                    {@render facts(it)}
                    <span class="text-sm text-ink-2">This Mac{#if it.lease?.expires} · {leaseLeft(it.lease.expires)}{/if}</span>
                  </span>
                  <span class="flex shrink-0 items-center gap-2">
                    {#if sid}<Button size="xs" onclick={() => nav.openSession(sid)}>Open session</Button>{/if}
                    <Button variant="danger" size="xs" disabled={hub.busy !== ""} loading={hub.busy === `release:${it.id}`} onclick={() => askRelease(it)}>Release…</Button>
                  </span>
                  {@render rowError(it)}
                </li>
              {/each}
              {#each sections.teammates as it (it.id)}
                <li class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-3 py-2.5" data-board-item={it.id}>
                  <span class="grid min-w-0 gap-1">
                    {@render headline(it)}
                    {@render facts(it)}
                  </span>
                  <span class="grid shrink-0 justify-items-end text-sm">
                    <span class="text-ink-2">{it.lease?.deviceName || "A teammate"}</span>
                    {#if it.lease?.expires}<span class="text-faint">{leaseLeft(it.lease.expires)}</span>{/if}
                  </span>
                </li>
              {/each}
            </ul>
          {/if}
        </section>

        <section class="grid gap-3" aria-label="Finished chores">
          <Disclosure label="Done" hint={String(sections.done.length)}>
            {@render finished(sections.done, "No finished chores yet.")}
          </Disclosure>
          <Disclosure label="Failed" hint={String(sections.failed.length)}>
            {@render finished(sections.failed, "No failed chores.")}
          </Disclosure>
          <Disclosure label="Escalated" hint={String(sections.escalated.length)}>
            {@render finished(sections.escalated, "Nothing escalated.")}
          </Disclosure>
        </section>

        <section aria-labelledby="team-devices">
          <h2 id="team-devices" class="label mb-2 text-faint">Team devices <span class="num">{devices.length}</span></h2>
          {#if hub.orgError}
            <p role="alert" class="text-sm text-warn">{hub.orgError}</p>
          {:else if !hub.org}
            <p class="text-sm text-faint">Loading devices…</p>
          {:else if devices.length === 0}
            <p class="text-sm text-faint">No devices have joined yet.</p>
          {:else}
            <table class="w-full overflow-hidden rounded-[10px] border border-edge text-sm">
              <thead class="label text-left text-faint">
                <tr>
                  <th class="px-3 py-2">Device</th>
                  <th class="px-3 py-2">Last seen</th>
                  <th class="px-3 py-2 text-right">Free slots</th>
                  <th class="px-3 py-2">Agents</th>
                </tr>
              </thead>
              <tbody>
                {#each devices as d (d.name)}
                  {@const up = deviceOnline(d)}
                  <tr class="border-t border-edge/30" data-device={d.name}>
                    <td class="px-3 py-2">
                      <span class="inline-flex min-w-0 items-center gap-2">
                        <span class="h-1.5 w-1.5 shrink-0 rounded-full {up ? 'bg-good' : 'bg-faint'}" role="img" aria-label={up ? "Online" : "Offline"}></span>
                        <span class="min-w-0 truncate text-ink">{d.name}</span>
                        {#if d.me}<span class="rounded bg-pill-grey px-1.5 text-pill-grey-fg">This Mac</span>{/if}
                      </span>
                    </td>
                    <td class="px-3 py-2 text-faint">{#if d.me}Now{:else if d.lastSeen}<TimeAgo at={d.lastSeen} />{:else}Never{/if}</td>
                    <td class="num px-3 py-2 text-right text-ink">{d.freeSlots}</td>
                    <td class="px-3 py-2 text-faint">{(d.agents ?? []).join(", ") || "—"}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          {/if}
        </section>
      {/if}
    </div>
  </div>
</div>
