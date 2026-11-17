<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import { onDestroy, untrack, type Snippet } from "svelte";
  import TouchButton from "./TouchButton.svelte";
  import { PANE_KIND_AGENT, type PaneInfo } from "@mobile/wire";
  import { connection } from "@mobile/lib/connection.svelte";
  import { overflowFade, type OverflowEdges } from "@mobile/lib/edgefade";
  import { LONG_PRESS_MS, beginKey, cancelKey, moveKey, type KeyGesture } from "@mobile/lib/keygesture";
  import { paneTabs } from "@mobile/lib/panetabs.svelte";

  // The tab strip over one session's panes: agent, shells, dev tabs, review.
  //
  // The inventory and what is done to it live in `panetabs.svelte.ts`; this file
  // keeps the gestures and the markup.
  //
  // The inventory reloads when the session changes, when `refreshKey` is bumped
  // (the screen saw a pane exit or an attach refused), and when the connection
  // comes back. There is no timer: a tmux listing every few seconds is wasted on
  // a screen that usually shows one pane.
  //
  // A capability gap (a daemon without `cmd=panes`) draws a sentence and a
  // disabled "+"; every other failed load is silent because the terminal already
  // says what is wrong.

  let {
    /** The session whose panes these are. */
    session,
    /** The pane the screen is currently attached to. */
    active,
    /** The id of the region these tabs control, for `aria-controls`. */
    panelId,
    /** Bumped by the screen when a fact only it can see invalidates the inventory. */
    refreshKey = 0,
    /** What the phone can show, in cells; a new shell is born at this size. */
    capacity = { cols: 0, rows: 0 },
    /** Whether this phone may start shells (the `shell` tier). */
    canShell = true,
    /** Pinned after the "+", outside the scroller (the view-settings button). */
    trailing,
    /** Attach to a pane. */
    onselect,
    /** Something the user needs told, in the daemon's own sentence where it has one. */
    onnotice,
    /** A tab was held (or its menu key pressed): open the menu for that pane. */
    onmenu,
    /** The live inventory after every successful load. */
    onpanes,
  }: {
    session: string;
    active: string;
    panelId?: string;
    refreshKey?: number;
    capacity?: { cols: number; rows: number };
    canShell?: boolean;
    trailing?: Snippet;
    onselect: (pane: string) => void;
    onnotice?: (message: string) => void;
    onmenu?: (pane: string) => void;
    onpanes?: (session: string, names: string[]) => void;
  } = $props();

  paneTabs.bind(() => ({ session, active, capacity, canShell, onselect, onnotice, onpanes }));
  onDestroy(() => paneTabs.release());

  const NONE: OverflowEdges = { left: false, right: false };
  let edges = $state<OverflowEdges>(NONE);
  let strip = $state<HTMLDivElement | undefined>();

  function openMenu(pane: string): void {
    // The agent pane is the session itself: it has no menu.
    if (paneTabs.panes.find((x) => x.name === pane)?.kind === PANE_KIND_AGENT) return;
    paneTabs.menuPane = pane;
    onmenu?.(pane);
  }

  // One effect for both triggers. The plain `let` is an edge detector and must
  // not be `$state`: an effect that reads and writes the same rune loops. The
  // load is untracked because it reads `panes` and `active` synchronously.
  let lastSession = "";
  $effect(() => {
    const id = session;
    void refreshKey;
    const switched = id !== lastSession;
    lastSession = id;
    // A session switch is not news; the user asked for it.
    void untrack(() => paneTabs.load(id, !switched));
  });

  // Back from the pocket: anything can have happened on the Mac meanwhile.
  // Only the false -> true edge, so the first connection does not double the load.
  let wasReady = connection.ready;
  $effect(() => {
    const ready = connection.ready;
    const back = ready && !wasReady;
    wasReady = ready;
    if (back && session !== "") void untrack(() => paneTabs.load(session, true));
  });

  // Keep the attached tab on screen, and carry focus with an arrow-key walk.
  let takeFocus = false;
  $effect(() => {
    void active;
    void paneTabs.panes;
    const el = strip?.querySelector<HTMLElement>("[data-pane-selected='true']");
    if (!el || typeof el.scrollIntoView !== "function") return;
    if (takeFocus) {
      takeFocus = false;
      try {
        el.focus({ preventScroll: true });
      } catch {
        el.focus();
      }
    }
    try {
      el.scrollIntoView({ block: "nearest", inline: "nearest" });
    } catch {
      /* no options overload; the tab stays put */
    }
  });

  // A hold must not outlive the strip: its timer opens a menu for a screen that
  // is gone.
  onDestroy(clearHold);

  // --- The long press ---------------------------------------------------------
  //
  // A press that travels is a scroll: the strip is wall-to-wall tabs, so a
  // sideways swipe necessarily begins on one. The accessory bar's gate
  // (`beginKey`/`moveKey`/`cancelKey`, an 8px slop) is reused; only the timer is
  // local. `preventDefault` on pointerdown is NOT called, or the tap's click
  // would be suppressed.

  let gesture: KeyGesture | undefined;
  let holdTimer: ReturnType<typeof setTimeout> | undefined;
  /**
   * The lift that ends a long press must not also select the tab. Cleared by
   * the NEXT press, not by the click it suppresses: a menu dismissed on the
   * backdrop never produces that click.
   */
  let suppressClick = false;

  function clearHold(): void {
    if (holdTimer === undefined) return;
    clearTimeout(holdTimer);
    holdTimer = undefined;
  }

  function holdStart(e: PointerEvent, p: PaneInfo): void {
    // The first finger and the main button only. Compared against `false`, not
    // coerced: an event with no `isPrimary` must still open the menu.
    if (e.isPrimary === false || (e.button ?? 0) !== 0) return;
    clearHold();
    if (p.kind === PANE_KIND_AGENT) return;
    suppressClick = false;
    gesture = beginKey(e.clientX, e.clientY);
    holdTimer = setTimeout(() => {
      holdTimer = undefined;
      if (!gesture || gesture.cancelled) return;
      suppressClick = true;
      openMenu(p.name);
    }, LONG_PRESS_MS);
  }

  function holdMove(e: PointerEvent): void {
    if (!gesture) return;
    const next = moveKey(gesture, e.clientX, e.clientY);
    if (next === gesture) return;
    gesture = next;
    if (next.cancelled) clearHold();
  }

  /** The finger lifted, or the browser took the gesture over for its scroll. */
  function holdEnd(): void {
    if (gesture) gesture = cancelKey(gesture);
    gesture = undefined;
    clearHold();
  }

  /**
   * Arrow-key walk along the strip (the tabs carry a roving tabindex, so without
   * it every unselected pane is unreachable by a hardware keyboard), and
   * ContextMenu / Shift+F10 for the menu. An arrow both moves and attaches.
   */
  function onkeydown(e: KeyboardEvent, p: PaneInfo): void {
    const panes = paneTabs.panes;
    if (e.altKey || e.ctrlKey || e.metaKey || panes.length === 0) return;
    if (e.key === "ContextMenu" || (e.shiftKey && e.key === "F10")) {
      if (p.kind === PANE_KIND_AGENT) return;
      e.preventDefault();
      openMenu(p.name);
      return;
    }
    const here = panes.findIndex((x) => x.name === active);
    let next = -1;
    switch (e.key) {
      case "ArrowRight":
        next = here < 0 ? 0 : Math.min(panes.length - 1, here + 1);
        break;
      case "ArrowLeft":
        next = here < 0 ? 0 : Math.max(0, here - 1);
        break;
      case "Home":
        next = 0;
        break;
      case "End":
        next = panes.length - 1;
        break;
      default:
        return;
    }
    e.preventDefault();
    const target = panes[next];
    if (!target || target.name === active) return;
    takeFocus = true;
    onselect(target.name);
  }

  // `canCreateShell` folds a shell cap and a missing worktree into one boolean,
  // so the disabled "+" names both rather than guessing which.
  const CANNOT_CREATE = "No worktree, or too many shells.";
  const NO_TIER = "This phone can't do that.";
  const NEEDS_NEWER_DAEMON = "New shells need a newer Lola on the Mac.";

  const canCreate = $derived(canShell && paneTabs.canCreateShell);
  const createLabel = $derived(
    !canShell
      ? NO_TIER
      : paneTabs.canCreateShell
        ? "New shell"
        : paneTabs.unsupported
          ? NEEDS_NEWER_DAEMON
          : CANNOT_CREATE,
  );

  // `min-w-0` lets a flex child shrink so `overflow-x-auto` can engage;
  // `overscroll-x-contain` keeps a swipe past the last tab off the WebView's
  // back gesture. Horizontal padding stays on the ROW: WebKit drops a
  // horizontal scroller's trailing padding.
  const STRIP =
    "flex h-full min-w-0 flex-1 items-center gap-4 overflow-x-auto overscroll-x-contain [scrollbar-width:none]";
</script>

<!-- Nothing to draw until the inventory has answered. An empty strip that fills
     in a moment later reads as a layout fault. The one failure that DOES draw
     is the capability gap, which would otherwise be an absence with no
     explanation anywhere. -->
{#if paneTabs.panes.length > 0 || paneTabs.unsupported || trailing}
  <!-- `h-11` is the design's strip height AND the tap-target floor at once,
       which is what lets the padding go (see STRIP). `border-edge-soft` because
       every list hairline in the redesign is the soft token; plain `edge` is the
       panel border and is visibly heavier than this rule wants to be.

       `bg-canvas` rather than the old `bg-panel`: the strip is part of the
       screen's own chrome now, not a raised surface, and the context card
       directly beneath it is `bg-panel` — two panels stacked with only a
       hairline between them read as one box that has been ruled, which is the
       opposite of what the card is for. -->
  <div class="relative flex h-9 shrink-0 items-center border-b border-edge-soft bg-canvas pr-1 pl-4">
    {#if paneTabs.unsupported}
      <!-- The strip's place, holding the reason it is not a strip. Faint and
           one line, because it is a note about the Mac rather than an error in
           this session — the pane below is streaming perfectly well. -->
      <span class="min-w-0 flex-1 truncate py-3 text-sm text-faint">
        Panes need a newer Lola on the Mac
      </span>
    {:else if paneTabs.panes.length > 0}
      <div
        bind:this={strip}
        class={STRIP}
        role="tablist"
        aria-label="Session panes"
        aria-orientation="horizontal"
        use:overflowFade={{ onedges: (e) => (edges = e) }}
      >
        {#each paneTabs.panes as p (p.name)}
          {@const on = p.name === active}
          <!-- Hand-rolled rather than a <TouchButton>, which is the documented
               exception: Button sets `aria-pressed` for `selected`, and
               aria-pressed on a role="tab" is an invalid pairing. The desktop's
               Tabs.svelte is hand-rolled for the same reason. The 44pt floor is
               kept here instead, as `h-11`.

               `onclick` STILL CARRIES THE TAP, unlike the accessory bar, whose
               keys fire on a settled pointerdown because a bar key sends bytes
               into a live coding agent. Selecting a tab sends nothing and is
               undone by selecting another, and WebKit already suppresses the
               click when a touch turns into a scroll. The pointer handlers
               beside it are the LONG PRESS only — they never select — which is
               why a press that travels simply lets the strip scroll, and the
               click, if one follows, does what it always did.

               `aria-haspopup="dialog"` because holding the tab opens one, and
               `-webkit-touch-callout` so WebKit's own text callout does not
               race the sheet to the same gesture.

               THE BUTTON IS THE TARGET AND THE SPAN INSIDE IT IS THE CHIP, and
               the split is what reconciles two numbers that disagree. The design
               draws a 31-point chip (`py-[7px]` around a 13px label) inside a
               44-point strip; the brief's rule 3 asks for a 44-point target.
               Both are satisfied by painting nothing on the button — it is a
               transparent `h-full` box in a `h-11` strip — and putting the
               ground, the radius and the design's padding on the span. It is
               the same move <MetaPill> makes for its own tappable case, and the
               same reason: inflating the chip to 44 would make a tab the tallest
               thing on a screen whose subject is the pane below it.

               THE ACTIVE TAB IS `bg-sel`, NOT THE OLD `bg-accent-fill`. The
               accent chip is the app's "one per surface" emphasis and this
               screen already spends it on the back chevron and the "+"; a third
               use on a tab that is merely the one you are looking at made the
               strip the loudest thing above the terminal. The 5-point dot is
               what the design gives it instead — the same mark <StatusChip>
               leads with, so "selected" is stated by a mark rather than by a
               colour that means something else elsewhere. -->
          <button
            type="button"
            role="tab"
            aria-selected={on}
            aria-controls={panelId}
            aria-label={paneTabs.tabName(p)}
            aria-haspopup={p.kind === PANE_KIND_AGENT ? undefined : "dialog"}
            data-pane-selected={on}
            tabindex={on ? 0 : -1}
            class="group flex h-full min-w-11 shrink-0 touch-manipulation items-center
                   justify-center whitespace-nowrap select-none [-webkit-touch-callout:none]"
            onclick={() => {
              // CONSUMED, not merely read. It is cleared by the next press as
              // well (a menu dismissed on the sheet's backdrop never produces
              // the click this is waiting for), but leaving it set once the
              // click HAS arrived left it armed for anything that clicks
              // without a pointer first — Enter or Space on a focused tab,
              // which is how a hardware keyboard selects one. That swallowed
              // the first keystroke after every long press.
              if (suppressClick) {
                suppressClick = false;
                return;
              }
              onselect(p.name);
            }}
            onpointerdown={(e) => holdStart(e, p)}
            onpointermove={holdMove}
            onpointerup={holdEnd}
            onpointercancel={holdEnd}
            onpointerleave={holdEnd}
            oncontextmenu={(e) => e.preventDefault()}
            onkeydown={(e) => onkeydown(e, p)}
          >
            <!-- Text only: the active pane is `text-ink` with a 2px accent
                 underline, the rest `text-faint`. No pill, no dot. -->
            <span
              class="relative flex h-full items-center px-1 text-base font-medium transition-colors
                     {on ? 'text-ink' : 'text-faint'}"
            >
              {paneTabs.displayName(p)}
              {#if on}
                <span
                  class="absolute inset-x-0 bottom-0 h-0.5 bg-accent"
                  aria-hidden="true"
                  data-pane-underline
                ></span>
              {/if}
            </span>
          </button>
        {/each}
      </div>

      <!-- Painted on the ROW, over the strip's own background, and inset past
           the "+" so it marks the strip's edge rather than the row's. The mask
           alone is not enough on a strip that ends on clean tab boundaries: it
           can only dim what it covers, so a clip landing in a gap reads as a
           complete strip with nothing after it — which is the "the list
           silently ends" bug the accessory bar already had to fix once. -->
      {#if edges.left}
        <div
          class="pointer-events-none absolute inset-y-0 left-4 flex w-7 items-center justify-start
                 bg-gradient-to-r from-panel via-panel/80 to-transparent text-sm text-faint"
          aria-hidden="true"
        >
          <Icon name="chevron-left" size={14} />
        </div>
      {/if}
      {#if edges.right}
        <div
          class="pointer-events-none absolute inset-y-0 right-24 flex w-7 items-center justify-end
                 bg-gradient-to-l from-panel via-panel/80 to-transparent text-sm text-faint"
          aria-hidden="true"
        >
          <Icon name="chevron-right" size={14} />
        </div>
      {/if}
    {:else}
      <span class="min-w-0 flex-1"></span>
    {/if}

    <!-- OUTSIDE the scroller, pinned. This is the whole reason the row is split
         in two, and it is the accessory bar's chevron lesson applied: a "+" that
         is the last item of the strip scrolls away exactly when there are enough
         tabs to need it, which is the only moment it matters. The `ml-1` stands
         in for the `gap-1` the strip owns; TouchButton's own `shrink-0` keeps it
         from being squeezed by a full strip.

         A REAL <TouchButton>, unlike the tabs above it. The hand-roll there is
         the documented exception — Button sets `aria-pressed` for `selected`,
         which is invalid on a `role="tab"` — and that reason does not reach this
         button, which is an ordinary action outside the tablist. It was
         hand-rolled anyway and re-stated four things the shared control already
         owns: the 44pt square, the disabled fade, the in-flight spinner, and the
         rule that a working control must not wear the 40% of a dead one.

         `variant="bare"` and an accent foreground, where it used to be
         `secondary` — an outlined box. The design draws the "+" as a bare
         accent glyph, and the outline was there to keep it findable against a
         strip of outlined-looking tabs that no longer exist. The overrides all
         carry the trailing `!` because each of them ties on specificity with one
         of the size map's or the variant's own (CLAUDE.md's Button invariant);
         `w-auto!` undoes `icon`'s square so the glyph gets the design's
         horizontal padding while `min-w-11!` keeps the 44-point floor. -->
    <TouchButton
      icon
      variant="bare"
      loading={paneTabs.creating}
      disabled={!canCreate}
      aria-label={createLabel}
      title={createLabel}
      class="ml-1 h-11! w-11! text-accent! select-none"
      onclick={() => void paneTabs.createShell()}
    >
      <!-- The glyph steps aside while the spinner is up: two marks fighting over
           one 44pt square is worse than one. A glyph, not a "+" character, so it
           sits on the same optical weight as the type button beside it. -->
      {#if !paneTabs.creating}<Icon name="plus" size={20} />{/if}
    </TouchButton>
    {@render trailing?.()}
  </div>
{/if}
