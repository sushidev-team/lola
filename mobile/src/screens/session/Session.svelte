<script lang="ts" module>
  import type { Component } from "svelte";
  import type { Seg } from "@mobile/lib/router.svelte";
  import { changesOffered } from "$lib/sessionview";
  import { offeredSegments } from "./segments";

  type Loaded = { default: Component<{ id: string; pane?: string; packages: number }> };

  // One lazy chunk per segment; Conversation is the screen's own default and
  // loads with it, the others when first shown.
  const SEGMENTS = {
    conversation: () => import("./ConversationSegment.svelte"),
    changes: () => import("./ChangesSegment.svelte"),
    review: () => import("./ReviewSegment.svelte"),
    terminal: () => import("./TerminalSegment.svelte"),
  } as const satisfies Record<Seg, () => Promise<unknown>>;

  const loaded = new Map<Seg, Promise<Loaded>>();
  function segment(seg: Seg): Promise<Loaded> {
    let p = loaded.get(seg);
    if (!p) {
      p = SEGMENTS[seg]() as Promise<Loaded>;
      loaded.set(seg, p);
    }
    return p;
  }

  export { offeredSegments };
</script>

<script lang="ts">
  import { untrack } from "svelte";
  import { DaemonService } from "@bindings/desktop";
  import { store } from "$lib/store.svelte";
  import { talk } from "$lib/talk.svelte";
  import EndedView from "$lib/components/session/EndedView.svelte";
  import SegmentBar from "@mobile/lib/components/SegmentBar.svelte";
  import { copy } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";
  import { can } from "@mobile/lib/tiers.svelte";
  import SessionHeader from "./SessionHeader.svelte";

  // The Session screen (spec §4.4, §5.2). The root pays the keyboard's height as
  // bottom padding so the Workspace column shrinks with it; the tab bar is never
  // drawn here. A session that left the store shows its ended story, or says it
  // ended once nothing is left to read.

  const route = $derived(router.top.name === "session" ? router.top : null);
  const id = $derived(route?.id ?? "");
  const seg = $derived(route?.seg ?? "conversation");
  const pane = $derived(route?.pane);
  const session = $derived(store.sessionById(id));
  const missing = $derived(id !== "" && !session);
  const ended = $derived(missing ? talk.ended(id) : undefined);

  $effect(() => {
    if (missing) untrack(() => void talk.loadEnded());
  });

  // Review packages exist with or without a PR, so any checkout is asked once per
  // session; the effect reads only primitives, so a sessions push that leaves them
  // alone asks nothing.
  let packages = $state({ id: "", n: 0 });
  const packageCount = $derived(packages.id === id ? packages.n : 0);
  const hasCheckout = $derived(!!session && changesOffered(session));
  $effect(() => {
    const sid = id;
    if (!sid || !hasCheckout || !can("reviewPackages")) return;
    let live = true;
    DaemonService.ReviewPackages(sid)
      .then((d) => {
        if (live) packages = { id: sid, n: d?.packages?.length ?? 0 };
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  });

  const offered = $derived(offeredSegments(session, packageCount));
  const segments = $derived(offered.map((s) => ({ id: s, label: copy.session.segments[s] })));

  // A deep link or a vanished PR can name a segment this session does not offer.
  $effect(() => {
    if (session && route && !offered.includes(route.seg)) {
      untrack(() => router.replace({ name: "session", id: route.id, seg: "conversation" }));
    }
  });

  function choose(next: Seg): void {
    if (route) router.replace({ name: "session", id: route.id, seg: next, ...(next === "terminal" && pane ? { pane } : {}) });
  }
</script>

<div
  class="kb-pad grid h-full min-h-0 grid-cols-[minmax(0,1fr)] grid-rows-[auto_auto_minmax(0,1fr)] bg-canvas text-ink"
  data-phone-desk
  data-session-screen
>
  <div class="min-w-0 pt-[var(--lola-top-inset,env(safe-area-inset-top,0px))]">
    <SessionHeader {session} {id} onback={() => router.back()} onmenu={() => router.open("sessionMenu")} />
  </div>
  <div class="min-w-0 px-4 pt-1 pb-2">
    {#if session}
      <SegmentBar {segments} value={seg} onchange={choose} />
    {/if}
  </div>
  <div class="min-h-0 min-w-0">
    {#if missing}
      {#if ended}
        <EndedView sessionId={ended.id} />
      {:else}
        <div class="grid h-full place-content-center gap-1 px-6 text-center">
          <h2 class="text-headline">{copy.session.gone.title}</h2>
          <p class="text-subhead text-faint">{copy.session.gone.body}</p>
        </div>
      {/if}
    {:else}
      {#key seg}
        {#await segment(seg) then m}
          {@const Segment = m.default}
          <Segment {id} {pane} packages={packageCount} />
        {/await}
      {/key}
    {/if}
  </div>
</div>
