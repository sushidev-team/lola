<script lang="ts">
  import { onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { store, scopedSessions, type SessionInfo } from "$lib/store.svelte";
  import { updates } from "$lib/update.svelte";
  import { nav } from "$lib/nav.svelte";
  import { terms } from "$lib/terms.svelte";
  import { triaged } from "$lib/filters";
  import { reflowGridRows } from "$lib/reflow";
  import Sidebar from "$lib/components/Sidebar.svelte";
  import MainTopBar from "$lib/components/MainTopBar.svelte";
  import PushErrorBanner from "$lib/components/PushErrorBanner.svelte";
  import Toast from "$lib/components/Toast.svelte";
  import Cockpit from "$lib/views/Cockpit.svelte";
  import Home from "$lib/views/Home.svelte";
  import ProjectDetail from "$lib/views/ProjectDetail.svelte";
  import PRPicker from "$lib/views/PRPicker.svelte";
  import TicketPicker from "$lib/views/TicketPicker.svelte";
  import DoctorOverlay from "$lib/views/DoctorOverlay.svelte";
  import SettingsForm from "$lib/views/SettingsForm.svelte";
  import ProjectForm from "$lib/views/ProjectForm.svelte";
  import UpdateOverlay from "$lib/views/UpdateOverlay.svelte";
  import HelpOverlay from "$lib/views/HelpOverlay.svelte";
  import ConfirmDialog from "$lib/components/ConfirmDialog.svelte";
  import SessionMenu from "$lib/components/SessionMenu.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { overlayClose } from "$lib/overlayClose";
  import Setup from "$lib/views/Setup.svelte";

  // The currently-selected cockpit session — the target of every session action
  // the global key handler fires.
  const sel = $derived(store.sessionById(nav.selectedId));

  onMount(() => {
    store.start();
    // Load the version + run the interval-gated startup auto-check so the
    // sidebar's utility row can surface a badge when a release is out.
    void updates.init();
    // The macOS status-bar menu cannot open an overlay itself — it is nav state
    // that lives here — so it asks. See newStatusBarMenu in main.go.
    Events.On("app:open-settings", () => nav.openOverlay("settings"));
    Events.On("app:open-update", () => nav.openOverlay("update"));
  });

  function typing(el: EventTarget | null): boolean {
    const t = el as HTMLElement | null;
    // SELECT is included so a picker/select dropdown swallows global shortcuts
    // the same way a text field does (arrow keys pick options, letters filter).
    return (
      !!t &&
      (t.tagName === "INPUT" ||
        t.tagName === "TEXTAREA" ||
        t.tagName === "SELECT" ||
        t.isContentEditable)
    );
  }

  // The cockpit's visible rows, in the SAME order the table renders (shared with
  // Cockpit.svelte via scopedSessions) and through the SAME triage filter the
  // sidebar applies — otherwise arrow-key movement walks a different list than
  // the one on screen.
  function cockpitRows(): SessionInfo[] {
    return triaged(scopedSessions(store.sessions, nav.scoped, nav.project), nav.triage);
  }

  function moveSel(delta: number) {
    const rows = cockpitRows();
    if (rows.length === 0) return;
    let i = rows.findIndex((r) => r.id === nav.selectedId);
    i = i < 0 ? 0 : Math.min(rows.length - 1, Math.max(0, i + delta));
    nav.select(rows[i].id);
  }

  // Jump to the next/prev session parked on a human (needs_input), wrapping.
  function jumpNeedsInput(dir: number) {
    const rows = cockpitRows();
    const len = rows.length;
    if (len === 0) return;
    let start = rows.findIndex((r) => r.id === nav.selectedId);
    if (start < 0) start = 0;
    for (let n = 1; n <= len; n++) {
      const r = rows[(((start + dir * n) % len) + len) % len];
      if (r.status === "needs_input") {
        nav.select(r.id);
        return;
      }
    }
  }

  // Cockpit session navigation + actions. Returns true when a key was consumed
  // (so the caller can preventDefault the browser's own Enter/arrow/space use).
  function cockpitKey(e: KeyboardEvent): boolean {
    const rows = cockpitRows();
    switch (e.key) {
      case "j":
      case "ArrowDown":
        moveSel(1);
        return true;
      case "k":
      case "ArrowUp":
        moveSel(-1);
        return true;
      case "g":
        if (rows[0]) nav.select(rows[0].id);
        return true;
      case "G":
        if (rows.length) nav.select(rows[rows.length - 1].id);
        return true;
      case "Enter":
        if (sel) nav.toggleFocusTerm(sel.id);
        return true;
      case "V":
        nav.cycleLens();
        return true;
      case "n":
        jumpNeedsInput(1);
        return true;
      case "N":
        jumpNeedsInput(-1);
        return true;
      case "s":
        // Open a worktree shell for the selection — the desktop equivalent of the
        // TUI's "s". Repeatable: each press adds another shell tab. No-op in the
        // grid lens, which has no embed.
        if (sel && nav.lens !== "grid") terms.newShell(sel.id, sel.worktree);
        return true;
      // '<' / '>' switch terminal tabs. Both the shifted glyph and the unshifted
      // ',' / '.' on the same key are bound, so it works with or without Shift and
      // on layouts (German) where '[' / ']' need Option and never arrive.
      case "<":
      case ",":
        if (sel && nav.lens !== "grid") terms.cycleTab(sel.id, -1);
        return true;
      case ">":
      case ".":
        if (sel && nav.lens !== "grid") terms.cycleTab(sel.id, +1);
        return true;
      case "x":
        if (sel) store.askKill(sel.id); // ask first — shared confirm dialog
        return true;
      case "o":
        if (sel?.prUrl) store.openURL(sel.prUrl);
        return true;
      case "c":
        if (sel) store.coderabbit(sel.id);
        return true;
      case "R":
        if (sel && (sel.status === "dead" || sel.status === "session_ended")) store.revive(sel.id);
        return true;
      case "P":
        if (sel) nav.openOverlay("project", sel.project);
        return true;
    }
    return false;
  }

  function onKey(e: KeyboardEvent) {
    // A pending confirmation and an open overlay own Escape even while one of
    // their own fields is focused — the guarded config forms open the confirm
    // from inside a text input — so both are handled BEFORE the typing() bail
    // below, which would otherwise let the field swallow the Escape that should
    // close the overlay. (Modal no longer handles Escape itself, so these are the
    // single place it fires.)

    // An open context menu swallows every key: Escape closes it, nothing else
    // leaks through to a cockpit action underneath. Checked before the confirm
    // dialog only for order's sake — the menu closes before it opens one.
    if (sessionMenu.request) {
      if (e.key === "Escape") {
        sessionMenu.close();
        e.preventDefault();
      }
      return;
    }

    // A pending confirmation swallows every key while open: Escape cancels, Enter
    // is left to the dialog's own focus (safe default for a destructive action).
    // Nothing else leaks through to a cockpit action underneath.
    if (confirm.request) {
      if (e.key === "Escape") {
        confirm.cancel();
        e.preventDefault();
      }
      return;
    }

    // An open overlay swallows keys: Escape closes any of them, '?' also closes
    // the help overlay (so the same key toggles it off). A guarded overlay (an
    // edit form) registers its own close so the dirty check + discard prompt run;
    // ask it first, falling back to a blunt close for the overlays that don't.
    if (nav.overlay) {
      if (e.key === "Escape" || (nav.overlay === "help" && e.key === "?")) {
        if (!overlayClose.request()) nav.closeOverlay();
        e.preventDefault();
      }
      return;
    }

    if (typing(e.target)) return;

    // Let a focused button/link handle its own Enter/Space natively instead of
    // firing a cockpit action on top of the activation (e.g. a lens toggle that
    // still holds focus after a click).
    const active = document.activeElement as HTMLElement | null;
    if ((e.key === "Enter" || e.key === " ") && active && (active.tagName === "BUTTON" || active.tagName === "A")) {
      return;
    }

    // A focused live terminal owns the keyboard (handled inside the view).
    if (nav.focusedTerm) return;

    // '?' opens the keybinding reference from any view.
    if (e.key === "?") {
      nav.openOverlay("help");
      e.preventDefault();
      return;
    }

    // View-independent globals.
    switch (e.key) {
      case "p":
        nav.goHome();
        return;
      case "d":
        nav.openOverlay("doctor");
        return;
      case "S":
        nav.openOverlay("settings");
        return;
      case "b":
        nav.toggleSidebar();
        return;
      // Escape unwinds ONE layer per press, outermost first: leave a view, then
      // drop the triage filter, then leave the project scope. Collapsing them
      // into one press would throw away two decisions on a reflex key.
      case "Escape":
        if (nav.view !== "cockpit") nav.goCockpit(nav.scoped ? nav.project : "");
        else if (nav.triage !== "") nav.setTriage("");
        else if (nav.scoped) nav.goCockpit("");
        return;
    }

    // Cockpit session navigation + actions.
    if (nav.view === "cockpit" && cockpitKey(e)) e.preventDefault();
  }
</script>

<svelte:window on:keydown={onKey} />

{#if store.configChecked && !store.hasConfig}
  <div class="h-full bg-canvas text-ink">
    <div class="drag h-11 shrink-0"></div>
    <Setup />
  </div>
{:else}
<!-- Two columns, as a CSS GRID: grid cells stretch reliably in WKWebView, a
     display:flex child of a flex column does not. Collapse is the first track
     going to 0px — never an {#if}, which would remount the sidebar. -->
<div
  class="grid h-full bg-canvas text-ink"
  style="grid-template-columns:{nav.sidebarOpen ? '248px' : '0px'} minmax(0,1fr)"
>
  <!-- Full height, ALWAYS MOUNTED. Collapse is the grid track going to 0px plus
       overflow-hidden + inert inside — never an {#if}. A new mount boundary in
       this template is what froze the template effect before. -->
  <Sidebar />

  <div class="grid min-h-0 min-w-0" style="grid-template-rows:44px auto minmax(0,1fr)" {@attach reflowGridRows}>
    <MainTopBar />
    <!-- Out-of-date-daemon banner: always mounted (so it reacts), self-hides when
         there is no push error. The WRAPPER is what must always render: the banner
         itself emits NO DOM node when there is no error, and auto-placement then
         shifts <main> up into the `auto` row, leaving `minmax(0,1fr)` empty — the
         whole cockpit collapses to content height and the terminal stops reaching
         the bottom. An always-present empty div pins each child to its own row and
         costs 0px when there is nothing to show. See PushErrorBanner /
         store.pushErrors. -->
    <div class="min-w-0"><PushErrorBanner /></div>

    <!-- The Cockpit stays MOUNTED for every view. Unmounting it tears down its live
         LiveTerminals, and a LiveTerminal unmount freezes THIS component's template
         effect in the production WKWebView — the same failure as the fullscreen
         toggle (see SessionsColumn / CockpitLayout). Once frozen, later nav.view
         changes stop re-rendering, which is why the projects "back" did nothing. So
         the other views render as an opaque overlay ON TOP of the cockpit instead of
         replacing it; nav.view changes now always re-render. -->
    <main class="relative min-h-0 overflow-hidden">
      <Cockpit />
      {#if nav.view !== "cockpit"}
        <div class="absolute inset-0 z-40 overflow-hidden bg-canvas">
          {#if nav.view === "home"}
            <Home />
          {:else if nav.view === "detail"}
            <ProjectDetail />
          {:else if nav.view === "prpicker"}
            <PRPicker />
          {:else if nav.view === "ticketpicker"}
            <TicketPicker />
          {/if}
        </div>
      {/if}
    </main>
  </div>
</div>

<!-- Action results. Outside the grid on purpose: it is `fixed`, and a transient
     message must cost the layout nothing. -->
<Toast />

{#if nav.overlay === "doctor"}
  <DoctorOverlay />
{:else if nav.overlay === "settings"}
  <SettingsForm />
{:else if nav.overlay === "project"}
  <ProjectForm />
{:else if nav.overlay === "update"}
  <UpdateOverlay />
{:else if nav.overlay === "help"}
  <HelpOverlay />
{/if}
<ConfirmDialog />
<SessionMenu />
{/if}
