<script lang="ts">
  // The Team view: the team hub's chores, who works them, what they shipped,
  // and the org's Macs. Reads `hub` (polled only while this view is on screen)
  // and splits the board with the pure helpers in $lib/hubboard.
  //
  // Nothing here decides who may claim what. `local.claimable` / `local.reason`
  // are the daemon's answer for this Mac, a claim or release is re-checked by
  // the daemon when pressed, and a refusal is shown in the daemon's words
  // through ONE mapper (hubReason), never with a raw "Error:" prefix.
  //
  // Plain words throughout: a chore is "taken by" someone, a Mac was "last
  // seen", this machine is "this Mac". The view never says lease, heartbeat,
  // offer or device key.
  import { onDestroy, onMount, tick } from "svelte";
  import Button from "$lib/components/Button.svelte";
  import Chevron from "$lib/components/Chevron.svelte";
  import HubSyncButton from "$lib/components/hub/HubSyncButton.svelte";
  import FixAllButton from "$lib/components/hub/FixAllButton.svelte";
  import UpdateAllDialog from "$lib/components/hub/UpdateAllDialog.svelte";
  import Disclosure from "$lib/components/Disclosure.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import Select from "$lib/components/Select.svelte";
  import TimeAgo from "$lib/components/TimeAgo.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { hub, connectionState } from "$lib/hub.svelte";
  import { nav } from "$lib/nav.svelte";
  import { sandboxLogin } from "$lib/sandboxlogin.svelte";
  import { store } from "$lib/store.svelte";
  import {
    attemptLine,
    boardSections,
    claimProjects,
    deliveryClass,
    deliveryLabel,
    deviceOnline,
    dockerDown,
    fixAllItems,
    groupByAdvisory,
    groupByRepo,
    healthLine,
    hubReason,
    itemTitle,
    kindIcon,
    kindLabel,
    localSession,
    needsYou,
    onlineCount,
    packageLine,
    prURL,
    severityClass,
    severityCounts,
    severityFill,
    severityLabel,
    severityTally,
    skipLine,
    splitFix,
    statsLine,
    whoOf,
    whyLine,
    workGroups,
    workingFor,
    type BoardGroup,
    type NeedsKind,
    type NeedsRow,
    type WorkGroup,
  } from "$lib/hubboard";
  import type { HubBoardItem, HubUpdateScope } from "@bindings/internal/protocol";
  import { ConfigService } from "@bindings/desktop";

  let release: (() => void) | null = null;
  /** Projects that take chores on this Mac; null = not read (or unreadable). */
  let choreProjects = $state<number | null>(null);
  /** The clock the "working for" lines read; ticks once a minute. */
  let now = $state(Date.now());
  let clock: ReturnType<typeof setInterval> | undefined;

  function readChoreProjects() {
    ConfigService.HubProjects()
      .then((ps) => (choreProjects = (ps ?? []).filter((p) => p.hubChores).length))
      .catch(() => (choreProjects = null));
  }

  onMount(() => {
    release = hub.watch({ board: true });
    readChoreProjects();
    clock = setInterval(() => (now = Date.now()), 60_000);
  });
  onDestroy(() => {
    release?.();
    clearInterval(clock);
  });

  // The "No project takes chores" banner is a read of config.toml. Settings is
  // where that changes, so the read runs again each time the settings overlay
  // closes; the banner never outlives the fix.
  let lastOverlay: string | null = null;
  $effect(() => {
    const o = nav.overlay as string | null;
    if (lastOverlay === "settings" && o !== "settings") readChoreProjects();
    lastOverlay = o;
  });

  const st = $derived(hub.status);
  const conn = $derived(connectionState(st, now));
  const sections = $derived(boardSections(hub.items));
  const needs = $derived(hub.board ? needsYou(hub.items, st, now) : []);
  const devices = $derived(hub.org?.devices ?? []);
  const online = $derived(onlineCount(devices, now));
  const orgName = $derived(st?.org || hub.org?.org || "Team hub");
  const health = $derived((st?.findingsHealth ?? []).map(healthLine).filter((h) => h !== null));
  const skips = $derived(skipGroups(st?.offerSkips ?? []));
  const stats = $derived(statsLine(hub.stats));
  /** The board is shown but the hub did not answer the last read. */
  const stale = $derived(!!hub.board && (!!hub.boardError || conn.tone === "bad"));

  const toneCls: Record<string, string> = {
    good: "border-good/40 bg-good/10 text-good",
    bad: "border-bad/40 bg-bad/10 text-bad",
    warn: "border-warn/40 bg-warn/10 text-warn",
  };

  /** The project a person picked for an item (or group) several projects could run. */
  let picks = $state<Record<string, string>>({});
  /** The daemon's refusal of the last claim/release, per item or group key. */
  let rowErrors = $state<Record<string, string>>({});
  /** Finished rows whose history is open. */
  let historyOpen = $state<Record<string, boolean>>({});
  /** The row a chip asked for, outlined for a moment. */
  let highlighted = $state("");

  const projectName = (p: string) => store.displayNameFor(p) || p;

  function openSettings() {
    nav.openOverlay("settings", "", "hub");
  }

  function setError(key: string, msg: string) {
    rowErrors = { ...rowErrors, [key]: msg };
  }

  /** Same skip reason on several chores reads once, with a count. */
  function skipGroups(list: { itemId: string; kind: string; reason: string }[]) {
    const by = new Map<string, { text: string; fix: string; count: number }>();
    for (const sk of list) {
      const l = skipLine(sk as never);
      const g = by.get(l.text) ?? { ...l, count: 0 };
      g.count++;
      by.set(l.text, g);
    }
    return [...by.values()];
  }

  type ClaimOpts = { errKey?: string; pickKey?: string; again?: boolean };

  /** The project a claim names: the person's pick when several fit, else the row's one project. */
  function claimProject(it: HubBoardItem, pickKey: string): { project: string; missing: boolean } {
    const choices = claimProjects(it);
    const project = choices.length ? (picks[pickKey] ?? "") : (it.local?.project ?? "");
    return { project, missing: choices.length > 0 && !project };
  }

  function askClaim(it: HubBoardItem, opts: ClaimOpts = {}) {
    const errKey = opts.errKey ?? it.id;
    setError(errKey, "");
    const { project, missing } = claimProject(it, opts.pickKey ?? it.id);
    if (missing) {
      setError(errKey, "Choose the project to run it in.");
      return;
    }
    const where = project ? projectName(project) : "its project";
    confirm.ask({
      title: opts.again ? "Claim this chore again?" : "Claim this chore?",
      body: `Claim ${itemTitle(it)} and start a session in ${where}?`,
      detail: "The team sees this Mac working it. A coding agent starts in a new worktree and opens a pull request.",
      confirmLabel: opts.again ? "Claim again" : "Claim",
      onConfirm: () => void claim(it, project, errKey),
    });
  }

  /**
   * The same pre-start check the store's open path runs: a sandboxed agent
   * with no login would start, print "Not logged in" and wait. A person who
   * gives up the login dialog gets a line saying why nothing started.
   */
  async function loginReady(project: string, errKey: string): Promise<boolean> {
    if (await sandboxLogin.ensure(project)) return true;
    setError(errKey, "the sandboxed agent has no login yet");
    return false;
  }

  async function claim(it: HubBoardItem, project: string, errKey: string) {
    if (!(await loginReady(project, errKey))) return;
    const r = await hub.claim(it.id, project);
    if (r.ok) store.setFlash(`Claimed ${itemTitle(it)} in ${projectName(r.data.project)}`);
    else setError(errKey, r.error);
  }

  function askRelease(it: HubBoardItem, errKey = it.id) {
    setError(errKey, "");
    confirm.ask({
      title: "Release this chore?",
      body: `Release ${itemTitle(it)} back to the team?`,
      detail: "Another Mac may take it, and this Mac skips it for 24 hours. The session keeps running, but the team stops hearing from it.",
      confirmLabel: "Release",
      onConfirm: () => void doRelease(it, errKey),
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
    const held = g.items.filter((it) => it.state === "claimed");
    if (held.length === 1) return askRelease(held[0], g.key);
    confirm.ask({
      title: "Release these chores?",
      body: `Release ${held.length} chores back to the team?`,
      detail: "Another Mac may take them, and this Mac skips them for 24 hours. The session keeps running, but the team stops hearing from it.",
      confirmLabel: "Release all",
      onConfirm: () => void releaseGroup(g, held),
    });
  }

  /** Releases one by one; every refusal is kept, one line per chore. */
  async function releaseGroup(g: WorkGroup, items: HubBoardItem[]) {
    let done = 0;
    const errs: string[] = [];
    for (const it of items) {
      const r = await hub.release(it.id);
      if (r.ok) done++;
      else errs.push(`${itemTitle(it)}: ${hubReason(r.error)}`);
    }
    setError(g.key, errs.join("\n"));
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
    // One chore: the single claim, its errors on the group header where the
    // button was pressed (its own row may be collapsed).
    if (items.length === 1) return askClaim(items[0], { errKey: gid, pickKey: gid });
    const { project, missing } = claimProject(items[0], gid);
    if (missing) {
      setError(gid, "Choose the project to run it in.");
      return;
    }
    const where = project ? projectName(project) : "its project";
    const { advisories, prs } = splitFix(items);
    const agents = (advisories.length ? 1 : 0) + prs.length;
    const body = prs.length === 0
      ? `Start one agent for ${advisories.length} advisories in ${where}?`
      : advisories.length === 0
        ? `Start ${agents} agents in ${where}, one per failing dependency bump?`
        : `Start ${agents} agents in ${where}? One fixes the ${advisories.length === 1 ? "advisory" : `${advisories.length} advisories`}; each failing dependency bump gets its own.`;
    confirm.ask({
      title: "Fix all chores?",
      body,
      detail: "Each chore is claimed for this Mac. The advisories go to one agent and one pull request; a failing dependency bump keeps its own pull request.",
      confirmLabel: "Fix all",
      onConfirm: () => void fixAll(g, items, project),
    });
  }

  /** The open "Update all dependencies" dialog. */
  let updating = $state<{ g: BoardGroup; items: HubBoardItem[]; project: string } | null>(null);
  let updateErr = $state("");

  function openUpdateAll(g: BoardGroup) {
    const gid = `group:${g.key}`;
    setError(gid, "");
    const items = splitFix(fixAllItems(g)).advisories;
    if (items.length === 0) return;
    const { project, missing } = claimProject(items[0], gid);
    if (missing) {
      setError(gid, "Choose the project to run it in.");
      return;
    }
    updateErr = "";
    updating = { g, items, project };
  }

  async function startUpdate(scope: HubUpdateScope) {
    if (!updating) return;
    const { g, items, project } = updating;
    if (!(await loginReady(project, `group:${g.key}`))) {
      updating = null;
      return;
    }
    const r = await hub.claimMany(`group:${g.key}`, items.map((it) => it.id), project, scope);
    if (r.ok) {
      updating = null;
      store.setFlash(`Started a dependency update in ${projectName(r.data.project)}`);
    } else {
      updateErr = hubReason(r.error);
    }
  }

  /** Advisories as one claim, each failing bump as its own; every refusal is kept. */
  async function fixAll(g: BoardGroup, items: HubBoardItem[], project: string) {
    const gid = `group:${g.key}`;
    if (!(await loginReady(project, gid))) return;
    const { advisories, prs } = splitFix(items);
    const errs: string[] = [];
    let claimed = 0;
    if (advisories.length > 1) {
      const r = await hub.claimMany(gid, advisories.map((it) => it.id), project);
      if (r.ok) claimed += advisories.length;
      else errs.push(hubReason(r.error));
    } else if (advisories.length === 1) {
      const r = await hub.claim(advisories[0].id, project);
      if (r.ok) claimed++;
      else errs.push(`${itemTitle(advisories[0])}: ${hubReason(r.error)}`);
    }
    for (const it of prs) {
      const r = await hub.claim(it.id, project || it.local?.project || "");
      if (r.ok) claimed++;
      else errs.push(`${itemTitle(it)}: ${hubReason(r.error)}`);
    }
    setError(gid, errs.join("\n"));
    if (claimed) store.setFlash(`Claimed ${claimed} ${claimed === 1 ? "chore" : "chores"} in ${project ? projectName(project) : groupName(g)}`);
  }

  async function doRelease(it: HubBoardItem, errKey = it.id) {
    const r = await hub.release(it.id);
    if (r.ok) store.setFlash(r.data.message ? hubReason(r.data.message) : `Released ${itemTitle(it)}`, r.data.queued ? "warn" : "good");
    else setError(errKey, r.error);
  }

  /** Busy per row: a claim on one chore never greys out another. */
  const claimBusy = (id: string) => hub.isBusy(`claim:${id}`);
  const joining = $derived(hub.isBusy("join") || hub.isBusy("leave"));

  // A chip's click: bring that chore into view, open what hides it, outline it.
  $effect(() => {
    const id = hub.focusItem;
    if (!id || !hub.board) return;
    const it = hub.items.find((x) => x.id === id);
    if (it?.state === "open") {
      const key = groupMode === "project" ? it.repo || "" : it.kind === "dep_advisory" && it.advisory ? it.advisory : itemTitle(it);
      expanded = { ...expanded, [key]: true };
    } else if (it && (it.state === "claimed" || it.state === "in_review")) {
      const w = working.find((g) => g.items.some((x) => x.id === id));
      if (w) expanded = { ...expanded, [w.key]: true };
    }
    hub.focusItem = "";
    highlighted = id;
    void tick().then(() => {
      const el =
        document.querySelector(`[data-needs-item="${CSS.escape(id)}"]`) ?? document.querySelector(`[data-board-item="${CSS.escape(id)}"]`);
      (el as HTMLElement | null)?.scrollIntoView?.({ block: "center" });
    });
    setTimeout(() => {
      if (highlighted === id) highlighted = "";
    }, 4000);
  });

  const NEEDS_TAG: Record<NeedsKind, { text: string; cls: string }> = {
    taken: { text: "Taken by another Mac", cls: "border-bad/40 bg-bad/10 text-bad" },
    failed: { text: "Failed", cls: "border-bad/40 bg-bad/10 text-bad" },
    escalated: { text: "Escalated", cls: "border-warn/40 bg-warn/10 text-warn" },
    review: { text: "Waiting on review", cls: "border-warn/40 bg-warn/10 text-warn" },
    critical: { text: "Critical", cls: "border-bad/40 bg-bad/10 text-bad" },
  };
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

<!-- A refusal, in plain words. Docker being down is the one a person fixes
     from here, so it carries the way to the doctor. -->
{#snippet errorLine(key: string, cls = "")}
  {#if rowErrors[key]}
    <p role="alert" class="col-span-full flex min-w-0 flex-wrap items-center gap-2 whitespace-pre-line break-words text-sm text-bad {cls}" data-row-error>
      <span class="min-w-0">{hubReason(rowErrors[key])}</span>
      {#if dockerDown(rowErrors[key])}
        <Button size="xs" variant="secondary" onclick={() => nav.openOverlay("doctor")}>Open doctor</Button>
      {/if}
    </p>
  {/if}
{/snippet}

{#snippet projectPick(it: HubBoardItem, key: string, label: string)}
  {@const choices = claimProjects(it)}
  {#if it.local?.claimable && choices.length}
    <Select aria-label="Project for {label}" bind:value={picks[key]}>
      <option value="">Choose a project</option>
      {#each choices as p (p)}<option value={p}>{projectName(p)}</option>{/each}
    </Select>
  {/if}
{/snippet}

{#snippet whoChip(w: { name: string; device: string; mine: boolean } | null)}
  {#if w}
    <span class="inline-flex min-w-0 items-baseline gap-1.5" data-who>
      <span class="min-w-0 truncate text-ink-2">{w.mine ? "This Mac" : w.name}</span>
      {#if !w.mine && w.device && w.device !== w.name}<span class="min-w-0 truncate text-faint">{w.device}</span>{/if}
    </span>
  {/if}
{/snippet}

{#snippet prBits(it: HubBoardItem)}
  {@const pr = prURL(it)}
  {#if it.outcome?.delivery}
    <span class="rounded px-1.5 text-sm {deliveryClass(it.outcome.delivery)}" data-delivery={it.outcome.delivery}>{deliveryLabel(it.outcome.delivery)}</span>
  {/if}
  {#if pr}
    <Button size="xs" icon aria-label="Open pull request" title={pr} onclick={() => store.openURL(pr)}><Icon name="git-pull-request" size={14} /></Button>
  {/if}
{/snippet}

<!-- One advisory per line, columns aligned across the group:
     severity · id · package from → to · where · age · action. -->
{#snippet itemRow(it: HubBoardItem, where: string, action: import("svelte").Snippet<[HubBoardItem]> | null, note = "")}
  {@const place = where || (it.reporters > 1 ? `${it.reporters} reporters` : "")}
  <li class="team-row grid min-w-0 items-center gap-x-4 gap-y-1 py-2 pl-10 pr-3 {highlighted === it.id ? 'outline outline-2 -outline-offset-2 outline-accent' : ''}" data-board-item={it.id}>
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
    {@render errorLine(it.id)}
  </li>
{/snippet}

{#snippet claimAction(it: HubBoardItem)}
  {@const claimable = !!it.local?.claimable}
  {@render projectPick(it, it.id, itemTitle(it))}
  <Button size="xs" disabled={!claimable || claimBusy(it.id) || joining} loading={claimBusy(it.id)}
    title={claimable ? `Claim ${itemTitle(it)}` : hubReason(it.local?.reason) || "Not claimable on this Mac"}
    onclick={() => askClaim(it)}>Claim</Button>
{/snippet}

<!-- In progress: the PR's state and link for every row, mine or a
     teammate's; this Mac's rows still holding a claim can be given back. -->
{#snippet progressAction(it: HubBoardItem)}
  {@render prBits(it)}
  {#if (it.lease?.mine || it.owner?.mine) && it.state === "claimed"}
    <Button variant="danger" size="xs" disabled={hub.isBusy(`release:${it.id}`) || joining} loading={hub.isBusy(`release:${it.id}`)} onclick={() => askRelease(it)}>Release…</Button>
  {/if}
{/snippet}

{#snippet resultAction(it: HubBoardItem)}
  {@render prBits(it)}
  {#if it.escalation?.url}
    <Button size="xs" title={it.escalation.url} onclick={() => store.openURL(it.escalation!.url!)}>
      <Icon name="external-link" size={14} /> {it.escalation.identifier || "Linear issue"}
    </Button>
  {:else if it.escalation?.pending}
    <span class="text-sm text-warn">Linear issue on its way</span>
  {/if}
{/snippet}

{#snippet finished(rows: HubBoardItem[], empty: string)}
  {#if rows.length === 0}
    <p class="px-1 py-2 text-sm text-faint">{empty}</p>
  {:else}
    <ul class="mt-1 divide-y divide-edge/40">
      {#each rows as it (it.id)}
        {@const who = whoOf(it)}
        {@const hist = it.history ?? []}
        {@const why = whyLine(it)}
        <li class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 py-2 {highlighted === it.id ? 'outline outline-2 -outline-offset-2 outline-accent' : ''}" data-board-item={it.id}>
          <span class="grid min-w-0">
            <span class="flex min-w-0 items-center gap-1.5">
              <span class="min-w-0 truncate text-sm text-ink" data-item-title>{itemTitle(it)}</span>
              {#if it.package}<span class="min-w-0 truncate font-mono text-sm text-faint">{it.package}</span>{/if}
            </span>
            <span class="flex min-w-0 flex-wrap items-baseline gap-x-1.5 text-sm text-faint" data-finished-meta>
              <span class="min-w-0 truncate">{it.repo}</span>
              {#if who}<span>·</span>{@render whoChip(who)}{/if}
              <span>·</span><TimeAgo at={it.lastSeen} hint="Last update" />
              {#if why}<span>· {why}</span>{/if}
            </span>
          </span>
          <span class="flex shrink-0 items-center gap-1.5">{@render resultAction(it)}</span>
          {#if hist.length}
            <span class="col-span-full">
              <Button size="xs" aria-expanded={!!historyOpen[it.id]} onclick={() => (historyOpen = { ...historyOpen, [it.id]: !historyOpen[it.id] })}>
                <Chevron open={!!historyOpen[it.id]} /> History <span class="num text-faint">{hist.length}</span>
              </Button>
              {#if historyOpen[it.id]}
                <ol class="mt-1 grid gap-1 border-l border-edge pl-3 text-sm" data-history={it.id}>
                  {#each hist as a, i (i)}
                    {@const l = attemptLine(a)}
                    <li class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] gap-x-2">
                      <span class="min-w-0"><span class="text-ink-2">{l.what}</span> <span class="text-faint">· {l.who}</span></span>
                      <span class="text-faint"><TimeAgo at={a.at} /></span>
                    </li>
                  {/each}
                </ol>
              {/if}
            </span>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
{/snippet}

{#snippet needsRow(n: NeedsRow)}
  {@const it = n.item}
  {@const tag = NEEDS_TAG[n.kind]}
  {@const claimable = !!it?.local?.claimable}
  <li class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-4 py-3 {highlighted === n.id ? 'outline outline-2 -outline-offset-2 outline-accent' : ''}"
    data-needs-item={n.id} data-needs-kind={n.kind}>
    <span class="grid min-w-0 gap-0.5">
      <span class="flex min-w-0 flex-wrap items-center gap-2">
        <span class="inline-flex shrink-0 items-center rounded-full border px-2 text-sm {tag.cls}">{tag.text}</span>
        <span class="min-w-0 truncate text-ink" data-item-title>{n.title}</span>
        {#if it?.package}<span class="min-w-0 truncate font-mono text-sm text-faint">{it.package}</span>{/if}
      </span>
      <span class="min-w-0 truncate text-sm text-faint">{n.repo}{#if n.repo && n.why} · {/if}<span data-needs-why>{n.why}</span></span>
    </span>
    <span class="flex shrink-0 flex-wrap items-center justify-end gap-2">
      {#if n.session}<Button size="xs" variant="secondary" onclick={() => nav.openSession(n.session)}><Icon name="terminal" size={14} /> Open session</Button>{/if}
      {#if n.pr}<Button size="xs" variant="secondary" title={n.pr} onclick={() => store.openURL(n.pr)}><Icon name="git-pull-request" size={14} /> Open PR</Button>{/if}
      {#if n.issue}<Button size="xs" variant="secondary" title={n.issue} onclick={() => store.openURL(n.issue)}><Icon name="external-link" size={14} /> Open Linear issue</Button>{/if}
      {#if it && claimable}
        {@render projectPick(it, `needs:${n.id}`, n.title)}
        <Button size="xs" variant="primary" disabled={claimBusy(it.id) || joining} loading={claimBusy(it.id)}
          onclick={() => askClaim(it, { errKey: `needs:${n.id}`, pickKey: `needs:${n.id}`, again: n.kind !== "critical" })}>{n.kind === "critical" ? "Claim" : "Claim again"}</Button>
      {/if}
    </span>
    {#if it && !claimable && it.local?.reason && (n.kind === "critical" || n.kind === "failed")}
      <span class="col-span-full text-sm text-faint" data-claim-reason>Can't claim here: {hubReason(it.local.reason)}</span>
    {/if}
    {@render errorLine(`needs:${n.id}`)}
  </li>
{/snippet}

<div class="grid h-full min-h-0 grid-rows-[auto_minmax(0,1fr)]" data-team-view>
  <div class="flex min-w-0 flex-wrap items-center gap-3 border-b border-edge px-5 py-3">
    <span class="min-w-0 truncate text-base font-medium text-ink">{hub.joined ? orgName : "Team"}</span>
    {#if hub.joined}
      <span class="inline-flex items-center rounded-full border px-2 py-0.5 text-sm {toneCls[conn.tone]}" data-connection={conn.tone}>{conn.label}</span>
      {#if hub.org}<span class="num text-sm text-faint" data-online>{online} of {devices.length} {devices.length === 1 ? "Mac" : "Macs"} online</span>{/if}
      {#if stats}<span class="num text-sm text-faint" data-team-stats>{stats}</span>{/if}
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
        <div role="alert" class="flex min-w-0 flex-wrap items-center gap-3 rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn" data-daemon-down>
          <span class="min-w-0 flex-1">The lola daemon is not answering. {hubReason(hub.statusError)}</span>
          <Button size="sm" variant="secondary" onclick={() => store.startDaemon()}>Start daemon</Button>
        </div>
      {:else if !hub.joined}
        <section class="mx-auto grid max-w-[520px] justify-items-center gap-3 py-12 text-center" data-team-empty>
          <span class="text-faint"><Icon name="users" size={28} /></span>
          {#if hub.revoked}
            <h2 class="text-base font-medium text-ink" data-hub-revoked>This Mac was removed from the team</h2>
            <p class="text-sm text-faint">The hub no longer accepts this Mac. Ask your hub admin for a new invite and join again.</p>
            <Button variant="primary" size="md" onclick={openSettings}>Join again</Button>
          {:else}
            <h2 class="text-base font-medium text-ink">Share chores with your team</h2>
            <p class="text-sm text-faint">One Mac fixes each security advisory and failing dependency bump. The team sees the pull request.</p>
            <Button variant="primary" size="md" onclick={openSettings}>Set up Team hub</Button>
          {/if}
        </section>
      {:else}
        {#if stale}
          <p role="status" class="flex min-w-0 flex-wrap items-center gap-1.5 text-sm text-warn" data-stale>
            <Icon name="clock" size={14} />
            {#if hub.board?.fetchedAt}<TimeAgo at={hub.board.fetchedAt} prefix="As of " />{:else}As of the last read{/if}
            <span>· hub unreachable</span>
          </p>
        {/if}
        {#if st?.lastError}
          <p role="alert" class="min-w-0 break-words text-sm text-bad">{hubReason(st.lastError)}{#if st.lastErrorAt} · <TimeAgo at={st.lastErrorAt} />{/if}</p>
        {/if}
        {#if choreProjects === 0}
          <div class="flex min-w-0 flex-wrap items-center gap-3 rounded-lg border border-edge px-3 py-2.5 text-sm" data-no-chore-projects>
            <span class="text-faint"><Icon name="folder" size={16} /></span>
            <span class="min-w-0 flex-1 text-ink">No project on this Mac takes chores yet.</span>
            <Button size="sm" variant="secondary" onclick={openSettings}>Choose projects</Button>
          </div>
        {/if}
        {#if hub.boardError}<div role="alert" class="rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">{hubReason(hub.boardError)}</div>{/if}

        {#if needs.length}
          <section class="min-w-0 overflow-hidden rounded-xl border border-bad/40" aria-labelledby="team-needs" data-needs>
            <header class="flex items-center gap-3 border-b border-edge bg-bad/5 px-4 py-3">
              <h2 id="team-needs" class="text-base font-medium text-ink">Needs you <span class="num ml-1 text-faint">{needs.length}</span></h2>
            </header>
            <ul class="divide-y divide-edge/50">
              {#each needs as n (n.id)}{@render needsRow(n)}{/each}
            </ul>
          </section>
        {/if}

        {#if health.length || skips.length}
          <section class="min-w-0 rounded-xl border border-warn/40" aria-labelledby="team-loop" data-loop-health>
            <header class="flex items-center gap-3 border-b border-edge px-4 py-3">
              <h2 id="team-loop" class="text-base font-medium text-ink">What this Mac could not do</h2>
            </header>
            <ul class="divide-y divide-edge/40 text-sm">
              {#each health as h (h!.repo)}
                <li class="grid min-w-0 gap-0.5 px-4 py-2.5" data-health-repo={h!.repo}>
                  <span class="min-w-0"><span class="text-ink">{h!.repo}</span> <span class="text-warn">· {h!.text}</span></span>
                  <span class="text-faint">{h!.fix}</span>
                </li>
              {/each}
              {#each skips as s (s.text)}
                <li class="grid min-w-0 gap-0.5 px-4 py-2.5" data-offer-skip>
                  <span class="min-w-0 text-ink">{s.count === 1 ? "One chore was" : `${s.count} chores were`} not taken: <span class="text-warn">{s.text}</span></span>
                  {#if s.fix}<span class="text-faint">{s.fix}</span>{/if}
                </li>
              {/each}
            </ul>
          </section>
        {/if}

        {#if hub.board}
          {@const tally = severityTally(sections.available)}
          {@const sessions = working.filter((g) => g.mine).length}
          {@const mates = new Set(sections.teammates.map((it) => whoOf(it)?.device || "")).size}
          <section class="grid min-w-0 overflow-hidden rounded-xl border border-edge bg-panel/40 lg:grid-cols-[minmax(0,1.7fr)_minmax(0,1fr)]" aria-label="Summary" data-team-summary>
            <div class="grid min-w-0 content-center gap-3 px-5 py-4" data-team-card="open">
              <p class="flex items-baseline gap-2">
                <span class="num text-[28px] font-semibold leading-none text-ink">{sections.available.length}</span>
                <span class="text-faint">{sections.available.length === 1 ? "open chore" : "open chores"}</span>
              </p>
              {#if sections.available.length}
                <span class="flex h-2.5 gap-0.5 overflow-hidden rounded-full" role="img" aria-label={severityCounts(sections.available)}>
                  {#each tally as t (t.severity)}
                    <span class="h-full first:rounded-l-full last:rounded-r-full {severityFill(t.severity)}" style:flex-grow={t.count}></span>
                  {/each}
                </span>
                <ul class="flex flex-wrap gap-x-5 gap-y-1 text-sm" aria-label="Open chores by severity">
                  {#each tally as t (t.severity)}
                    <li class="flex items-center gap-1.5">
                      <span class="h-2 w-2 rounded-full {severityFill(t.severity)}" aria-hidden="true"></span>
                      <span class="num font-medium text-ink">{t.count}</span>
                      <span class="text-faint">{t.severity ? severityLabel(t.severity).toLowerCase() : "unrated"}</span>
                    </li>
                  {/each}
                </ul>
              {:else}
                <p class="flex items-center gap-1.5 text-sm text-good"><Icon name="check" size={14} /> The team is caught up.</p>
              {/if}
            </div>
            <dl class="grid min-w-0 content-center gap-2.5 border-t border-edge px-5 py-4 text-sm lg:border-l lg:border-t-0">
              <div class="grid grid-cols-[6rem_minmax(0,1fr)] items-baseline gap-3" data-team-card="mine">
                <dt class="text-faint">This Mac</dt>
                <dd class="min-w-0 truncate">
                  {#if sections.mine.length}
                    <span class="num font-medium text-ink">{sections.mine.length}</span> <span class="text-faint">{sections.mine.length === 1 ? "chore" : "chores"} · {sessions} {sessions === 1 ? "session" : "sessions"}</span>
                  {:else}<span class="text-faint">Idle</span>{/if}
                </dd>
              </div>
              <div class="grid grid-cols-[6rem_minmax(0,1fr)] items-baseline gap-3" data-team-card="teammates">
                <dt class="text-faint">Teammates</dt>
                <dd class="min-w-0 truncate">
                  {#if sections.teammates.length}
                    <span class="num font-medium text-ink">{sections.teammates.length}</span> <span class="text-faint">{sections.teammates.length === 1 ? "chore" : "chores"} · {mates} {mates === 1 ? "Mac" : "Macs"}</span>
                  {:else}<span class="text-faint">None working</span>{/if}
                </dd>
              </div>
              <div class="grid grid-cols-[6rem_minmax(0,1fr)] items-baseline gap-3" data-team-card="done">
                <dt class="text-faint">Done</dt>
                <dd class="min-w-0 truncate">
                  <span class="num font-medium text-ink">{sections.done.length}</span>
                  {#if sections.failed.length}<span class="text-bad"> · {sections.failed.length} failed</span>{/if}
                  {#if sections.escalated.length}<span class="text-warn"> · {sections.escalated.length} escalated</span>{/if}
                  {#if !sections.failed.length && !sections.escalated.length}<span class="text-faint"> · no failures</span>{/if}
                </dd>
              </div>
            </dl>
          </section>
        {/if}

        <div class="grid min-w-0 items-start gap-6 xl:grid-cols-[minmax(0,1fr)_340px]">
          <div class="grid min-w-0 gap-6">
            <section class="min-w-0 rounded-xl border border-edge" aria-labelledby="team-available">
              <header class="flex min-w-0 flex-wrap items-center gap-3 border-b border-edge px-4 py-3">
                <h2 id="team-available" class="text-base font-medium text-ink">Available <span class="num ml-1 text-faint">{sections.available.length}</span></h2>
                {#if sections.available.length > 0}
                  <span class="ml-auto flex items-center gap-0.5 rounded-lg border border-edge p-0.5" role="group" aria-label="Group available chores">
                    {#each [["project", "By project"], ["advisory", "By advisory"]] as [mode, text] (mode)}
                      <!-- aria-pressed spelled out: a toggle group reports "false" on the
                           other half too, which Button's selected alone leaves unset. -->
                      <Button size="xs" selected={groupMode === mode} aria-pressed={groupMode === mode} onclick={() => setGroupMode(mode as "project" | "advisory")}>{text}</Button>
                    {/each}
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
                    {@const fixAdvisories = splitFix(fix).advisories.length}
                    {@const busy = claimBusy(gid) || fix.some((it) => claimBusy(it.id))}
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
                          {#if fix.length}{@render projectPick(fix[0], gid, groupName(g))}{/if}
                          {#if groupMode === "project"}
                            <FixAllButton label={fix.length > 1 ? `Fix all ${fix.length}` : "Claim"}
                              disabled={fix.length === 0 || busy || joining}
                              loading={busy}
                              updateDisabled={fixAdvisories === 0}
                              title={fix.length ? `Start work on ${fix.length === 1 ? "this chore" : `${fix.length} chores`}` : hubReason(g.reason) || "Not claimable on this Mac"}
                              onFix={() => askFixAll(g)} onUpdateAll={() => openUpdateAll(g)} />
                          {/if}
                        </span>
                        {#if g.reason}<p class="col-span-full flex items-center gap-1.5 pl-10 text-sm text-faint" data-group-reason><Icon name="lock" size={12} /> Can't claim here: {hubReason(g.reason)}</p>{/if}
                        {@render errorLine(gid, "pl-10")}
                      </div>
                      {#if open}
                        <ul id={`${gid}-rows`} class="divide-y divide-edge/30 border-t border-edge/40 bg-canvas/40 pb-1">
                          {#each g.items as it (it.id)}
                            {@render itemRow(it, groupMode === "project" ? "" : it.repo, claimAction, !it.local?.claimable && !g.reason ? `Can't claim here: ${hubReason(it.local?.reason) || "not on this Mac"}` : "")}
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
                    {@const first = w.items.find((it) => it.claimedAt === w.since) ?? w.items[0]}
                    {@const forText = workingFor(first, now)}
                    {@const held = w.items.filter((it) => it.state === "claimed").length}
                    {@const session = w.mine ? w.session || localSession(w.items[0], st) : ""}
                    <li class="min-w-0" data-work-group={w.key} data-mine={w.mine || undefined}>
                      <div class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-3 py-3">
                        <span class="flex min-w-0 items-center gap-3">
                          <Button size="sm" variant="ghost" aria-expanded={open} aria-controls={`${w.key}-rows`} onclick={() => toggle(w.key)}>
                            <Chevron {open} />
                            <span class="min-w-0 truncate text-base font-medium text-ink">{proj ? projectName(proj) : w.repo}</span>
                          </Button>
                          <span class="grid min-w-0 gap-1">
                            <span class="flex min-w-0 flex-wrap items-center gap-2 text-sm">
                              <span class="inline-flex items-baseline gap-1.5 rounded-full px-2 {w.mine ? 'bg-accent-fill text-accent-ink' : 'bg-pill-grey text-pill-grey-fg'}" data-work-who>
                                {w.mine ? "This Mac" : w.who}
                                {#if !w.mine && w.device !== w.who}<span class="opacity-80">· {w.device}</span>{/if}
                              </span>
                              <span class="num text-faint">{w.items.length} {w.items.length === 1 ? "chore" : "chores"}</span>
                              {#if forText}<span class="text-faint" data-working-for>· {forText}</span>{/if}
                            </span>
                            {#if proj}<span class="min-w-0 truncate text-sm text-faint">{w.repo}</span>{/if}
                          </span>
                        </span>
                        <span class="flex shrink-0 items-center gap-2">
                          {@render sevBar(w.items, "hidden w-28 md:flex")}
                          {#if w.items.length === 1}{@render prBits(w.items[0])}{/if}
                          {#if w.mine}
                            {#if session}<Button size="sm" variant="secondary" onclick={() => nav.openSession(session)}><Icon name="terminal" size={14} /> Open session</Button>{/if}
                            {#if held}
                              <Button variant="danger" size="sm" disabled={joining || w.items.some((it) => hub.isBusy(`release:${it.id}`))} onclick={() => askReleaseGroup(w)}>{held > 1 ? "Release all…" : "Release…"}</Button>
                            {/if}
                          {/if}
                        </span>
                        {@render errorLine(w.key, "pl-10")}
                      </div>
                      {#if open}
                        <ul id={`${w.key}-rows`} class="divide-y divide-edge/30 border-t border-edge/40 bg-canvas/40 pb-1">
                          {#each w.items as it (it.id)}
                            {@render itemRow(it, "", progressAction)}
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
                <h2 id="team-devices" class="text-base font-medium text-ink">Macs <span class="num ml-1 text-faint">{devices.length}</span></h2>
              </header>
              {#if hub.orgError}
                <p role="alert" class="px-4 py-3 text-sm text-warn">{hubReason(hub.orgError)}</p>
              {:else if !hub.org}
                <p class="px-4 py-3 text-sm text-faint">Loading Macs…</p>
              {:else if devices.length === 0}
                <p class="px-4 py-3 text-sm text-faint">No Mac has joined yet.</p>
              {:else}
                <ul class="divide-y divide-edge/40">
                  {#each devices as d (d.name)}
                    {@const up = deviceOnline(d, now)}
                    <li class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 px-4 py-2.5" data-device={d.name}>
                      <span class="grid min-w-0 gap-0.5">
                        <span class="flex min-w-0 items-center gap-2">
                          <!-- Shape AND words, never colour alone: a filled dot
                               and "Online", or a hollow ring and "Offline". -->
                          <span class="h-2 w-2 shrink-0 rounded-full {up ? 'bg-good' : 'border border-faint'}" role="img" aria-label={up ? "Online" : "Offline"}></span>
                          <span class="min-w-0 truncate text-ink">{d.member || d.name}</span>
                          {#if d.me}<span class="rounded bg-pill-grey px-1.5 text-sm text-pill-grey-fg">This Mac</span>{/if}
                        </span>
                        <span class="min-w-0 truncate pl-4 text-sm text-faint">
                          {#if d.member}{d.name} · {/if}<span class={up ? "text-good" : ""} data-device-state>{up ? "Online" : "Offline"}</span>
                          {#if !d.me}· {#if d.lastSeen}<TimeAgo at={d.lastSeen} prefix="last seen " />{:else}never seen{/if}{/if}{#if (d.agents ?? []).length} · {(d.agents ?? []).join(", ")}{/if}
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
              <div class="grid gap-3">
                <!-- Failed and escalated are open by default: they are the
                     results a person may still have to act on. -->
                <div class="border-t border-edge pt-2" data-results="failed">
                  <h3 class="text-sm font-medium text-ink">Failed <span class="num font-normal text-faint">{sections.failed.length}</span></h3>
                  {@render finished(sections.failed, "No failed chores.")}
                </div>
                <div class="border-t border-edge pt-2" data-results="escalated">
                  <h3 class="text-sm font-medium text-ink">Escalated <span class="num font-normal text-faint">{sections.escalated.length}</span></h3>
                  {@render finished(sections.escalated, "Nothing escalated.")}
                </div>
                <Disclosure label="Done" hint={String(sections.done.length)}>
                  {@render finished(sections.done, "No finished chores yet.")}
                </Disclosure>
              </div>
            </section>
          </aside>
        </div>
      {/if}
    </div>
  </div>
</div>

{#if updating}
  <UpdateAllDialog where={updating.project ? projectName(updating.project) : groupName(updating.g)} advisories={updating.items.length}
    busy={hub.isBusy(`claim:group:${updating.g.key}`)} error={updateErr}
    onStart={(scope) => void startUpdate(scope)} onClose={() => (updating = null)} />
{/if}

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
