<script lang="ts">
  import { openProject } from "$lib/workspacenav";
  import { onMount, onDestroy, tick } from "svelte";
  import { toggleGitWorkspace } from "$lib/workspacenav";
  import { nav } from "$lib/nav.svelte";
  import { store } from "$lib/store.svelte";
  import { terms } from "$lib/terms.svelte";
  import { projectActions, openSession, openShell, newSessionHere } from "$lib/projectActions";
  import { ssh } from "$lib/ssh.svelte";
  import { hub } from "$lib/hub.svelte";
  import { hubReason } from "$lib/hubboard";
  import { overlayClose } from "$lib/overlayClose";
  import { matchesSearch, searchMatch } from "$lib/search";
  import Icon from "./Icon.svelte";
  import type { IconName } from "$lib/icons";
  import SearchHighlight from "./SearchHighlight.svelte";

  // Search first, act second: sessions and projects are the direct entries and come first; the
  // few actions that matter are shown with them, and everything else is one query away. The
  // selected entry's own actions live in the options panel (⌘K), as in a launcher.
  type Kind = "session" | "project" | "action";
  type Act = { id: string; label: string; shortcut?: string; enabled: boolean; run: () => void };
  type Result = { id: string; kind: Kind; icon: IconName; label: string; detail: string; project?: string; session?: string; primary?: boolean; run: () => void };
  const KIND_LABEL: Record<Kind, string> = { session: "Session", project: "Project", action: "Action" };
  const HEADING: Record<Kind, string> = { session: "Sessions", project: "Projects", action: "Actions" };
  const LIMIT: Record<Kind, number> = { session: 8, project: 8, action: 8 };
  // Nothing typed: only the common actions. Sessions and projects appear when the query matches them.
  const IDLE_LIMIT: Record<Kind, number> = { session: 0, project: 0, action: 8 };

  let query = $state("");
  let index = $state(0);
  let input: HTMLInputElement;
  let optionsOpen = $state(false);
  let optionsQuery = $state("");
  let optionsIndex = $state(0);
  let optionsInput = $state<HTMLInputElement>();

  const current = $derived(store.sessionById(nav.focusedTerm || nav.selectedId));
  const actions = $derived.by<Result[]>(() => {
    const a = (id: string, icon: IconName, label: string, detail: string, run: () => void, primary = false): Result => ({ id, kind: "action", icon, label, detail, primary, run });
    return [
      a("new", "plus", "New session…", "C", () => newSessionHere(), true),
      a("git", "git-branch", nav.inGit ? "Back from Git" : "Open Git workspace", "⌘⇧G", toggleGitWorkspace, true),
      a("temporary", "square-terminal", "New temporary shell", "default directory", () => { void openShell(); }, true),
      a("settings", "settings", "Settings", "", () => nav.openOverlay("settings"), true),
      a("team-open", "users", "Team: Open", hub.needsCount ? `${hub.needsCount} ${hub.needsCount === 1 ? "chore needs" : "chores need"} you` : "the team hub's chores and Macs", () => nav.goTeam(), true),
      a("sidebar", "panel-right", "Toggle sidebar", "⌘B", () => nav.toggleSidebar()),
      a("focus-sessions", "list", "Focus sessions", "⌘⇧E", () => nav.focusSessions()),
      a("shortcuts", "slash", "Keyboard shortcuts", "⌘/", () => nav.openOverlay("help")),
      a("doctor", "shield-check", "Doctor", "", () => nav.openOverlay("doctor")),
      a("ssh", "server", "New SSH session…", "an agent and a terminal on a host", () => ssh.newSession()),
      ...(current && current.location !== "ssh" ? [a("ssh-connect", "server", "Connect this session to a host…", current.title || current.id.slice(0, 8), () => { nav.openSession(current.id); ssh.connectHost(current.id); })] : []),
      ...(current && current.location !== "ssh" && current.prNumber > 0 ? [a("coderabbit-poll", "git-pull-request", "Poll CodeRabbit reviews", current.issue || current.title || current.id, () => { void store.coderabbit(current.id); })] : []),
      ...ssh.hosts.map((h) => a(`ssh:${h.name}`, "server", `SSH to ${h.label || h.name}`, h.tier, () => ssh.newSession(h.name))),
      a("activity", "activity", "View all activity", "", () => nav.openOverlay("activity")),
      ...(hub.joined ? hub.needs : []).slice(0, 5).map((n) => a(`team-needs:${n.id}`, "users", `Team: ${n.title}`, `Needs you · ${n.why}`, () => { hub.focus(n.id); nav.goTeam(); })),
      a("team-sync", "refresh-cw", "Team: Sync now", "ask the team hub for news", () => { void syncTeam(); }),
    ];
  });
  const sessionRows = $derived(
    [...store.sessions]
      .sort((a, b) => String(b.firstSeen || "").localeCompare(String(a.firstSeen || "")))
      .map<Result>((s) => ({ id: `s:${s.id}`, kind: "session", icon: "terminal", label: s.title || s.branch || s.id, detail: `${s.project ? store.displayNameFor(s.project) : "Temporary"} · ${s.branch || s.worktree}`, session: s.id, run: () => openSession(s.id) })),
  );
  const projectRows = $derived(
    store.projects.map<Result>((p) => ({ id: `p:${p.name}`, kind: "project", icon: p.kind === "stack" ? "layers" : "folder", label: store.displayNameFor(p.name), detail: p.path || p.repo, project: p.name, run: () => openProject(p.name) })),
  );
  const results = $derived.by<Result[]>(() => {
    const searching = query.trim() !== "";
    const keep = (rows: Result[], kind: Kind) => {
      const hits = searching ? rows.filter((r) => matchesSearch(query, r.label, r.detail)) : rows.filter((r) => kind !== "action" || r.primary);
      return hits.slice(0, searching ? LIMIT[kind] : IDLE_LIMIT[kind]);
    };
    return [...keep(sessionRows, "session"), ...keep(projectRows, "project"), ...keep(actions, "action")];
  });
  const active = $derived(Math.min(index, Math.max(0, results.length - 1)));
  const selected = $derived(results[active]);
  const project = $derived(selected?.project ? store.projectByName(selected.project) : undefined);

  // What ⌘K lists for the selected entry. The first is what Enter does.
  const options = $derived.by<Act[]>(() => {
    if (!selected) return [];
    if (project) return projectActions(project);
    if (selected.session) {
      const s = store.sessionById(selected.session);
      if (!s) return [];
      return [
        { id: "open", label: "Open session", shortcut: "↵", enabled: true, run: selected.run },
        { id: "shell", label: "New shell in session", enabled: !!s.worktree, run: () => { nav.openSession(s.id); terms.newShell(s.id, s.worktree); } },
        { id: "pr", label: "Open pull request", enabled: !!s.prUrl, run: () => { if (s.prUrl) store.openURL(s.prUrl); } },
        { id: "kill", label: "Stop session…", enabled: true, run: () => store.askKill(s.id) },
      ];
    }
    return [{ id: "run", label: selected.label, shortcut: "↵", enabled: true, run: selected.run }];
  });
  const shownOptions = $derived(options.filter((o) => matchesSearch(optionsQuery, o.label)));

  // Focus goes back to whatever opened the palette, unless closing it opened another overlay.
  let invoker: HTMLElement | null = null;
  let dialog: HTMLDivElement;
  onMount(() => { invoker = document.activeElement as HTMLElement | null; void tick().then(() => input?.focus()); void ssh.loadHosts(); overlayClose.register(close); });
  onDestroy(() => { overlayClose.unregister(close); if (!nav.overlay) invoker?.focus?.(); });
  // aria-modal does not contain Tab: wrap it between the first and last control.
  function trap(e: KeyboardEvent) {
    if (e.key !== "Tab") return;
    const els = Array.from(dialog.querySelectorAll<HTMLElement>('input,button:not([disabled]):not([tabindex="-1"])'));
    if (els.length === 0) return;
    const first = els[0], last = els[els.length - 1];
    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  }
  // The menu's ⌘K reaches the page through App (a native accelerator never types into the field).
  let seenToggle = nav.commandOptions;
  $effect(() => { if (nav.commandOptions !== seenToggle) { seenToggle = nav.commandOptions; toggleOptions(); } });

  // The daemon answers in its own words, through the one mapper (hubReason);
  // a hub that is not joined says so.
  async function syncTeam() {
    const r = await hub.sync();
    store.setFlash(r.ok ? "Team hub sync queued" : hubReason(r.error), r.ok ? "good" : "warn");
  }
  function run(action: () => void) { nav.closeOverlay(); action(); }
  /** Escape peels the options panel first, then the palette. */
  function close() { if (optionsOpen) closeOptions(); else nav.closeOverlay(); }
  async function toggleOptions() {
    if (optionsOpen) { closeOptions(); return; }
    if (!selected) return;
    optionsQuery = ""; optionsIndex = 0; optionsOpen = true;
    await tick();
    optionsInput?.focus();
  }
  function closeOptions() { optionsOpen = false; void tick().then(() => input?.focus()); }
  function scroll(id: string) { void tick().then(() => document.getElementById(id)?.scrollIntoView?.({ block: "nearest" })); }
  function key(e: KeyboardEvent) {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault(); index = Math.max(0, Math.min(results.length - 1, active + (e.key === "ArrowDown" ? 1 : -1)));
      scroll(`command-${index}`);
    } else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      e.preventDefault(); void toggleOptions();
    } else if ((e.metaKey || e.ctrlKey) && (e.key === "Enter" || e.key.toLowerCase() === "o")) {
      e.preventDefault();
      const action = (project ? projectActions(project) : []).find((a) => a.id === (e.key === "Enter" ? "new" : "shell"));
      if (action?.enabled) run(action.run);
    } else if (e.key === "Enter" && selected) { e.preventDefault(); run(selected.run); }
  }
  function optionsKey(e: KeyboardEvent) {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault(); optionsIndex = Math.max(0, Math.min(shownOptions.length - 1, optionsIndex + (e.key === "ArrowDown" ? 1 : -1)));
    } else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      e.preventDefault(); closeOptions();
    } else if (e.key === "Enter") {
      e.preventDefault(); const o = shownOptions[optionsIndex];
      if (o?.enabled) run(o.run);
    }
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<div class="fixed inset-0 z-40 flex items-start justify-center bg-black/40 px-4 pt-[12vh] backdrop-blur-[2px]" role="presentation" onclick={(e) => e.target === e.currentTarget && nav.closeOverlay()}>
  <div bind:this={dialog} onkeydown={trap} class="relative flex max-h-[70vh] w-full max-w-[640px] flex-col overflow-hidden rounded-xl border border-edge bg-panel shadow-2xl" role="dialog" tabindex="-1" aria-modal="true" aria-label="Search projects, sessions and actions">
    <div class="flex items-center gap-2.5 border-b border-edge/70 px-4">
      <Icon name="search" size={16} class="shrink-0 text-faint" />
      <input bind:this={input} class="h-12 min-w-0 flex-1 bg-transparent text-base text-ink outline-none placeholder:text-faint" placeholder="Search projects, sessions and actions…" aria-label="Search projects, sessions, and actions" role="combobox" aria-autocomplete="list" aria-expanded="true" aria-controls="command-results" aria-activedescendant={selected ? `command-${active}` : undefined} bind:value={query} oninput={() => (index = 0)} onkeydown={key} />
    </div>
    <div id="command-results" role="listbox" aria-label="Results" class="min-h-0 flex-1 overflow-auto p-1.5">
      {#each results as r, i (r.id)}
        {@const match = searchMatch(query, r.label, r.detail)}
        {#if i === 0 || results[i - 1].kind !== r.kind}
          <div class="label px-2.5 pt-2 pb-1 text-faint" role="presentation">{HEADING[r.kind]}</div>
        {/if}
        <button id={`command-${i}`} role="option" tabindex="-1" aria-selected={selected?.id === r.id} class="flex h-9 w-full min-w-0 items-center gap-2.5 rounded-md px-2.5 text-left hover:bg-sel/60 {selected?.id === r.id ? 'bg-sel' : ''}" onmousemove={() => (index = i)} onclick={() => run(r.run)}>
          <Icon name={r.icon} size={15} class="shrink-0 text-faint" />
          <span class="min-w-0 shrink-0 truncate text-ink" style="max-width:60%"><SearchHighlight text={r.label} indices={match?.[0]} /></span>
          <span class="min-w-0 flex-1 truncate text-sm text-faint" title={r.detail}><SearchHighlight text={r.detail} indices={match?.[1]} /></span>
          <span class="shrink-0 text-sm text-faint">{KIND_LABEL[r.kind]}</span>
        </button>
      {:else}<p class="px-3 py-8 text-center text-sm text-faint">No matching projects, sessions, or actions.</p>{/each}
    </div>
    <div class="flex h-10 items-center gap-3 border-t border-edge/70 px-3 text-sm text-faint">
      <span class="mr-auto">↑↓ Select · Esc Close</span>
      <span class="text-ink">Open <kbd class="ml-1 rounded bg-sel px-1.5 py-0.5 font-sans">↵</kbd></span>
      <button type="button" class="flex items-center gap-1.5 rounded px-1.5 py-1 text-ink hover:bg-sel" aria-haspopup="menu" aria-expanded={optionsOpen} disabled={!selected} onclick={toggleOptions}>Actions <kbd class="rounded bg-sel px-1.5 py-0.5 font-sans">⌘K</kbd></button>
    </div>
    {#if optionsOpen}
      <div class="absolute right-3 bottom-12 z-10 w-72 overflow-hidden rounded-lg border border-edge bg-panel shadow-xl">
        <p class="truncate px-3 pt-2.5 pb-1 text-sm text-faint">{selected?.label}</p>
        <div id="command-options" role="menu" aria-label="{selected?.label} actions" class="max-h-60 overflow-auto p-1">
          {#each shownOptions as o, i (o.id)}
            <button id="command-option-{i}" role="menuitem" tabindex="-1" class="flex h-8 w-full items-center justify-between gap-2 rounded px-2 text-left text-sm hover:bg-sel disabled:opacity-40 {optionsIndex === i ? 'bg-sel' : ''}" disabled={!o.enabled} onmousemove={() => (optionsIndex = i)} onclick={() => run(o.run)}>{o.label}<span class="text-faint">{o.shortcut || ""}</span></button>
          {:else}<p class="px-2 py-3 text-sm text-faint">No matching actions.</p>{/each}
        </div>
        <input bind:this={optionsInput} role="combobox" aria-expanded="true" aria-controls="command-options" aria-activedescendant={shownOptions[optionsIndex] ? `command-option-${optionsIndex}` : undefined} class="w-full border-t border-edge/70 bg-transparent px-3 py-2 text-sm outline-none placeholder:text-faint" placeholder="Search for actions…" aria-label="Search for actions" bind:value={optionsQuery} oninput={() => (optionsIndex = 0)} onkeydown={optionsKey} />
      </div>
    {/if}
  </div>
</div>
