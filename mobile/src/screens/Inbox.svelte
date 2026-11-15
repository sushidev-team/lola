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
  import { copy, fill } from "@mobile/lib/copy";
  import {
    attachInbox,
    inbox,
    inboxItems,
    itemKind,
    ItemCache,
    markInteractive,
    unwindowed,
    type InboxItem,
  } from "@mobile/lib/inbox.svelte";
  import { prefetchOnce } from "@mobile/lib/prefetch";
  import { router, type InboxFilter } from "@mobile/lib/router.svelte";

  // The Inbox tab: what needs you as cards, everything else as dense rows.
  //
  // Invariants:
  //  - The filter lives in the route, found on the stack because this screen
  //    stays mounted under a pushed session; its cards watch nothing while one is.
  //  - The list is one virtualized list of keyed items; a push that changes one
  //    session changes one item.
  //  - No pull to refresh: the list is pushed.
  //  - The connection banner sits BELOW the large title, above the first section.
  //    With no snapshot to show (offline or still connecting) the list is three
  //    skeleton cards under the banner, never a blank screen.
  //  - Nothing animates before the first list is painted, or once the tab is
  //    left: only a row that changed while the list is shown moves.

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
  const items = $derived(inboxItems(inbox.buckets, filter, showAllDone, itemCache));
  // Off once the tab is left, so a screen being torn down plays no leave.
  const animate = $derived(painted && tabRoute !== null && unwindowed(items.length));
  const filterLabel = $derived(
    filter === "" ? "Filter" : `Filter, showing ${copy.inbox.sections[filter]}`,
  );

  const filterName = $derived(filter === "" ? "" : copy.inbox.sections[filter]);

  const counts = $derived(inbox.counts);
  const chipLabel = $derived(filter === "" ? "" : `${filterName} · ${counts[filter]}`);

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
  function clearFilter(): void {
    router.replace({ name: "tab", tab: "inbox" });
  }

  const label = (p: string): string => store.displayNameFor(p);
  const open = (id: string): void => router.push({ name: "session", id, seg: "conversation" });
</script>

{#snippet allClearBlock()}
  <div class="px-4 py-1.5">
    <div class="flex h-16 items-center gap-3 rounded-[10px] bg-edge-soft p-4" role="status">
      <Icon name="circle-check" size={20} class="shrink-0 text-good" />
      <span class="min-w-0 truncate text-headline text-ink">{copy.inbox.empty.title}</span>
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
        <FilterIcon active={filter !== ""} />
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

  {#if filter !== ""}
    <div class="flex shrink-0 px-4 pb-2">
      <button
        type="button"
        class="inline-flex h-8 touch-manipulation items-center gap-1.5 rounded-full bg-sel pr-2.5 pl-3 text-subhead text-ink active:opacity-70"
        aria-label={fill(copy.inbox.clearFilter, { filter: filterName, name: filterName })}
        onclick={clearFilter}
      >
        <span>{chipLabel}</span>
        <Icon name="x" size={14} class="text-faint" />
      </button>
    </div>
  {/if}

  <div class="min-h-0 flex-1">
    {#if items.length > 0}
      <VirtualList {items} key={(it) => it.key} kind={itemKind} label={copy.inbox.title}>
        {#snippet row(it: InboxItem)}
          {#if it.kind === "section"}
            <SectionHeader title={copy.inbox.sections[it.bucket]} count={it.count} />
          {:else if it.kind === "card"}
            <InboxCard
              model={it.model}
              projectLabel={label(it.model.project)}
              watch={onTop && inbox.watched.has(it.model.id)}
              {animate}
            />
          {:else if it.kind === "row"}
            <InboxRow
              model={it.model}
              projectLabel={label(it.model.project)}
              {animate}
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
    {:else if noSnapshot}
      {#if banner}
        {@render skeletons()}
      {:else}
        <div class="flex h-full items-center justify-center" aria-busy="true">
          <p class="text-subhead font-normal text-faint" role="status">{copy.inbox.connecting.status}</p>
        </div>
      {/if}
    {:else if filter === ""}
      {@render allClearBlock()}
    {:else}
      <EmptyState title={filterName} body={copy.activity.empty.body} />
    {/if}
  </div>
</div>

<style>
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
