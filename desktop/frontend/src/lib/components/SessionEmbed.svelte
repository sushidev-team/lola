<script lang="ts">
  import { flip } from "svelte/animate";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { terms, AGENT } from "$lib/terms.svelte";
  import StatusPill from "./StatusPill.svelte";
  import LivePulse from "./LivePulse.svelte";
  import LiveTerminal from "./LiveTerminal.svelte";
  import Button from "./Button.svelte";
  import MenuItem from "./MenuItem.svelte";

  // `focused` = the expanded full-cockpit view ("minimize" toggle); otherwise the
  // compact detail panel. The two used to differ in terminal font size as well —
  // they no longer do, because the size is half of a matched metric set (see
  // TERM_FONT). Focus changes how much room the terminal gets, not how big its
  // type is, which is also how Ghostty behaves.
  //
  // Takes the session ID (a plain nav value) and resolves the session from the
  // store HERE rather than receiving the resolved object as a prop: the Cockpit
  // view container does not re-render on the async daemon push in the production
  // WKWebView, so a `store.sessionById(...)` computed up there stays frozen at
  // undefined ("select a session" forever). A leaf component's own read reacts.
  // See WKWEBVIEW_REACTIVITY in Cockpit.svelte.
  let { sessionId, focused = false }: { sessionId: string; focused?: boolean } = $props();
  const session = $derived(sessionId ? store.sessionById(sessionId) : undefined);

  // Terminal tabs. Every session shows its agent pane; any number of shell tabs
  // can be opened (each a real tmux session in the worktree — see $lib/terms). The
  // bar hides when there are no shells AND the panel is compact: nothing to switch,
  // so no chrome. Focus (the big/fullscreen view) always shows it, so a shell is
  // reachable there without the "s" shortcut.
  const shells = $derived(session ? terms.shellsFor(session.id) : []);
  const activeTab = $derived(session ? terms.activeTab(session.id) : AGENT);
  const showTabs = $derived(!!session && (shells.length > 0 || focused));

  // The tmux name the LiveTerminal attaches to for the active tab. Keying the
  // terminal on this (below) swaps agent ⇄ shell by re-attaching — the same
  // proven remount the selection change already does, never a live DOM toggle. A
  // shell tab IS its tmux name; the agent tab resolves to the session's pane.
  const activeName = $derived(!session ? "" : activeTab === AGENT ? session.tmuxName : activeTab);
  const activeIsShell = $derived(activeTab !== AGENT);

  // Picking a tab BY HAND focuses the terminal it selects, so typing lands in the
  // agent's input immediately — clicking "agent" while a shell tab was open used
  // to leave the pane unfocused, and the keystrokes went to the global handler
  // instead (an innocent "s" opened another shell).
  //
  // It cannot simply be `autofocus` unconditionally: <LiveTerminal> is keyed on
  // activeName, so it ALSO remounts when the selection moves, and focusing there
  // would trap j/k inside the terminal the moment the list scrolled. So the intent
  // is recorded on click and consumed by the next mount. Reset whenever the
  // session changes, so a stale click can never focus a terminal the user arrived
  // at with the keyboard.
  let pickedTab = $state(false);
  function selectTab(id: string, tab: string) {
    pickedTab = true;
    terms.select(id, tab);
  }
  $effect(() => {
    sessionId; // re-run on selection change
    pickedTab = false;
  });

  // Drag-to-sort for the shell tabs. Pointer events, not HTML5 drag-and-drop:
  // the draggable thing is a <button>, which WebKit refuses to start a native
  // drag from without a `-webkit-user-drag` override, and the native drag image
  // paints badly over the WebGL terminal canvas in the production WKWebView.
  //
  // The pointer is captured only AFTER it has travelled past `dragSlop`, so an
  // ordinary click still reaches the label button — a captured pointerup
  // retargets the following click to the capturing element, which would swallow
  // the tab selection. Same reason the drop is applied live during the move:
  // there is no drag image to place, only the row reordering under the cursor.
  const dragSlop = 5; // px before a press becomes a drag rather than a click
  const flipMs = 140; // tab travel time; also how long a swap is locked (see below)
  let tabEls = $state<(HTMLElement | undefined)[]>([]);
  let dragging = $state(-1); // index being dragged, -1 = none (drives the ghost)
  let dragFrom = -1;
  let dragX = 0;
  let swapLock = false;

  // The tab whose half the pointer is left of — i.e. where the dragged chip
  // would land. Falls through to the last tab when the pointer is past them all.
  function tabIndexAt(x: number, count: number): number {
    for (let i = 0; i < count; i++) {
      const r = tabEls[i]?.getBoundingClientRect();
      if (r && x < r.left + r.width / 2) return i;
    }
    return count - 1;
  }

  function dragStart(i: number, e: PointerEvent) {
    if (e.button !== 0 || tabMenu) return; // left button only, and never behind an open popover
    dragFrom = i;
    dragX = e.clientX;
  }

  function dragMove(id: string, e: PointerEvent) {
    if (dragFrom < 0) return;
    if (dragging < 0) {
      if (Math.abs(e.clientX - dragX) < dragSlop) return;
      dragging = dragFrom;
      (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    }
    // A swap is locked for the length of the FLIP animation. The tabs travel to
    // their new slots on a transform, so `getBoundingClientRect` reports where
    // they are MID-FLIGHT — re-reading it immediately puts the pointer back over
    // the neighbour's old half and swaps the pair straight back, forever. Waiting
    // out the animation reads the settled layout instead.
    if (swapLock) return;
    const to = tabIndexAt(e.clientX, shells.length);
    if (to === dragFrom) return;
    terms.moveTab(id, dragFrom, to);
    dragFrom = to;
    dragging = to;
    swapLock = true;
    setTimeout(() => (swapLock = false), flipMs);
  }

  function dragEnd(e: PointerEvent) {
    const el = e.currentTarget as HTMLElement;
    if (el.hasPointerCapture?.(e.pointerId)) el.releasePointerCapture(e.pointerId);
    dragFrom = -1;
    dragging = -1;
    swapLock = false;
  }

  // The tab's right-click menu (rename / close). Local state rather than a
  // request pushed into sessionmenu.svelte.ts, because that store exists to give
  // MANY session surfaces the same items — this menu acts on a tab, which only
  // exists here. It borrows the same shape: a backdrop that swallows the next
  // click, and a clamp so a tab near the window edge doesn't open off-screen.
  // The popover has two faces: the menu (rename / close) and the rename FORM.
  // Renaming used to happen in the tab itself, and a tab is the wrong size for a
  // text field — the chip is sized to a two-word label, so the name being typed
  // scrolled out of its own box. Reusing the popover gives the field real room
  // without introducing a modal for a two-word edit.
  type TabMenu = { tab: string; x: number; y: number; mode: "menu" | "rename" };
  let tabMenu = $state<TabMenu | null>(null);
  let menuEl = $state<HTMLElement | null>(null);
  let renameEl = $state<HTMLInputElement | null>(null);
  let renameText = $state("");

  // Clamp into the viewport once the popover has a measurable size, so a tab near
  // the window edge doesn't open off-screen. Re-runs when the mode flips, because
  // the rename form is the wider of the two faces.
  $effect(() => {
    const m = tabMenu;
    const node = menuEl;
    if (!m || !node) return;
    const { width, height } = node.getBoundingClientRect();
    node.style.left = `${Math.max(4, Math.min(m.x, window.innerWidth - width - 4))}px`;
    node.style.top = `${Math.max(4, Math.min(m.y, window.innerHeight - height - 4))}px`;
  });

  // Focus + select on open, so typing replaces the current name and Enter alone
  // confirms it. `select()` rather than a bare focus: the field is pre-filled
  // precisely so clearing it is possible, and a caret at the end hides that.
  $effect(() => {
    if (tabMenu?.mode === "rename") renameEl?.select();
  });

  function openTabMenu(tab: string, e: MouseEvent) {
    e.preventDefault(); // suppress WebKit's own menu
    e.stopPropagation(); // ...and the session menu the surface behind would open
    tabMenu = { tab, x: e.clientX, y: e.clientY, mode: "menu" };
  }

  // The rename form is pre-filled with the label the tab currently shows, which
  // is what makes emptying the field the way back to the default "Shell N"
  // (terms.rename reads blank as "forget this name").
  //
  // The global shortcut handler already ignores keystrokes aimed at an <input>
  // (App.svelte's `typing`), so an "s" typed into a tab name cannot open a shell.
  function startRename(id: string, tab: string, at?: MouseEvent) {
    renameText = terms.labelFor(id, tab);
    tabMenu = { tab, x: at ? at.clientX : (tabMenu?.x ?? 0), y: at ? at.clientY : (tabMenu?.y ?? 0), mode: "rename" };
  }

  function commitRename(id: string) {
    const tab = tabMenu?.tab;
    tabMenu = null;
    if (tab) terms.rename(id, tab, renameText);
  }

  // Discover this session's shell tabs from the tmux server on selection, then
  // poll so a shell opened in the TUI (or another window) appears here within a
  // few seconds — the two surfaces attach to the same tmux sessions.
  $effect(() => {
    const id = session?.id;
    if (!id) return;
    terms.refresh(id);
    const poll = setInterval(() => terms.refresh(id), 4000);
    return () => clearInterval(poll);
  });

  const canRevive = $derived(session && (session.status === "dead" || session.status === "session_ended"));
</script>

{#if !session}
  <!-- The glyph is decoration and must read as decoration: at the same text-xl as
       the sentence under it, it parsed as a typo in the first line rather than an
       icon above it. Scaled up and quietened with a colour (`edge`), not opacity —
       faint at 40% lands near 1.3:1 on the light flavor. -->
  <div class="flex h-full flex-col items-center justify-center gap-1 text-faint">
    <div class="hero-glyph text-edge" aria-hidden="true">⌘</div>
    <div class="mt-2 text-xl">select a session</div>
    <div class="text-sm">its live agent terminal shows here</div>
  </div>
{:else}
  <!-- z-10 header keeps the chrome above the WebGL terminal canvas. The terminal
       wrapper stays in normal flow (NO `isolate` on the root, NO `z-0` on the
       wrapper): wrapping the canvas in its own `isolate`+`z-0` stacking context made
       WKWebView paint the wrapper's opaque `bg-panel` over it — a blank terminal. -->
  <div class="flex h-full min-h-0 flex-col">
    <!-- header — z-10 keeps the minimize/focus button above the canvas layer. -->
    <div class="relative z-10 flex flex-wrap items-center gap-2 border-b border-edge/60 px-3 py-2">
      <span class="selectable font-medium text-accent-ink">{session.issue || session.id.slice(0, 8)}</span>
      <span class="selectable truncate text-ink">{session.title}</span>
      <span class="text-edge">·</span>
      <span class="text-sm text-faint">{store.displayNameFor(session.project)}</span>
      <LivePulse agentState={session.agentState} />
      <StatusPill status={session.status} interpreted={session.interpretedState} />
      {#if session.headline}
        <!-- The interpreter's one-line judgement (untrusted, display only). -->
        <span class="truncate text-sm text-info" title={session.waitingOn || undefined}>≈ {session.headline}</span>
      {/if}
      {#if session.currentTool}
        <!-- What the in-flight turn runs right now (PostToolUse hook). -->
        <!-- Mono = identifier. Inline mono inside a 13px row is set one token
             down so JetBrains Mono's lower x-height matches SF's apparent size. -->
        <span class="font-mono text-sm text-faint" title="tool the agent is running">{session.currentTool}</span>
      {/if}
      {#if session.prStale}
        <span class="text-sm text-warn" title="gh has been failing; the delivery state may be old">⚠ PR stale</span>
      {/if}
      {#if session.branch}<span class="selectable font-mono text-sm text-faint">{session.branch}</span>{/if}
      <!-- The session's actions live HERE, not in a band under the terminal. That
           band cost a horizontal rule and a row of height at the bottom of the
           window to hold four buttons that belong to the session named on this
           line — and it stopped the terminal short of the window's edge, which is
           the one thing the region is for. -->
      <span class="ml-auto flex items-center gap-1.5">
        {#if session.prNumber > 0}
          <Button size="xs" onclick={() => store.openURL(session.prUrl)}>Open PR <span aria-hidden="true">↗</span></Button>
        {/if}
        <Button size="xs" onclick={() => store.coderabbit(session.id)}>CodeRabbit</Button>
        <Button size="xs" onclick={() => store.review(session.id)}>Review</Button>
        {#if canRevive}
          <Button variant="accent" size="xs" onclick={() => store.revive(session.id)}>Revive</Button>
        {/if}
        <!-- Opens the shared confirm dialog (App.svelte) rather than an inline
             yes/no, so the 'x' shortcut and this button confirm the same way. -->
        <Button variant="danger" size="xs" onclick={() => store.askKill(session.id)}>Kill</Button>
        <!-- A rule, not a gap: Kill and Focus now share a row, and the one that
             ends a session must not sit a stone's throw from the one that merely
             makes it bigger. -->
        <span class="mx-0.5 h-4 w-px shrink-0 bg-edge/60" aria-hidden="true"></span>
        {#if focused}
          <!-- The chord is spelled out, not just tooltipped: while this terminal
               has focus every other key goes to the agent, so this is the only
               way back to the cockpit without reaching for the mouse. -->
          <span class="text-sm text-faint">⌃Q back</span>
          <Button variant="primary" size="xs" title="exit fullscreen (Ctrl-Q)" onclick={() => nav.toggleFocusTerm(session.id)}>
            <span aria-hidden="true">⤢</span> Minimize
          </Button>
        {:else}
          <Button variant="secondary" size="xs" title="expand to fullscreen" onclick={() => nav.toggleFocusTerm(session.id)}>
            <span aria-hidden="true">⛶</span> Focus
          </Button>
        {/if}
      </span>
    </div>

    <!-- Terminal tabs. Shown when a shell is open or the panel is focused/big:
         the agent tab, one tab per shell (drag to sort, "×" on hover to close),
         and a "+ Shell" parked in the right corner — an *add* control, not one of
         the tabs, so it sits opposite them rather than trailing the row. Collapses
         in the compact, agent-only case so the plain detail panel stays chrome-free. -->
    {#if showTabs}
      <!-- Boxed tabs, joined to the pane below. The strip draws the rule; the
           active tab covers it with a strip of its own background (`-mb-px` over
           `border-b border-panel`) and paints itself in the terminal's colour, so
           the tab and the pane read as one surface and every other tab reads as
           behind it. Chips floating in a padded row could not say that: they
           looked like a toolbar that happened to sit above a terminal. -->
      <!-- The strip is RECESSED half a step below the pane it sits on, which is
           what lets the active tab — painted in the pane's own `base` — read as
           lifted out of it. On a band that is uniformly `base` the active tab had
           nothing to be lighter than. -->
      <div
        class="relative z-10 flex items-stretch border-b border-edge/60 bg-[color-mix(in_srgb,var(--color-panel)_88%,var(--color-canvas))] select-none"
      >
        <div class="flex min-w-0 items-stretch">
          <!-- The agent tab is the same cell as a shell tab minus the × and its
               counterweight, so the strip is one row of boxes, not two shapes. -->
          <button
            type="button"
            aria-pressed={activeTab === AGENT}
            class="h-8 shrink-0 border-r border-edge/40 px-3.5 transition-colors {activeTab === AGENT
              ? '-mb-px border-b border-panel bg-panel font-medium text-ink'
              : 'text-faint hover:bg-sel hover:text-ink'}"
            onclick={() => selectTab(session.id, AGENT)}
          >
            Agent
          </button>
          {#each shells as sh, i (sh)}
            <!-- The tab is ONE chip containing two controls, so the wrapper — not
                 the label button — paints the background and the text colour, and
                 both buttons inside it run `variant="bare"` (no chip, inherits
                 colour). That is the fifth deliberate exception to "every action
                 is a <Button>", and it exists because the × is INSIDE the tab:
                 with the chip on the label, moving the cursor onto the × left the
                 label unhovered and the tab visibly went dark under the pointer.
                 One painter also keeps the old hazard away — a wrapper background
                 plus the button's own would be two same-name utilities racing on
                 Tailwind's source order rather than on the class attribute.
                 Selected and hover are written as whole literal strings for the
                 same reason `hover:` can't just be added next to `bg-accent-fill`:
                 the pseudo-class wins on specificity, so a selected tab would
                 lose its accent chip the moment the cursor arrived.
                 The × keeps its slot in the flow whether or not it is visible,
                 and an empty slot of the same width balances it on the left: the
                 label then sits centred in the chip instead of shoved off it, and
                 nothing reflows when the × fades in under the cursor.
                 role="group" is what the pointer handlers need to satisfy
                 a11y_no_static_element_interactions, and it is also true: the
                 wrapper only carries the drag, and both controls inside it are
                 real buttons that stay reachable from the keyboard. -->
            <!-- animate:flip is what makes a drag legible: without it a tab
                 teleports into its new slot and the only feedback is that the
                 row is suddenly different. The each block is keyed on the tmux
                 name, which is what lets Svelte match a moved tab to its old
                 position and travel it there. -->
            <div
              bind:this={tabEls[i]}
              animate:flip={{ duration: flipMs }}
              role="group"
              class="group flex h-8 shrink-0 items-center gap-1 border-r border-edge/40 px-1.5 transition-colors {activeTab ===
              sh
                ? '-mb-px border-b border-panel bg-panel font-medium text-ink'
                : 'text-faint hover:bg-sel hover:text-ink'}"
              class:opacity-60={dragging === i}
              style="touch-action: none"
              onpointerdown={(e) => dragStart(i, e)}
              onpointermove={(e) => dragMove(session.id, e)}
              onpointerup={dragEnd}
              onpointercancel={dragEnd}
              oncontextmenu={(e) => openTabMenu(sh, e)}
            >
              <!-- The × slot's counterweight. Purely optical, hence aria-hidden. -->
              <span class="h-5 w-5 shrink-0" aria-hidden="true"></span>
              <!-- aria-pressed by hand: the chip that normally carries it moved
                   to the wrapper, but the label is still the control toggled. -->
              <Button
                variant="bare"
                size="xs"
                class="cursor-grab px-0.5!"
                aria-pressed={activeTab === sh}
                ondblclick={(e) => startRename(session.id, sh, e)}
                onclick={() => selectTab(session.id, sh)}
              >
                {terms.labelFor(session.id, sh)}
              </Button>
              <!-- Colour is the whole affordance here: no chip of its own (it is
                   already sitting on one), just the glyph fading in with the tab
                   and going red under the cursor. -->
              <Button
                variant="bare"
                size="xs"
                icon
                class="h-5! w-5! opacity-0 transition-[opacity,color] group-hover:opacity-100 hover:text-bad focus-visible:opacity-100"
                title={terms.isReviewTab(sh) ? "close the review pane" : "close shell"}
                aria-label={terms.isReviewTab(sh) ? "close review" : "close shell"}
                onclick={() => terms.closeShell(session.id, sh)}>×</Button
              >
            </div>
          {/each}
        </div>
        <!-- Still a Button, not a tab cell: it opens a shell, it never *is* one,
             and the boxed cells to its left are what the distinction rides on. -->
        <span class="ml-auto flex shrink-0 items-center pr-3 pl-2">
          <Button
            size="xs"
            class="px-2.5!"
            title="open a shell in the worktree"
            onclick={() => terms.newShell(session.id, session.worktree)}
          >
            <span aria-hidden="true">+</span> Shell
          </Button>
        </span>
      </div>

      {#if tabMenu}
        <!-- Backdrop: any click (or another right-click) outside dismisses the
             menu without reaching the surface underneath — the terminal below
             would otherwise take the click and steal the keyboard. -->
        <div
          class="fixed inset-0 z-40"
          role="presentation"
          onclick={() => (tabMenu = null)}
          oncontextmenu={(e) => {
            e.preventDefault();
            tabMenu = null;
          }}
        ></div>
        {#if tabMenu.mode === "rename"}
          <!-- The rename face. A form, so Enter submits without a keydown of our
               own; Escape closes it. Cancel/Save rather than commit-on-blur —
               the field is pre-filled, and a stray click elsewhere silently
               renaming the tab is worse than one extra click to confirm. -->
          <form
            bind:this={menuEl}
            class="fixed z-50 w-64 rounded-lg border border-edge bg-panel p-3 shadow-xl"
            style="left:{tabMenu.x}px;top:{tabMenu.y}px"
            onsubmit={(e) => {
              e.preventDefault();
              commitRename(session.id);
            }}
          >
            <label class="label mb-1.5 block text-faint" for="tab-rename">Tab name</label>
            <!-- The accent border IS the focus signal here, so the global
                 :focus-visible ring is suppressed: the field is focused the moment
                 the popover opens, and a 2px halo around a 256px panel read as an
                 alarm rather than as focus. It needs the trailing `!` — app.css's
                 `:focus-visible` rule is unlayered, and an unlayered rule beats any
                 Tailwind utility on layer order, however specific the utility. -->
            <input
              id="tab-rename"
              bind:this={renameEl}
              bind:value={renameText}
              maxlength="40"
              spellcheck="false"
              onkeydown={(e) => {
                if (e.key !== "Escape") return;
                e.preventDefault();
                e.stopPropagation(); // "abandon the edit", never "leave fullscreen"
                tabMenu = null;
              }}
              class="h-8 w-full rounded-md border border-edge bg-canvas px-2.5 text-ink focus:border-accent focus-visible:outline-none!"
            />
            <div class="mt-1.5 text-sm text-faint">Empty resets it to the default name.</div>
            <div class="mt-3 flex justify-end gap-2">
              <Button size="xs" onclick={() => (tabMenu = null)}>Cancel</Button>
              <Button variant="primary" size="xs" type="submit">Save</Button>
            </div>
          </form>
        {:else}
          <div
            bind:this={menuEl}
            class="fixed z-50 min-w-[10rem] rounded-lg border border-edge bg-panel p-1 shadow-xl"
            style="left:{tabMenu.x}px;top:{tabMenu.y}px"
            role="menu"
          >
            <div class="label truncate px-2 pt-1 pb-1.5 text-faint">{terms.labelFor(session.id, tabMenu.tab)}</div>
            <MenuItem icon="✎" onclick={() => startRename(session.id, tabMenu?.tab ?? "")}>Rename…</MenuItem>
            <div class="my-1 h-px bg-edge/60"></div>
            <MenuItem
              variant="danger"
              icon="×"
              onclick={() => {
                const tab = tabMenu?.tab;
                tabMenu = null;
                if (tab) terms.closeShell(session.id, tab);
              }}
            >
              {terms.isReviewTab(tabMenu.tab) ? "Close review" : "Close shell"}
            </MenuItem>
          </div>
        {/if}
      {/if}
    {/if}

    <!-- Live terminal (agent pane or worktree shell). p-4 = 16px, matching
         Ghostty's window-padding-x/y; p-2 gave it half the breathing room and
         contributed to the cramped read. bg-panel is the flavor's `base` — the
         exact colour LiveTerminal paints as its terminal background — so the
         padding gutter is seamless with the terminal and the OSC-11 background an
         agent reads is genuinely the colour surrounding it. There is no fontSize
         prop any more: the old `focused ? 14 : 12` broke the cell arithmetic
         (see TERM_FONT). -->
    <div class="min-h-0 flex-1 bg-panel p-4">
      {#if activeName}
        <!-- Keyed on the active tab's tmux name, which already carries the session
             identity: switching agent ⇄ shell (or moving the selection) re-attaches
             by remounting. Keyed on the NAME, not a focus flag — with one cell size
             for every terminal, focus changes nothing here, and rebuilding on it
             would drop the scrollback every time the panel expanded. -->
        {#key activeName}
          <LiveTerminal
            name={activeName}
            webgl
            interactive
            autofocus={pickedTab || activeIsShell || focused}
            onExit={activeIsShell ? () => terms.shellExited(session.id, activeName) : undefined}
            onEscapeFocus={focused ? () => nav.toggleFocusTerm(session.id) : undefined}
          />
        {/key}
      {:else}
        <div class="flex h-full items-center justify-center text-faint">no tmux session (dead)</div>
      {/if}
    </div>
  </div>
{/if}
