<script lang="ts">
  import { onDestroy, onMount, tick } from "svelte";
  import { store } from "$lib/store.svelte";
  import FilterIcon from "@mobile/lib/icons/FilterIcon.svelte";
  import Banner, { connectionBanner } from "@mobile/lib/components/Banner.svelte";
  import { connection } from "@mobile/lib/connection.svelte";
  import InboxCard from "@mobile/lib/components/InboxCard.svelte";
  import EmptyState from "@mobile/lib/components/EmptyState.svelte";
  import InboxRow from "@mobile/lib/components/InboxRow.svelte";
  import LargeTitle from "@mobile/lib/components/LargeTitle.svelte";
  import SectionHeader from "@mobile/lib/components/SectionHeader.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import VirtualList from "@mobile/lib/components/VirtualList.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import SegmentBar from "@mobile/lib/components/SegmentBar.svelte";
  import { copy } from "@mobile/lib/copy";
  import {
    attachInbox,
    BUCKETS,
    inbox,
    inboxItems,
    inboxView,
    itemKind,
    ItemCache,
    markInteractive,
    projectOrder,
    type InboxItem,
  } from "@mobile/lib/inbox.svelte";
  import { inboxSearch } from "@mobile/lib/inboxsearch.svelte";
  import { prefetchOnce } from "@mobile/lib/prefetch";
  import { router, type InboxFilter } from "@mobile/lib/router.svelte";

  // The Inbox tab: what needs you as cards, everything else as dense rows.
  //
  // Invariants:
  //  - The filter lives in the route, found on the stack because this screen
  //    stays mounted under a pushed session; its cards watch nothing while one is.
  //  - Grouping by project is a preference (`inboxView`), not a route: the filter
  //    still narrows the same list, and a group header is one more keyed item.
  //  - The list is one virtualized list of keyed items; a push that changes one
  //    session changes one item.
  //  - No pull to refresh: the list is pushed.
  //  - The connection banner sits BELOW the large title, above the first section.
  //    With no snapshot to show (offline or still connecting) the list is three
  //    skeleton cards under the banner, never a blank screen.
  //  - Status filter changes start a fresh scroll position. Live pushes keep the anchor.

  onMount(() => attachInbox());

  // The session screen and its segments, fetched before the first tap (spec §8.4).
  const SESSION_CHUNKS = [
    () => import("./session/Session.svelte"),
    () => import("./session/ConversationSegment.svelte"),
    () => import("./session/ChangesSegment.svelte"),
    () => import("./session/ReviewSegment.svelte"),
    () => import("./session/TerminalSegment.svelte"),
  ];
  let cancelPrefetch = (): void => {};
  onDestroy(() => cancelPrefetch());

  let painted = $state(false);
  $effect(() => {
    if (painted || !inbox.loaded) return;
    let frame = 0;
    void tick().then(() => {
      frame = requestAnimationFrame(() => {
        painted = true;
        markInteractive();
        cancelPrefetch = prefetchOnce(SESSION_CHUNKS);
      });
    });
    return () => cancelAnimationFrame(frame);
  });

  const tabRoute = $derived.by(() => {
    for (let i = router.stack.length - 1; i >= 0; i--) {
      const r = router.stack[i];
      if (r.name === "tab" && r.tab === "inbox") return r;
    }
    return null;
  });
  const filter = $derived<InboxFilter>(tabRoute?.filter ?? "");
  // Card watches hold the device's pane probes; a session pushed on top needs them.
  const onTop = $derived(router.top.name === "tab" && router.top.tab === "inbox");
  let showAllDone = $state(false);

  // One cache for the screen's life: an unchanged row keeps its item object across pushes.
  const itemCache = new ItemCache();
  const order = $derived(projectOrder(store.projects, store.groups));
  const grouping = $derived(
    inboxView.group === "project" ? { order, label: (p: string) => (p === "" ? copy.inbox.groupBy.noProject : store.displayNameFor(p)) } : undefined,
  );
  // The search narrows the models the filter and the grouping list; the all-clear block is a claim
  // about the whole Inbox, so it is not drawn over a narrowed list.
  const searching = $derived(inboxSearch.active);
  const items = $derived(
    inboxItems(inboxSearch.buckets, filter, showAllDone, itemCache, grouping).filter((it) => !searching || it.kind !== "clear"),
  );
  const grouped = $derived(inboxView.group === "project");
  const filterLabel = $derived(
    (filter === "" ? "Filter" : `Filter, showing ${copy.inbox.sections[filter]}`) +
      (grouped ? ", grouped by project" : ""),
  );

  const filterName = $derived(filter === "" ? "" : copy.inbox.sections[filter]);

  const scopes = $derived([
    { id: "" as InboxFilter, label: copy.inbox.all },
    ...BUCKETS.map(id => ({ id, label: `${copy.inbox.sections[id]} ${inboxSearch.counts[id]}` })),
  ]);

  // Nothing to show: no list yet, or an empty one while the link is down (an all-clear
  // claimed offline would be a guess).
  const noSnapshot = $derived(items.length === 0 && (!inbox.loaded || (!connection.ready && inbox.rows.size === 0)));
  const banner = $derived(connectionBanner(connection.diagnosis, connection.label));
  function bannerAction(intent: "pair" | "details" | undefined): void {
    if (intent === "pair") {
      router.stack = [{ name: "connect", state: "scan" }];
      router.sheet = "";
    } else if (intent === "details") router.open("connection");
  }
  function selectFilter(next: InboxFilter): void {
    if (next !== filter) router.replace({ name: "tab", tab: "inbox", ...(next ? { filter: next } : {}) });
  }

  const label = (p: string): string => store.displayNameFor(p);
  const open = (id: string): void => router.push({ name: "session", id, seg: "conversation" });
</script>

{#snippet allClearBlock()}
  <div class="px-4 pt-3">
    <div class="flex min-h-11 items-center gap-2 rounded-lg border border-edge-soft bg-panel px-3 py-2" role="status">
      <Icon name="circle-check" size={16} class="shrink-0 text-good" />
      <span class="text-subhead text-subtext">{copy.inbox.empty.title}</span>
    </div>
  </div>
{/snippet}

{#snippet skeletons()}
  <div class="flex flex-col py-1" aria-busy="true" data-skeleton>
    {#each [0, 1, 2] as n (n)}
      <div class="px-4 py-1.5"><div class="skeleton h-24 rounded-[14px] bg-edge-soft"></div></div>
    {/each}
  </div>
{/snippet}

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <LargeTitle title={copy.inbox.title}>
    {#snippet trailing()}
      <TouchButton icon aria-label={filterLabel} onclick={() => router.open("filter")}>
        <FilterIcon active={filter !== "" || grouped} />
      </TouchButton>
    {/snippet}
  </LargeTitle>

  {#if banner}
    <Banner
      tone={banner.tone}
      glyph={banner.glyph}
      body={banner.body}
      action={banner.action}
      role={banner.id === "key_changed" ? "alert" : "status"}
      onaction={() => bannerAction(banner.intent)}
    />
  {/if}

  {#if inbox.rows.size > 0 || searching}
    <div class="shrink-0 px-4 pb-2">
      <div class="relative flex min-h-11 items-center rounded-lg border border-edge-soft bg-panel focus-within:border-accent">
        <Icon name="search" size={16} class="pointer-events-none absolute left-2.5 text-faint" />
        <input
          type="search"
          class="min-h-11 w-full min-w-0 rounded-lg bg-transparent pr-11 pl-9 text-body text-ink outline-none placeholder:text-faint"
          aria-label={copy.inbox.search.placeholder}
          placeholder={copy.inbox.search.placeholder}
          autocapitalize="off"
          autocorrect="off"
          spellcheck="false"
          enterkeyhint="search"
          bind:value={inboxSearch.query}
        />
        {#if searching}
          <button
            type="button"
            class="absolute right-0 grid size-11 touch-manipulation place-items-center text-faint active:opacity-70"
            aria-label={copy.inbox.search.clear}
            onclick={() => inboxSearch.clear()}
          >
            <Icon name="x" size={16} />
          </button>
        {/if}
      </div>
    </div>
  {/if}

  {#if inbox.rows.size > 0 || filter}
    <div class="session-scopes shrink-0 border-b border-edge-soft px-4 pb-2">
      <SegmentBar segments={scopes} value={filter} onchange={selectFilter} label={copy.inbox.statusFilter} />
    </div>
  {/if}

  <div class="min-h-0 flex-1">
    {#if items.length > 0}
      {#key filter}
        <VirtualList {items} key={(it) => it.key} kind={itemKind} label={copy.inbox.title}>
          {#snippet row(it: InboxItem)}
            {#if it.kind === "section"}
              <SectionHeader title={copy.inbox.sections[it.bucket]} count={it.count} />
            {:else if it.kind === "group"}
              <SectionHeader title={it.label} count={it.count} />
            {:else if it.kind === "card"}
              <InboxCard
                model={it.model}
                projectLabel={label(it.model.project)}
                watch={onTop && inbox.watched.has(it.model.id)}
              />
            {:else if it.kind === "row"}
              <InboxRow
                model={it.model}
                projectLabel={label(it.model.project)}
                onopen={() => open(it.model.id)}
              />
            {:else if it.kind === "clear"}
              {@render allClearBlock()}
            {:else}
              <div class="px-4 py-2">
                <TouchButton onclick={() => (showAllDone = true)}>{copy.inbox.showAll} ({it.hidden})</TouchButton>
              </div>
            {/if}
          {/snippet}
        </VirtualList>
      {/key}
    {:else if noSnapshot}
      {#if banner}
        {@render skeletons()}
      {:else}
        <div class="flex h-full items-center justify-center" aria-busy="true">
          <p class="text-subhead font-normal text-faint" role="status">{copy.inbox.connecting.status}</p>
        </div>
      {/if}
    {:else if searching}
      <EmptyState title={copy.inbox.search.none} />
    {:else if filter === ""}
      <EmptyState icon="square-terminal" title={copy.inbox.noSessions} />
    {:else}
      <EmptyState title={filterName} body={copy.activity.empty.body} />
    {/if}
  </div>
</div>

<style>
  .session-scopes :global(.segment-track) { min-height: 44px; }
  .skeleton {
    animation: skeleton-pulse 1.4s ease-in-out infinite;
  }
  @keyframes skeleton-pulse {
    50% {
      opacity: 0.55;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .skeleton {
      animation: none;
    }
  }
</style>
