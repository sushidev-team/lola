<script lang="ts">
  // The top bar while a session is open. It says what happened to the work: the delivery axis
  // lives here, and the agent axis at the bottom left of the view. Each fact is said once, so
  // there is no status pill here, and nothing filled.
  //
  // A fixed 44px with no wrap, so a chip arriving can never reflow the PTY below. Still the
  // window's drag band: every control here is a real button, which app.css opts out of it.
  //
  // Below 900px of main column the dev chip and the action go into ⋯, which already has both.
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { bestOf } from "$lib/bestof.svelte";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { devToggleTitle } from "$lib/devtoggle";
  import { devUrlLabel } from "$lib/devurl";
  import { usageLabel } from "$lib/usage";
  import { appLabel, appPr, appState, focusLabel } from "$lib/stackapps";
  import { agentName } from "$lib/ssh";
  import { isAgentless } from "$lib/sessionview";
  import { talk } from "$lib/talk.svelte";
  import { widthOf } from "./measure";
  import Button from "../Button.svelte";
  import Chevron from "../Chevron.svelte";
  import MenuItem from "../MenuItem.svelte";
  import HoverCard from "../HoverCard.svelte";
  import PrBadge from "../PrBadge.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const session = $derived(store.sessionById(sessionId));
  // A session that left the list has only its record, and nothing here can act on it.
  const ended = $derived(session ? undefined : talk.ended(sessionId));
  const title = $derived(session?.title || session?.branch || session?.issue || ended?.title || ended?.issue || "Session");
  const issue = $derived(session?.issue || ended?.issue || "");
  const agentless = $derived(!!session && isAgentless(session));

  let width = $state(0);
  // 0 means not measured yet, which is the wide layout: never narrow a bar on a guess.
  const narrow = $derived(width > 0 && width < 880);

  const hoverRows = $derived.by<[string, string][]>(() => {
    const s = session;
    if (!s) return [];
    const rows: [string, string][] = [];
    if (!agentless) rows.push(["Agent", agentName(s.agent)]);
    if (s.model) rows.push(["Model", s.model]);
    if (s.branch) rows.push(["Branch", s.branch]);
    if (s.worktree) rows.push([s.location === "main" ? "Checkout" : "Worktree", s.worktree]);
    if (s.sandbox) rows.push(["Sandbox", s.sandbox]);
    if (s.ports?.length) rows.push(["Ports", `:${s.ports.join(", :")}`]);
    if (s.usage) rows.push(["Usage", usageLabel(s.usage)]);
    return rows;
  });

  // Conflicts first, because nothing else on this list stops a merge. One at most, never solid.
  // Revive is not here: the footer owns it in every view, composer and strip alike, and it is that
  // state's focal point, so a second one up here would only compete with it.
  const action = $derived.by<{ label: string; run: () => void; loading?: boolean } | null>(() => {
    const s = session;
    if (!s) return null;
    if (s.delivery === "merge_conflict" || s.status === "merge_conflict") {
      return { label: "Resolve conflicts", run: () => store.resolveConflict(s.id), loading: store.resolvePending[s.id] };
    }
    if (s.bestOf && !s.bestOf.winner) return { label: "Compare attempts", run: () => bestOf.open(s.id) };
    return null;
  });

  // Best first, across every dev tab. A stack runs several servers and each is a place to go.
  const devUrls = $derived(session?.devActive ? (session.devUrls ?? []) : []);

  const hasPr = $derived((session?.prNumber ?? 0) > 0);
  const apps = $derived(session?.stack ? (session.apps ?? []) : []);
  const moreApps = $derived(Math.max(0, apps.length - 1));
  const chipShown = $derived(hasPr || apps.length > 0);

  let pop = $state<{ x: number; y: number } | null>(null);
  function openPop(e: MouseEvent) {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    pop = { x: Math.max(4, Math.min(r.left, window.innerWidth - 344)), y: r.bottom + 4 };
  }
  function run(fn: () => void) {
    pop = null;
    fn();
  }
</script>

<div class="grid h-full min-w-0 flex-1 grid-cols-[minmax(0,1fr)_auto] items-center gap-2" {@attach widthOf((w) => (width = w))} data-session-bar>

  <div class="flex min-w-0 items-center gap-3">
    <HoverCard class="flex min-w-0 items-baseline gap-2 outline-none">
      {#snippet card()}
        <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1" data-session-facts>
          {#each hoverRows as [k, v] (k)}
            <dt class="text-faint">{k}</dt>
            <dd class="min-w-0 break-all text-ink">{v}</dd>
          {/each}
        </dl>
      {/snippet}
      {#if session}
        <Button variant="bare" size="xs" class="h-auto! min-w-0 shrink! truncate px-0! text-[13px]! font-medium text-ink" title="Rename session" onclick={() => nav.openOverlay("rename-session", sessionId)}>
          <span class="min-w-0 truncate">{title}</span>
        </Button>
      {:else}
        <span class="min-w-0 truncate font-medium text-ink">{title}</span>
      {/if}
      {#if issue}<span class="selectable shrink-0 text-sm text-faint">{issue}</span>{/if}
    </HoverCard>
    {#if ended}<span class="min-w-0 truncate text-sm text-faint" data-ended-tag>Ended{#if ended.reason}{" · "}{ended.reason}{/if}</span>{/if}

    {#if chipShown && session}
      <!-- PrBadge colours for a list, where a row's chip competes with nothing. In the session view
           each hue has one job: magenta says an agent wrote a page, info says working, warn is for
           a tier, so those three go neutral here. Red, orange and green keep their meaning. -->
      <Button variant="bare" size="xs" class="shrink-0 gap-1 rounded-md px-1! enabled:hover:bg-sel [&_.text-magenta]:text-ink-2! [&_.text-info]:text-ink-2! [&_.text-warn]:text-ink-2!" aria-haspopup="menu" aria-expanded={!!pop} aria-label="Delivery" onclick={openPop} data-delivery-chip>
        {#if hasPr}
          <PrBadge {session} delivery={session.delivery} app={focusLabel(session)} />
        {:else}
          <span class="num text-faint">{apps.length} apps</span>
        {/if}
        <span class="text-faint"><Chevron open /></span>
      </Button>
    {/if}

    {#if !narrow && session?.devCommands?.length}
      {#if devUrls.length}
        {#each devUrls as devUrl (devUrl)}
          <Button size="xs" class="shrink-0 font-mono!" title="Open {devUrl}" onclick={() => store.openURL(devUrl)} data-dev-chip>
            <span class="size-1.5 rounded-full bg-good" aria-hidden="true"></span>
            {devUrlLabel(devUrl).replace(/^(127\.0\.0\.1|localhost)/, "")} <span aria-hidden="true">↗</span>
          </Button>
        {/each}
      {:else}
        <Button size="xs" class="shrink-0" loading={store.devPending[sessionId]} title={devToggleTitle(session)} onclick={() => session && store.dev(sessionId, !session.devActive)} data-dev-chip>
          {#if session.devActive}<span class="size-1.5 rounded-full bg-good" aria-hidden="true"></span>{/if}
          {#if !store.devPending[sessionId]}<span aria-hidden="true">▷</span>{/if} Dev
        </Button>
      {/if}
    {/if}
  </div>

  <div class="flex items-center gap-2">
    {#if action && !narrow}
      <Button variant="secondary" size="xs" loading={action.loading} onclick={action.run} data-session-action>{action.label}</Button>
    {/if}
    {#if session}<Button icon size="xs" aria-label="Session actions" aria-haspopup="menu" title="Session actions" onclick={(e) => sessionMenu.open(sessionId, e)}>⋯</Button>{/if}
  </div>
</div>

{#if pop && session}
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => (pop = null)} oncontextmenu={(e) => { e.preventDefault(); pop = null; }}></div>
  <div class="fixed z-50 grid w-[340px] gap-2 rounded-lg border border-edge bg-panel p-2 text-sm shadow-xl" style="left:{pop.x}px;top:{pop.y}px" role="menu" data-delivery-pop>
    <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 px-1">
      {#if hasPr}
        <dt class="text-faint">Checks</dt><dd class="text-ink">{session.checks || "None"}</dd>
        <dt class="text-faint">Review</dt><dd class="text-ink">{session.review || "None"}</dd>
      {/if}
      {#if session.mergeQueue}
        <dt class="text-faint">Merge queue</dt><dd class="num text-ink">{session.mergeQueue.position}</dd>
      {/if}
      {#if session.overlaps?.length}
        <dt class="text-faint">Overlaps</dt><dd class="min-w-0 break-all text-ink">{session.overlaps.map((o) => o.issue || o.session).join(", ")}</dd>
      {/if}
    </dl>
    {#if apps.length}
      <ul class="grid gap-1 px-1" aria-label="Apps">
        {#each apps as a (a.name)}
          {@const st = appState(a)}
          <li class="flex min-w-0 items-center gap-1.5 whitespace-nowrap" title={a.repo || a.name}>
            <span class="min-w-0 truncate text-ink">{appLabel(a)}</span>
            <span class="text-faint" aria-hidden="true">·</span>
            {#if st === "pr"}
              <PrBadge session={appPr(a)} delivery={a.delivery ?? ""} onOpen={() => store.openURL(a.url ?? "")} />
            {:else if st === "changes"}
              <span class="text-ink-2">changes, no PR</span>
            {:else}
              <span class="text-faint">untouched</span>
            {/if}
            {#if a.ports?.length}<span class="num text-faint" title="port lease">:{a.ports.join(", :")}</span>{/if}
          </li>
        {/each}
      </ul>
      {#if moreApps > 0 && hasPr}<p class="px-1 text-faint">The chip shows the app that needs attention first, +{moreApps} {moreApps === 1 ? "app" : "apps"}.</p>{/if}
    {/if}
    <div class="grid gap-0.5 border-t border-edge/60 pt-1">
      {#if hasPr && !session.stack}
        <MenuItem trailing="↗" onclick={() => run(() => store.openURL(session.prUrl))}>Open PR</MenuItem>
      {/if}
      {#if session.location !== "ssh"}
        <MenuItem onclick={() => run(() => store.review(session.id))}>Trigger review</MenuItem>
      {/if}
    </div>
  </div>
{/if}
