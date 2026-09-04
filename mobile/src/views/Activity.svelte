<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { eventPhrase, statusText } from "$lib/theme";

  // The daemon's event feed: every status transition it recorded, newest first.
  //
  // IT DRAWS ITS OWN ROWS NOW, and the reason is that it stopped being a widget.
  // This screen used to mount `$lib/components/ActivityFeed.svelte` unchanged —
  // the desktop's sidebar track — which was right while the feed was one of
  // several things sharing a screen. It is a TAB now, with the whole display to
  // itself, and that component is shaped for a rail: it caps at 30 rows, packs
  // 11px lines four points apart, and says "no activity yet" in the desktop's
  // lower case. On a 390-point screen holding nothing else, that reads as a
  // panel somebody forgot to finish.
  //
  // WHAT THE ROWS GAINED: the issue TITLE, which the event has carried all along
  // (`protocol.Event.Title`) and the rail had no width for — so a row says what
  // changed rather than only which ticket it happened to. The transition and the
  // age are unchanged, and the vocabulary is still `$lib/theme`'s `eventPhrase`
  // and `statusText`, the port of Go's internal/state that
  // desktop/state_parity_test.go pins. Nothing here spells a status word.
  //
  // THE ONE RULE THIS FILE MUST KEEP is the reason ActivityFeed's own header
  // calls itself the only reader of `store.activity`: `store.setActivity` defers
  // its write to a macrotask so an activity read never lands in the same flush
  // as a sessions read, and that same-flush corruption is what once froze the
  // sessions list. Reading the feed HERE is fine; reading it BESIDE
  // `store.sessions` or `store.projects` in one component is not. So this screen
  // reads exactly one field of the store, and every fact on a row comes off the
  // event itself.
  //
  // Which is also why the rows are NOT tappable. Opening a session needs its
  // pane name, and that means reading `store.sessions` in this component — the
  // exact pairing above. An event is a fact the daemon recorded and the session
  // it names is one tab away; a row is not worth the hazard.
  //
  // NO CAP. The rail sliced to 30 because rows nobody can scroll to are retained
  // memory; a full-height scroller is the case where they can be scrolled to.
  // The daemon bounds what it sends on its own side.
</script>

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <!-- The redesign's screen header: a large title over one line of subtitle.
       The top inset is spelled out at the point of use rather than taken from
       `pt-safe-t`, because App.svelte's development banner sets
       `--lola-top-inset: 0px` on the container holding the screens when it has
       already paid it — a custom property is substituted on the element it is
       DECLARED on, so a var() baked into the spacing token could never see the
       override (see app.css). The 6px is `pt-1.5` written as a literal: spacing
       is pinned to px so it does not scale with Dynamic Type, and a rem here
       would quietly reintroduce that. -->
  <header
    class="flex shrink-0 flex-col gap-0.5 px-5 pb-3"
    style="padding-top: calc(var(--lola-top-inset, env(safe-area-inset-top, 0px)) + 6px)"
  >
    <h1 class="flex h-11 items-center text-2xl text-ink">Activity</h1>
    <!-- `text-body`, not the `text-base` the Sessions header uses. That one is a
         facts line — three counts and a host name — and reads as data at the row
         size. This is a HINT, which the brief's scale puts one step down. -->
    <span class="truncate text-body text-subtext">Status changes, newest first</span>
  </header>

  <!-- No bottom safe-area spacer: the tab bar below this screen pays that inset
       itself, and a screen that paid it too would leave a band of canvas
       between the last event and a bar already clear of the home indicator. -->
  <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain pb-4">
    {#if store.activity.length === 0}
      <div class="flex flex-col items-center gap-2 px-8 py-12 text-center">
        <span class="text-lg text-ink">No activity yet</span>
        <!-- WHAT AN EMPTY FEED MEANS, because the two causes look identical and
             only one is a problem. The list screen's empty states make the same
             distinction for the same reason. -->
        <span class="copy text-body text-faint">
          Transitions appear here as the daemon records them. A quiet feed means
          nothing has changed state, not that nothing is running.
        </span>
      </div>
    {:else}
      <ul>
        <!-- Keyed on the same tuple the rail used: an id alone is not unique —
             one session transitions repeatedly — and the age is rewritten on
             every push, which together make the pair stable enough to key on. -->
        {#each store.activity as ev (ev.id + ev.to + ev.ago)}
          <li class="flex flex-col gap-[3px] border-b border-edge-soft px-5 py-[11px]">
            <!-- The brief's compact row: what happened leads at the row size and
                 the facts follow one tier down. The TITLE is untrusted — it is a
                 Linear issue title — so it is a text node, clamped to two lines
                 because it is the only free text here and the only thing that
                 can be long. -->
            {#if ev.title}
              <span class="line-clamp-2 text-base font-medium text-ink">{ev.title}</span>
            {:else}
              <!-- No title on the record: an adopted session, or one the daemon
                   knew about before Linear answered. The transition carries the
                   row rather than leaving it blank. -->
              <span class="text-base font-medium text-ink">{eventPhrase(ev.from, ev.to)}</span>
            {/if}

            <div class="flex items-center gap-1.5 text-sm">
              <!-- The transition, in the colour of the state it ARRIVED at, from
                   the shared table. Drawn here only when the title took the line
                   above; the `{#if}` is what stops it being said twice. -->
              {#if ev.title}
                <span class="min-w-0 truncate {statusText(ev.to)}">
                  {eventPhrase(ev.from, ev.to)}
                </span>
                <span class="shrink-0 text-edge" aria-hidden="true">·</span>
              {/if}
              <!-- `shrink-0`: an issue key is seven characters and is the row's
                   citation handle. "NO…" is not a citation. A short id stands in
                   for a session that never had one. -->
              <span class="num shrink-0 text-faint">{ev.issue || ev.id.slice(0, 8)}</span>
              <span class="flex-1"></span>
              <!-- `num` is tabular figures: every age is rewritten on each push,
                   and proportional digits would nudge the column on all of
                   them. -->
              <span class="num shrink-0 text-faint">{ev.ago}</span>
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>
