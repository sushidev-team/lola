<script lang="ts">
  import { onMount, tick } from "svelte";
  import { toggleGitWorkspace } from "$lib/workspacenav";
  import { nav } from "$lib/nav.svelte";
  import { store } from "$lib/store.svelte";
  import { projectActions, openSession, openShell, newSessionHere } from "$lib/projectActions";
  import { ssh } from "$lib/ssh.svelte";
  import { hub } from "$lib/hub.svelte";
  import { hubReason } from "$lib/hubboard";
  import { matchesSearch, searchMatch } from "$lib/search";
  import SearchHighlight from "./SearchHighlight.svelte";
  import Modal from "./Modal.svelte";

  let query = $state("");
  let index = $state(0);
  let input: HTMLInputElement;
  type Result = { id: string; label: string; detail: string; project?: string; run: () => void };
  const current = $derived(store.sessionById(nav.focusedTerm || nav.selectedId));
  const results = $derived.by<Result[]>(() => {
    const commands: Result[] = [
      { id: "git", label: nav.inGit ? "Back from Git" : "Open Git workspace", detail: "⌘⇧G", run: toggleGitWorkspace },
      { id: "sidebar", label: "Toggle sidebar", detail: "⌘B", run: () => nav.toggleSidebar() },
      { id: "focus-sessions", label: "Focus sessions", detail: "⌘⇧E", run: () => nav.focusSessions() },
      { id: "shortcuts", label: "Keyboard shortcuts", detail: "⌘/", run: () => nav.openOverlay("help") },
      { id: "doctor", label: "Doctor", detail: "Action", run: () => nav.openOverlay("doctor") },
      { id: "new", label: "New session…", detail: "Action", run: () => newSessionHere() },
      { id: "temporary", label: "New temporary shell", detail: "Action · default directory", run: () => { void openShell(); } },
      { id: "ssh", label: "New SSH session…", detail: "Action · an agent and a terminal on a host", run: () => ssh.newSession() },
      ...(current && current.location !== "ssh" ? [{ id: "ssh-connect", label: "Connect this session to a host…", detail: `Action · ${current.title || current.id.slice(0, 8)}`, run: () => { nav.openSession(current.id); ssh.connectHost(current.id); } }] : []),
      ...(current && current.location !== "ssh" && current.prNumber > 0 ? [{ id: "coderabbit-poll", label: "Poll CodeRabbit reviews", detail: `Action · ${current.issue || current.title || current.id}`, run: () => { void store.coderabbit(current.id); } }] : []),
      ...ssh.hosts.map((h) => ({ id: `ssh:${h.name}`, label: `SSH to ${h.label || h.name}`, detail: `Action · ${h.tier}`, run: () => ssh.newSession(h.name) })),
      { id: "activity", label: "View all activity", detail: "Action", run: () => nav.openOverlay("activity") },
      { id: "settings", label: "Settings", detail: "Action", run: () => nav.openOverlay("settings") },
      { id: "team-open", label: "Team: Open", detail: hub.needsCount ? `Action · ${hub.needsCount} ${hub.needsCount === 1 ? "chore needs" : "chores need"} you` : "Action · the team hub's chores and Macs", run: () => nav.goTeam() },
      ...(hub.joined ? hub.needs : []).slice(0, 5).map((n) => ({ id: `team-needs:${n.id}`, label: `Team: ${n.title}`, detail: `Needs you · ${n.why}`, run: () => { hub.focus(n.id); nav.goTeam(); } })),
      { id: "team-sync", label: "Team: Sync now", detail: "Action · ask the team hub for news", run: () => { void syncTeam(); } },
    ];
    const projects = store.projects.map(p => ({ id: `p:${p.name}`, label: store.displayNameFor(p.name), detail: p.path || p.repo, project: p.name, run: () => nav.goDetail(p.name) }));
    const sessions = [...store.sessions].sort((a,b) => String(b.firstSeen || "").localeCompare(String(a.firstSeen || ""))).map(s => ({ id: `s:${s.id}`, label: s.title || s.branch || s.id, detail: `Session · ${s.project ? store.displayNameFor(s.project) : "Temporary"} · ${s.branch || s.worktree}`, run: () => openSession(s.id) }));
    const filter = (rows: Result[]) => rows.filter(r => matchesSearch(query,r.label,r.detail));
    return [...filter(commands), ...filter(sessions).slice(0,12), ...filter(projects)].slice(0,40);
  });
  const selected = $derived(results[Math.min(index, Math.max(0, results.length - 1))]);
  const project = $derived(selected?.project ? store.projectByName(selected.project) : undefined);
  const actions = $derived(project ? projectActions(project) : []);
  onMount(() => { void tick().then(() => input?.focus()); void ssh.loadHosts(); });
  // The daemon answers in its own words, through the one mapper (hubReason);
  // a hub that is not joined says so.
  async function syncTeam() {
    const r = await hub.sync();
    store.setFlash(r.ok ? "Team hub sync queued" : hubReason(r.error), r.ok ? "good" : "warn");
  }
  function run(action: () => void) { nav.closeOverlay(); action(); }
  function key(e: KeyboardEvent) {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault(); index = Math.max(0, Math.min(results.length - 1, index + (e.key === "ArrowDown" ? 1 : -1)));
      void tick().then(() => document.getElementById(`command-${index}`)?.scrollIntoView?.({block:"nearest"}));
    } else if ((e.metaKey || e.ctrlKey) && (e.key === "Enter" || e.key.toLowerCase() === "o")) {
      e.preventDefault(); const action = actions.find(a => a.id === (e.key === "Enter" ? "new" : "shell"));
      if (action?.enabled) run(action.run);
    } else if (e.key === "Enter" && selected) { e.preventDefault(); run(selected.run); }
  }
</script>
<Modal title="Go to…" onClose={() => nav.closeOverlay()} width="760px" bodyClass="overflow-hidden">
  <div class="border-b border-edge p-3">
    <input bind:this={input} class="w-full bg-transparent px-2 py-2 text-lg text-ink outline-none" placeholder="Search projects, sessions, and actions…" aria-label="Search projects, sessions, and actions" role="combobox" aria-autocomplete="list" aria-expanded="true" aria-controls="command-results" aria-activedescendant={selected ? `command-${index}` : undefined} bind:value={query} oninput={() => index=0} onkeydown={key} />
  </div>
  <div class="grid min-h-0 grid-cols-[minmax(0,1fr)_220px] max-sm:grid-cols-1">
    <div id="command-results" role="listbox" aria-label="Results" class="max-h-[52vh] overflow-auto p-2">
      {#each results as r, i (r.id)}
        {@const match = searchMatch(query, r.label, r.detail)}
        <button id={`command-${i}`} role="option" aria-selected={selected?.id === r.id} class="grid w-full gap-1 rounded-md px-3 py-2.5 text-left hover:bg-sel/60 {selected?.id === r.id ? 'bg-sel' : ''}" onfocus={() => index=i} onclick={() => run(r.run)}>
          <span class="truncate text-ink"><SearchHighlight text={r.label} indices={match?.[0]} /></span><span class="truncate text-sm text-faint" title={r.detail}><SearchHighlight text={r.detail} indices={match?.[1]} /></span>
        </button>
      {:else}<p class="px-3 py-8 text-sm text-faint">No matching projects, sessions, or actions.</p>{/each}
    </div>
    <div class="border-l border-edge p-3 max-sm:border-t max-sm:border-l-0">
      {#if project}
        <p class="mb-2 truncate text-sm font-medium">{store.displayNameFor(project.name)}</p>
        {#each actions as action (action.id)}
          <button class="flex w-full items-center justify-between gap-2 rounded px-2 py-2 text-left text-sm hover:bg-sel disabled:opacity-40" disabled={!action.enabled} onclick={() => run(action.run)}>{action.label}<span class="text-faint">{action.shortcut || ""}</span></button>
        {/each}
      {:else}<p class="text-sm text-faint">Select a project to see its actions and shortcuts.</p>{/if}
    </div>
  </div>
  <div class="border-t border-edge px-4 py-2 text-sm text-faint">↑ ↓ Select · Enter Open · Esc Close</div>
</Modal>
