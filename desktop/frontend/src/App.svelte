<script lang="ts">
  import NewSession from "$lib/components/NewSession.svelte";
  import NewSSHSession from "$lib/components/ssh/NewSSHSession.svelte";
  import ConnectSSHHost from "$lib/components/ssh/ConnectSSHHost.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { talk } from "$lib/talk.svelte";
  import { changesOffered, conversationOffered } from "$lib/sessionview";
  import RenameSession from "$lib/components/RenameSession.svelte";
  import CommandPalette from "$lib/components/CommandPalette.svelte";
  import ActivityFeed from "$lib/components/ActivityFeed.svelte";
  import Modal from "$lib/components/Modal.svelte";
  import { toggleGitWorkspace } from "$lib/workspacenav";
  import { openShell, newSessionHere } from "$lib/projectActions";
  import { onMount } from "svelte";
  import { Events, Window } from "@wailsio/runtime";
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { updates } from "$lib/update.svelte";
  import { nav } from "$lib/nav.svelte";
  import { terms, DIFF, CHECKPOINTS } from "$lib/terms.svelte";
  import { visibleRoster } from "$lib/roster";
  import { sidebarWidth, sidebarLimit } from "$lib/sidebarwidth";
  let windowWidth = $state(1280);
  const sideWidth = $derived(sidebarWidth(nav.sidebarWidth, windowWidth));
  import { isChord } from "$lib/keys";
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
  import TeamView from "$lib/views/TeamView.svelte";
  import DoctorOverlay from "$lib/views/DoctorOverlay.svelte";
  import SettingsForm from "$lib/views/SettingsForm.svelte";
  import ProjectForm from "$lib/views/ProjectForm.svelte";
  import UpdateOverlay from "$lib/views/UpdateOverlay.svelte";
  import HelpOverlay from "$lib/views/HelpOverlay.svelte";
  import BestOfOverlay from "$lib/views/BestOfOverlay.svelte";
  import ConfirmDialog from "$lib/components/ConfirmDialog.svelte";
  import SandboxLoginDialog from "$lib/components/SandboxLoginDialog.svelte";
  import SessionMenu from "$lib/components/SessionMenu.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { overlayClose } from "$lib/overlayClose";
  import Setup from "$lib/views/Setup.svelte";
  import ReviewWorkspace from "$lib/components/session/ReviewWorkspace.svelte";
  import { reviewWorkspace } from "$lib/reviewworkspace.svelte";
  import DesktopContext from "$lib/components/DesktopContext.svelte";

  // The target of every session action: the session you are looking at, and the selection only
  // when none is open. The selection can differ from the open session (ssh.open, a fork), and ⌘.
  // must never stop a session you are not looking at.
  const sel = $derived(store.sessionById(nav.focusedTerm || nav.selectedId));

  function onTitleBarDoubleClick(event: MouseEvent) {
    if (event.button !== 0 || !(event.target instanceof Element)) return;
    // Use the same inherited property as Wails so controls and their icons opt out.
    if (getComputedStyle(event.target).getPropertyValue("--wails-draggable").trim() !== "drag") return;
    void Window.ToggleMaximise().catch((err) => store.setFlash(String(err), "warn"));
  }

  onMount(() => {
    store.start();
    // Load the version + start launch and periodic update checks so the
    // sidebar's utility row can surface a badge when a release is out.
    void updates.init();
    // The macOS status-bar menu cannot open an overlay itself — it is nav state
    // that lives here — so it asks. See newStatusBarMenu in main.go.
    Events.On("app:open-settings", () => nav.openOverlay("settings"));
    Events.On("app:open-update", () => nav.openOverlay("update"));
    // The Session menu's items + their ⌘ accelerators (see installAppMenu). They
    // name an action; the target is the selection, which only the frontend knows.
    Events.On("app:session-action", (e: { data: string }) => sessionAction(e.data));
    return () => updates.stop();
  });

  // Apply a Session-menu action to the current selection. AppKit delivers a menu
  // accelerator from ANY focus — including inside a live terminal, which is the
  // point — so the "an overlay owns the keyboard" rule that onKey enforces has to
  // be repeated here, or ⌘⇧K would stack a second kill confirmation on top of an
  // open dialog.
  function sessionAction(action: string) {
    // Go To has no unsaved work: the Git shortcut can dismiss it and continue.
    if (action === "toggle-git" && nav.overlay === "commands" && !confirm.request && !sessionMenu.request) {
      nav.closeOverlay();
      toggleGitWorkspace();
      return;
    }
    if (nav.overlay || confirm.request || sessionMenu.request || document.querySelector('[aria-modal="true"]')) return;
    if (action === "toggle-git") { toggleGitWorkspace(); return; }
    if (action === "toggle-sidebar") { nav.toggleSidebar(); return; }
    if (action === "focus-sessions") { nav.focusSessions(); return; }
    if (action === "shortcuts") { nav.openOverlay("help"); return; }
    if (action === "previous") { ssh.nextWaiting(-1); return; }
    if (action === "close-tab") {
      if (nav.view === "cockpit" && nav.focusedTerm) void terms.closeActiveTab(nav.focusedTerm);
      return;
    }
    if (action === "find-project-history") { if (nav.view === "detail" && nav.projectTab === "git") nav.gitSearchRequest++; return; }
    if (action === "commands") { nav.openOverlay("commands"); return; }
    if (action === "new-session") { newSessionHere(); return; }
    if (action === "temporary-shell") { void openShell(); return; }
    if (action.startsWith("ssh-")) { ssh.menuAction(action, sel); return; }
    if (action === "next") { ssh.nextWaiting(1); return; }
    const s = sel;
    if (!s) {
      store.setFlash("no session selected", "warn");
      return;
    }
    switch (action) {
      case "review":
        store.review(s.id); // the CONFIGURED provider, not coderabbit specifically
        break;
      case "open-pr":
        if (s.prUrl) store.openURL(s.prUrl);
        else store.setFlash("session has no PR yet", "warn");
        break;
      case "new-shell":
        // A tab only exists inside the session, so this opens it first.
        nav.openSession(s.id);
        terms.newShell(s.id, s.worktree);
        break;
      case "stop":
        // Never a session nobody can see. Home and the project views cover the selection, and a
        // stray chord there must not end someone's turn.
        if (nav.view !== "cockpit") {
          store.setFlash("Open a session to stop it.", "warn");
          break;
        }
        // No connection needed, and the daemon re-reads the pane behind it. Its refusal is a
        // sentence worth showing.
        talk.stop(s.id).catch((err) => store.setFlash(String(err), "warn"));
        break;
      case "changes":
        ssh.toggleChanges(s);
        break;
      case "kill":
        store.askKill(s.id); // ask first — shared confirm dialog
        break;
    }
  }

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
    return visibleRoster(store.rosterSessions, store.projects.map(p => p.name), n => store.displayNameFor(n), { query: nav.sidebarQuery, triage: nav.triage, scoped: nav.scoped, project: nav.project });
  }

  function moveSel(delta: number) {
    const rows = cockpitRows();
    if (rows.length === 0) return;
    let i = rows.findIndex((r) => r.id === nav.selectedId);
    i = i < 0 ? 0 : Math.min(rows.length - 1, Math.max(0, i + delta));
    nav.openSession(rows[i].id);
  }

  // Cockpit session navigation + actions. Returns true when a key was consumed
  // (so the caller can preventDefault the browser's own Enter/arrow/space use).
  function cockpitKey(e: KeyboardEvent): boolean {
    const rows = cockpitRows();
    switch (e.key) {
      case "ArrowDown": moveSel(1); return true;
      case "ArrowUp": moveSel(-1); return true;
      case "Home": if (rows[0]) nav.openSession(rows[0].id); return true;
      case "End": if (rows.length) nav.openSession(rows[rows.length - 1].id); return true;
      case "Enter": if (sel) nav.openSession(sel.id); return true;
    }
    return false;
  }

  // Which view of the open session the keyboard is looking at.
  const SESSION_RING: Record<string, 1 | -1> = { "<": -1, ",": -1, ">": 1, ".": 1 };

  // Contextual session navigation. Destructive/session actions use native menu
  // chords or explicit controls; timeline row keys stay with the focused timeline.
  function sessionKey(e: KeyboardEvent): boolean {
    const s = sel;
    if (e.key === "?") {
      nav.openOverlay("help");
      return true;
    }
    if (e.key === "Escape") return escapeLadder(s);
    if (!s) return false;
    if (ssh.bareKey(e.key, s)) return true;
    const v = ssh.view(s.id);
    const conversation = conversationOffered(s);
    switch (e.key) {
      case "n":
        ssh.nextWaiting(1);
        return true;
      case "N":
        ssh.nextWaiting(-1);
        return true;
      case "f":
        if (changesOffered(s)) terms.toggleDiff(s.id);
        return true;
      case "F":
        ssh.cycleFilter(s);
        return true;
      case "i":
        ssh.focusComposer(s);
        return true;
      case "t":
        if (conversation) {
          ssh.open(s.id);
          v.holdArrival = true;
          v.streamFocus++;
        }
        return true;
      case "/":
        if (conversation) {
          ssh.open(s.id);
          v.holdArrival = true;
          v.findFocus++;
        }
        return true;
      case "w":
        if (conversation && v.knownWidgets.length) ssh.dockKey(s);
        return true;
    }
    const ring = SESSION_RING[e.key];
    if (ring) {
      ssh.ringStrip(s, ring);
      return true;
    }
    return false;
  }

  // One step per press, and Esc never reaches the agent from here: a terminal holding the keyboard
  // owns Esc before this runs, and Ctrl-Q is its way out. Changes and an unfocused terminal step
  // back a view. The conversation steps back to Overview once nothing in it holds focus: the
  // composer and the timeline each give up focus first, on their own press.
  function escapeLadder(s: SessionInfo | undefined): boolean {
    if (!s) {
      nav.focusSessions();
      return true;
    }
    const tab = terms.activeTab(s.id);
    if (tab === DIFF || tab === CHECKPOINTS) {
      terms.toggleDiff(s.id);
      return true;
    }
    if (ssh.onTerminal(s) && conversationOffered(s)) {
      ssh.leaveTerminal(s);
      return true;
    }
    nav.focusSessions();
    return true;
  }

  function onKey(e: KeyboardEvent) {
    // A modifier chord is the platform's, never ours: every shortcut below is a
    // BARE key, so without this bail Cmd-C ran "coderabbit review" instead of
    // Copy and Cmd-X opened the kill confirmation instead of Cut (also Cmd-S,
    // Cmd-O, Cmd-N, Cmd-G, Cmd-, …). Shift is not a modifier here — 'V', 'G',
    // contextual uppercase keys and '?' are still supported. See lib/keys.ts.
    //
    // It sits ABOVE the menu/confirm/overlay blocks on purpose: those swallow
    // keys only to keep them off the cockpit actions, which this return already
    // guarantees, and Cmd-C must keep copying while a dialog is open.
    if (isChord(e)) return;

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

    if (typing(e.target)) {
      // The one Esc a field gives up here: the composer's. It keeps its draft. Everything else
      // that types keeps its Esc, a terminal's textarea above all, since that key is the agent's.
      if (e.key === "Escape" && (e.target as HTMLElement).closest?.("[data-action-zone]")) {
        (e.target as HTMLElement).blur();
        e.preventDefault();
      }
      return;
    }

    // Let a focused button/link handle its own Enter/Space natively instead of
    // firing a cockpit action on top of the activation (e.g. a lens toggle that
    // still holds focus after a click).
    const active = document.activeElement as HTMLElement | null;
    if ((e.key === "Enter" || e.key === " ") && active && (active.tagName === "BUTTON" || active.tagName === "A")) {
      return;
    }

    // Session navigation belongs to the sidebar; the conversation keeps its own row keys.
    const inSidebar = !!(e.target as HTMLElement)?.closest?.('[data-sidebar-sessions]');
    if (inSidebar && nav.view === 'cockpit') {
      if (e.key === 'Escape') {
        if (nav.sidebarQuery) nav.sidebarQuery = '';
        else if (nav.triage) nav.setTriage('');
        else if (nav.scoped) nav.goCockpit('');
        e.preventDefault();
        return;
      } else if (cockpitKey(e)) { e.preventDefault(); return; }
    }

    // Two depths, two key maps. Inside a session `typing()` above is the only guard, so a terminal
    // holding the keyboard keeps every key and this never sees them. Falling through to the globals
    // and cockpitKey instead would make `j` move the list behind the session, Enter close it, and
    // `x` ask to kill.
    if (!inSidebar && nav.focusedTerm && nav.view === "cockpit") {
      if (sessionKey(e)) e.preventDefault();
      return;
    }

    // `A` and `h` and `W` open a session from Overview. The session's own keys, `i t F /`, are not
    // here: they would bump focus counters for a composer that is not mounted.
    if (nav.view === "cockpit" && ssh.bareKey(e.key, sel)) {
      e.preventDefault();
      return;
    }

    // '?' opens the keybinding reference from any view.
    if (e.key === "?") {
      nav.openOverlay("help");
      e.preventDefault();
      return;
    }

    // View-independent globals.
    switch (e.key) {
      case "T":
        nav.goTeam();
        return;
      // Escape unwinds ONE layer per press, outermost first: leave a view, then
      // drop the triage filter, then leave the project scope. Collapsing them
      // into one press would throw away two decisions on a reflex key.
      case "Escape":
        if (nav.inGit) nav.returnFromGit();
        else if (nav.view !== "cockpit") nav.goCockpit(nav.scoped ? nav.project : "");
        else if (nav.triage !== "") nav.setTriage("");
        else if (nav.scoped) nav.goCockpit("");
        return;
    }

    // Cockpit session navigation + actions.
    if (nav.view === "cockpit" && cockpitKey(e)) e.preventDefault();
  }
</script>

<svelte:window on:keydown={onKey} on:dblclick={onTitleBarDoubleClick} bind:innerWidth={windowWidth} />
<DesktopContext />

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
  style="grid-template-columns:{nav.sidebarOpen ? `${sideWidth}px` : '0px'} minmax(0,1fr)"
>
  <!-- Full height, ALWAYS MOUNTED. Collapse is the grid track going to 0px plus
       overflow-hidden + inert inside — never an {#if}. A new mount boundary in
       this template is what froze the template effect before. -->
  <Sidebar width={sideWidth} maxWidth={sidebarLimit(windowWidth)} />

  <div class="grid min-h-0 min-w-0" style="grid-template-rows:{nav.view === 'detail' && store.projectByName(nav.project) ? '0px' : '44px'} auto minmax(0,1fr)" {@attach reflowGridRows}>
    <!-- Project workspaces own their single window toolbar. Keep the shared
         bar mounted for the session view, but remove its duplicate chrome. -->
    <div class="min-h-0" class:overflow-hidden={nav.view === 'detail' && !!store.projectByName(nav.project)} inert={nav.view === 'detail' && !!store.projectByName(nav.project)} aria-hidden={nav.view === 'detail' && !!store.projectByName(nav.project)}><MainTopBar /></div>
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
          {:else if nav.view === "team"}
            <TeamView />
          {/if}
        </div>
      {/if}
    </main>
  </div>
</div>

<!-- Action results. Outside the grid on purpose: it is `fixed`, and a transient
     message must cost the layout nothing. -->
<Toast />

{#if nav.overlay === "pr-review"}
  <ReviewWorkspace sessionId={reviewWorkspace.session} />
{:else if nav.overlay === "newsession"}
  <NewSession />
{:else if nav.overlay === "sshsession"}
  <NewSSHSession />
{:else if nav.overlay === "sshconnect"}
  <ConnectSSHHost />
{:else if nav.overlay === "commands"}
  <CommandPalette />
{:else if nav.overlay === "rename-session"}
  <RenameSession />
{:else if nav.overlay === "activity"}
  <Modal title="Activity" onClose={() => nav.closeOverlay()} width="680px"><ActivityFeed limit={200} /></Modal>
{:else if nav.overlay === "doctor"}
  <DoctorOverlay />
{:else if nav.overlay === "settings"}
  <SettingsForm />
{:else if nav.overlay === "project"}
  <ProjectForm />
{:else if nav.overlay === "update"}
  <UpdateOverlay />
{:else if nav.overlay === "help"}
  <HelpOverlay />
{:else if nav.overlay === "bestof"}
  <BestOfOverlay />
{/if}
<ConfirmDialog />
<SandboxLoginDialog />
<SessionMenu />
{/if}
