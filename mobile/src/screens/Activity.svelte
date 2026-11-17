<script lang="ts" module>
  import type { EndedSession, Event as ActivityEvent } from "@bindings/internal/protocol";

  /** One entry of the flat, virtualized list: a day header or an event. */
  export type ActivityItem =
    | { key: string; kind: "day"; label: string }
    | { key: string; kind: "event"; ev: ActivityEvent }
    | { key: string; kind: "ended-head" }
    | { key: string; kind: "ended"; session: EndedSession };

  /** Ended sessions listed under "Recently ended"; the daemon keeps 14 days, a phone shows the latest. */
  export const ENDED_VISIBLE = 20;

  /** The "Recently ended" block: a header and the newest ended sessions; nothing when none ended. */
  export function endedItems(ended: readonly EndedSession[]): ActivityItem[] {
    if (ended.length === 0) return [];
    return [
      { key: "ended-head", kind: "ended-head" },
      ...ended.slice(0, ENDED_VISIBLE).map((session): ActivityItem => ({ key: `ended:${session.id}`, kind: "ended", session })),
    ];
  }

  /** A row's key is stable for the life of the event, so the passing minutes never re-mount it. */
  export const eventKey = (ev: ActivityEvent): string => `${ev.id}:${ev.at ?? ev.to}`;

  const ymd = (d: Date): string => `${d.getFullYear()}-${d.getMonth() + 1}-${d.getDate()}`;

  /**
   * Events newest first with a non-sticky day header before each run of one
   * local day: "Today", "Yesterday" or a localized date. An event with no
   * timestamp stays in the day of the one before it (the first one is today).
   */
  export function activityItems(
    events: readonly ActivityEvent[],
    now: number,
    labels: { today: string; yesterday: string },
    locale?: string,
  ): ActivityItem[] {
    const today = new Date(now);
    const yesterday = new Date(now);
    yesterday.setDate(yesterday.getDate() - 1);
    const out: ActivityItem[] = [];
    let day = "";
    for (const ev of events) {
      const at = ev.at ? new Date(ev.at) : null;
      const when = at && !Number.isNaN(at.getTime()) ? at : null;
      const id = when ? ymd(when) : day || ymd(today);
      if (id !== day) {
        day = id;
        const label =
          id === ymd(today)
            ? labels.today
            : id === ymd(yesterday)
              ? labels.yesterday
              : (when ?? today).toLocaleDateString(locale, { weekday: "long", month: "long", day: "numeric" });
        out.push({ key: `day:${id}`, kind: "day", label });
      }
      out.push({ key: eventKey(ev), kind: "event", ev });
    }
    return out;
  }
</script>

<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { talk } from "$lib/talk.svelte";
  import { untrack } from "svelte";
  import EmptyState from "@mobile/lib/components/EmptyState.svelte";
  import LargeTitle from "@mobile/lib/components/LargeTitle.svelte";
  import SectionHeader from "@mobile/lib/components/SectionHeader.svelte";
  import VirtualList from "@mobile/lib/components/VirtualList.svelte";
  import { minuteClock } from "@mobile/lib/clock.svelte";
  import { copy } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";
  import { ageLabel, ageSpoken, META_SEP } from "@mobile/lib/inbox.svelte";
  import { activityWord, statusTone } from "@mobile/lib/statustone";

  // The daemon's event feed, newest first.
  //
  // Invariants:
  //  - A row's key is `${id}:${at ?? to}`, stable for the life of the event, so
  //    the passing minutes never re-mount a row. The age comes from `at` through
  //    the shared minute clock; `ago` is the fallback only when `at` is absent.
  //    A day header is keyed by its date, so a push animates only changed rows.
  //  - This screen reads `store.activity` and nothing else from the store: the
  //    store defers that write so it never lands in the same flush as sessions.
  //  - The list is virtualized. A row opens the session it is about while that
  //    session is live or still readable as ended; an older one is plain text.
  //    "Recently ended" closes the list: sessions that left the roster, from
  //    `endedSessions` (read tier), opened into the Session screen's ended view.

  // Asked once per visit: the daemon's list of sessions whose story is still readable.
  $effect(() => {
    untrack(() => void talk.loadEnded());
  });

  const endedList = $derived(talk.endedList());
  const items = $derived([
    ...activityItems(store.activity, minuteClock.now, copy.activity.days),
    ...endedItems(endedList),
  ]);

  /** A session this row can open: live, or kept readable by the daemon. */
  const openable = (id: string): boolean => !!store.sessionById(id) || endedList.some((e) => e.id === id);
  const open = (id: string): void => router.push({ name: "session", id, seg: "conversation" });
  const endedMeta = (e: EndedSession): string =>
    [e.project ? store.displayNameFor(e.project) : "", e.issue ?? "", ageLabel(e.endedAt)].filter((p) => p !== "").join(META_SEP);
</script>

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <LargeTitle title={copy.activity.title} />

  <div class="min-h-0 flex-1">
    {#if items.length === 0}
      <EmptyState title={copy.activity.empty.body} />
    {:else}
      <VirtualList {items} key={(it) => it.key} kind={(it) => (it.kind === "day" || it.kind === "ended-head" ? "section" : "row")} label={copy.activity.title}>
        {#snippet row(it: ActivityItem)}
          {#if it.kind === "day"}
            <SectionHeader title={it.label} />
          {:else if it.kind === "ended-head"}
            <SectionHeader title={copy.activity.ended} />
          {:else if it.kind === "ended"}
            {@const e = it.session}
            <button
              type="button"
              class="flex min-h-14 w-full touch-manipulation flex-col justify-center pl-4 text-left active:bg-sel"
              aria-label={[e.title || e.issue || e.id.slice(0, 8), e.reason ?? "", endedMeta(e)].filter((p) => p !== "").join(", ")}
              onclick={() => open(e.id)}
            >
              <span class="flex min-h-14 w-full min-w-0 flex-col justify-center gap-0.5 border-b border-edge-soft py-[7px] pr-4" aria-hidden="true">
                <span class="line-clamp-2 text-body text-faint">{e.title || e.issue || e.id.slice(0, 8)}</span>
                <span class="num truncate text-subhead font-normal text-subtext">{[e.reason ?? "", endedMeta(e)].filter((p) => p !== "").join(META_SEP)}</span>
              </span>
            </button>
          {:else}
            {@const ev = it.ev}
            {@const label = [activityWord(ev.from, ev.to), ev.title, ev.issue || ev.id.slice(0, 8), ageSpoken(ev.at, ev.ago)]
              .filter((p) => p !== "")
              .join(", ")}
            {#snippet body()}
              <div class="flex min-h-14 flex-col justify-center gap-0.5 border-b border-edge-soft py-[7px] pr-4" aria-hidden="true">
                <span class="line-clamp-2 text-body text-ink">{ev.title || activityWord(ev.from, ev.to)}</span>
                <span class="flex min-w-0 items-center text-subhead font-normal text-subtext">
                  {#if ev.title}
                    <span class="shrink-0 whitespace-pre {statusTone(ev.to)}">{activityWord(ev.from, ev.to)}</span>
                    <span class="shrink-0" aria-hidden="true">{META_SEP}</span>
                  {/if}
                  <span class="num min-w-0 truncate">{ev.issue || ev.id.slice(0, 8)}</span>
                  {#if ageLabel(ev.at, ev.ago)}
                    <span class="shrink-0 whitespace-pre" aria-hidden="true">{META_SEP}</span>
                    <span class="num shrink-0 whitespace-nowrap">{ageLabel(ev.at, ev.ago)}</span>
                  {/if}
                </span>
              </div>
            {/snippet}
            {#if openable(ev.id)}
              <button
                type="button"
                class="flex min-h-14 w-full touch-manipulation flex-col justify-center pl-4 text-left active:bg-sel"
                aria-label={label}
                onclick={() => open(ev.id)}
              >
                {@render body()}
              </button>
            {:else}
              <div class="flex min-h-14 flex-col justify-center pl-4" role="group" aria-label={label}>
                {@render body()}
              </div>
            {/if}
          {/if}
        {/snippet}
      </VirtualList>
    {/if}
  </div>
</div>
