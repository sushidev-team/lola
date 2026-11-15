<script lang="ts" module>
  import type { Event as ActivityEvent } from "@bindings/internal/protocol";

  /** One entry of the flat, virtualized list: a day header or an event. */
  export type ActivityItem =
    | { key: string; kind: "day"; label: string }
    | { key: string; kind: "event"; ev: ActivityEvent };

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
  import EmptyState from "@mobile/lib/components/EmptyState.svelte";
  import LargeTitle from "@mobile/lib/components/LargeTitle.svelte";
  import SectionHeader from "@mobile/lib/components/SectionHeader.svelte";
  import VirtualList from "@mobile/lib/components/VirtualList.svelte";
  import { minuteClock } from "@mobile/lib/clock.svelte";
  import { copy } from "@mobile/lib/copy";
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
  //  - The list is virtualized; events are display, not links.

  const items = $derived(
    activityItems(store.activity, minuteClock.now, copy.activity.days),
  );
</script>

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <LargeTitle title={copy.activity.title} />

  <div class="min-h-0 flex-1">
    {#if store.activity.length === 0}
      <EmptyState title={copy.activity.empty.body} />
    {:else}
      <VirtualList {items} key={(it) => it.key} kind={(it) => (it.kind === "day" ? "section" : "row")} label={copy.activity.title}>
        {#snippet row(it: ActivityItem)}
          {#if it.kind === "day"}
            <SectionHeader title={it.label} />
          {:else}
            {@const ev = it.ev}
            <div
              class="flex min-h-14 flex-col justify-center pl-4"
              role="group"
              aria-label={[activityWord(ev.from, ev.to), ev.title, ev.issue || ev.id.slice(0, 8), ageSpoken(ev.at, ev.ago)]
                .filter((p) => p !== "")
                .join(", ")}
            >
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
            </div>
          {/if}
        {/snippet}
      </VirtualList>
    {/if}
  </div>
</div>
