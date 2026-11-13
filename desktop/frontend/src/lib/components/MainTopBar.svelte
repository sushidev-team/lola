<script lang="ts">
  import GlyphIcon from "$lib/components/GlyphIcon.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import { store, scopedSessions } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { triaged } from "$lib/filters";
  import Button from "./Button.svelte";
  import { budgetLabel, headerLabel, headerLevel } from "$lib/usage";
  import QuotaBars from "./QuotaBars.svelte";
  import HoverCard from "./HoverCard.svelte";
  import SpendCard from "./SpendCard.svelte";
  import SessionBar from "./session/SessionBar.svelte";
  import { waitingQueue } from "$lib/queue";
  import { ssh } from "$lib/ssh.svelte";

  // The main column's 44px context header. It replaces the old full-width vitals
  // bar: it starts at the SIDEBAR's right edge, not the window's, so together
  // with the sidebar's own 44px brand row the entire top 44px of the window is
  // one continuous drag band in both sidebar states.
  //
  // Reads `store` and `nav` directly (leaf component) — it is never handed a
  // title or a count by App. See Sidebar.svelte for why.

  type Crumb = { text: string; onclick?: () => void };

  const projectName = $derived(store.displayNameFor(nav.project));

  // A session fills <main>, and this bar becomes its bar.
  const inSession = $derived(!!nav.focusedTerm && nav.view === "cockpit");
  // The sidebar carries the Waiting line. Hidden, it needs a way to say the same thing, or a
  // session waiting on you is invisible from inside another one.
  const waiting = $derived(nav.sidebarOpen ? 0 : waitingQueue(store.rosterSessions).length);

  const crumbs = $derived.by<Crumb[]>(() => {
    switch (nav.view) {
      case "home":
        return [{ text: "Projects" }];
      case "team":
        return [{ text: "Team" }];
      case "detail":
        return [{ text: "Projects", onclick: () => nav.goHome() }, { text: projectName }];
      case "prpicker":
        return [{ text: projectName, onclick: () => nav.goDetail(nav.project) }, { text: "Open a PR" }];
      case "ticketpicker":
        return [
          { text: projectName, onclick: () => nav.goDetail(nav.project) },
          { text: "Start a ticket" },
        ];
      default: {
        const c: Crumb[] = nav.scoped
          ? [{ text: "All", onclick: () => nav.goCockpit("") }, { text: projectName }]
          : [{ text: "Sessions" }];
        // The filter is a crumb, and clicking it is how you drop it — the same
        // affordance as Escape, for people who never learn Escape.
        if (nav.triage) c.push({ text: nav.triage, onclick: () => nav.setTriage("") });
        return c;
      }
    }
  });

  // The number of rows actually visible below — the count that used to sit in
  // the sessions panel header.
  const count = $derived(triaged(scopedSessions(store.sessions, nav.scoped, nav.project), nav.triage).length);

  // Mirrors <SidebarStatus>'s health read so the collapsed-sidebar alarm below
  // fires on the same condition the sidebar chip does.
  const daemonOk = $derived(
    store.alive && !!store.status && store.status.runtimeOk && store.status.linearOk,
  );
  const daemonHealth = $derived(
    store.status
      ? `runtime ${store.status.runtimeOk ? "✓" : "✗"} · linear ${store.status.linearOk ? "✓" : "✗"}`
      : "daemon health unknown",
  );

  // The agents' SUBSCRIPTION limits (Claude 5h/7d, Codex 7d) when any agent has
  // reported them, else today's tokens; plus the share of [budget].daily_tokens.
  // Quiet grey until 80% (of a limit or the budget), warn from there, bad from
  // 95% of a limit or once the budget is reached — and bad while [load] holds
  // dispatch, since new tickets are then silently waiting and the tooltip says why.
  const spend = $derived(store.status?.usage ?? null);
  const spendCls = $derived.by(() => {
    if (!spend) return "";
    const level = headerLevel(spend);
    if (level === "over" || spend.load?.busy) return "text-bad";
    return level === "near" ? "text-warn" : "text-faint";
  });

</script>

<header
  class="drag flex h-11 min-w-0 items-center gap-2 bg-canvas pr-3 {inSession ? '' : 'border-b border-edge'} {nav.sidebarOpen
    ? 'pl-3'
    : 'pl-[82px]'}"
>
  {#if !nav.sidebarOpen}
    <!-- Placed AFTER the traffic-light padding, never inside it. -->
    <Button icon title="show sidebar (⌘B)" aria-label="Show sidebar" onclick={() => nav.toggleSidebar()}><Icon name="chevrons-right" size={14} /></Button>
  {/if}

  {#if waiting > 0}
    <Button size="xs" class="num shrink-0 text-orange!" title="{waiting} waiting on you (⌘J)" aria-label="{waiting} waiting on you" onclick={() => ssh.nextWaiting()} data-waiting-badge>
      <span aria-hidden="true"><Icon name="circle-alert" size={14} /></span>{waiting}
    </Button>
  {/if}

  {#if inSession}
    <SessionBar sessionId={nav.focusedTerm} />
  {/if}

  {#if nav.view !== "cockpit"}
    <Button
      icon
      size="xs"
      title="back to sessions (Esc)"
      aria-label="Back to sessions"
      onclick={() => nav.goCockpit(nav.scoped ? nav.project : "")}
    >
      <Icon name="chevron-left" class="size-3.5" />
    </Button>
  {/if}

  {#if !inSession}
  <nav class="flex min-w-0 items-center gap-2 text-sm font-normal" aria-label="Breadcrumb">
    {#each crumbs as c, i (c.text + i)}
      {#if i > 0}
        <Icon name="slash" class="h-3 w-2 shrink-0 text-faint" size={14} />
      {/if}
      {#if c.onclick}
        <button class="min-w-0 truncate rounded py-1 text-faint hover:text-ink" onclick={c.onclick}>{c.text}</button>
      {:else}
        <span class="min-w-0 truncate" class:text-faint={i < crumbs.length - 1} aria-current={i === crumbs.length - 1 ? "page" : undefined}>{c.text}</span>
      {/if}
    {/each}
    {#if nav.view === "cockpit"}
      <span class="num shrink-0 text-sm text-faint">{count}</span>
    {/if}
  </nav>
  {/if}

  <span class="ml-auto flex shrink-0 items-center gap-2">
    {#if !inSession}
    <Button size="xs" onclick={() => nav.openOverlay("commands")} title="Search projects, sessions, and actions (⌘K)">Search <span class="text-faint">⌘K</span></Button>
    {/if}
    {#if spend}
      <HoverCard class="num inline-flex items-center text-sm whitespace-nowrap outline-none {spendCls}">
        {#snippet card()}<SpendCard usage={spend} />{/snippet}
        {#if spend.quotas?.length}
          <!-- Limits as bars; each bar carries its own tone, so the wrapper's
               colour only reaches the budget and load text beside them. -->
          <QuotaBars quotas={spend.quotas} />{#if budgetLabel(spend)}<span class="ml-2">· {budgetLabel(spend)}</span>{/if}
        {:else}
          {headerLabel(spend)}
        {/if}{#if spend.load?.busy}<span class="ml-1.5">· load busy</span>{/if}
      </HoverCard>
    {/if}
    <!-- Daemon alarm, ONLY while the sidebar is collapsed. <SidebarStatus> is the
         permanent home for liveness, but it lives inside the collapsible <aside>,
         and `sidebarOpen` persists to localStorage — so with the sidebar hidden a
         dead daemon had no indicator anywhere: the store keeps serving its last
         snapshot, and SessionsEmpty's "daemon isn't running" hero only renders
         when the list is empty. Silent while everything is fine, so the bar stays
         quiet; opens the doctor, same as the sidebar chip. -->
    {#if !nav.sidebarOpen && store.connected && !daemonOk}
      <!-- Trailing `!` on the colour: it has to beat the variant's own text-faint,
           and equal-specificity utilities are resolved by Tailwind's sheet order,
           not by the class attribute. -->
      <Button
        class={store.alive ? "text-warn!" : "text-bad!"}
        title="{daemonHealth} · open doctor"
        onclick={() => nav.openOverlay("doctor")}
      >
        <span aria-hidden="true"><GlyphIcon glyph={store.alive ? "▲" : "○"} /></span>
        <span>{store.alive ? "Degraded" : "Daemon down"}</span>
      </Button>
    {/if}

  </span>
</header>
