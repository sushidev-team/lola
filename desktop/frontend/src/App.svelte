<script lang="ts">
  import { onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { store, scopedSessions, type SessionInfo } from "$lib/store.svelte";
  import { updates } from "$lib/update.svelte";
  import { nav } from "$lib/nav.svelte";
  import { terms } from "$lib/terms.svelte";
  import VitalsBar from "$lib/components/VitalsBar.svelte";
  import Footer from "$lib/components/Footer.svelte";
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
  import KillConfirm from "$lib/components/KillConfirm.svelte";
  import Setup from "$lib/views/Setup.svelte";

  // The currently-selected cockpit session, for footer hints + actions.
  const sel = $derived(store.sessionById(nav.selectedId));

  onMount(() => {
    store.start();
    // Load the version + run the interval-gated startup auto-check so the footer
    // can surface a badge when a release is out.
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
  // Cockpit.svelte via scopedSessions) so arrow-key movement matches the list.
  function cockpitRows(): SessionInfo[] {
    return scopedSessions(store.sessions, nav.scoped, nav.project);
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
        if (sel) nav.confirmKill(sel.id); // ask first — KillConfirm dialog
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
    if (typing(e.target)) return;

    // Let a focused button/link handle its own Enter/Space natively instead of
    // firing a cockpit action on top of the activation (e.g. a lens toggle that
    // still holds focus after a click).
    const active = document.activeElement as HTMLElement | null;
    if ((e.key === "Enter" || e.key === " ") && active && (active.tagName === "BUTTON" || active.tagName === "A")) {
      return;
    }

    // The kill-confirmation dialog swallows every key while open: Escape cancels,
    // Enter is left to the focused Cancel button (safe default for a destructive
    // action). Nothing else leaks through to a cockpit action underneath.
    if (nav.killTarget) {
      if (e.key === "Escape") {
        nav.cancelKill();
        e.preventDefault();
      }
      return;
    }

    // An open overlay swallows keys: Escape closes any of them, '?' also closes
    // the help overlay (so the same key toggles it off).
    if (nav.overlay) {
      if (e.key === "Escape" || (nav.overlay === "help" && e.key === "?")) nav.closeOverlay();
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
      case "Escape":
        if (nav.view !== "cockpit") nav.goCockpit(nav.scoped ? nav.project : "");
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
<div class="flex h-full flex-col bg-canvas text-ink">
  <VitalsBar />

  <!-- The Cockpit stays MOUNTED for every view. Unmounting it tears down its live
       LiveTerminals, and a LiveTerminal unmount freezes THIS component's template
       effect in the production WKWebView — the same failure as the fullscreen
       toggle (see SessionsColumn / CockpitLayout). Once frozen, later nav.view
       changes stop re-rendering, which is why the projects "back" did nothing. So
       the other views render as an opaque overlay ON TOP of the cockpit instead of
       replacing it; nav.view changes now always re-render. -->
  <main class="relative min-h-0 flex-1 overflow-hidden">
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

  <Footer>
    {#snippet hints()}
      {#if nav.view === "cockpit"}
        <span class="tabular-nums">↑↓</span> move · <span class="tabular-nums">⏎</span> terminal · s shell · V lens · x kill · p projects · ? help
      {:else}
        esc back · p projects · ? help
      {/if}
    {/snippet}
  </Footer>
</div>

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
{#if nav.killTarget}
  <KillConfirm />
{/if}
{/if}
