<script lang="ts">
  import { onMount } from "svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import type { TicketsData, TicketRow } from "@bindings/internal/protocol";
  import Button from "$lib/components/Button.svelte";
  import Select from "$lib/components/Select.svelte";

  type Scope = "mine" | "team";
  const scopes: { id: Scope; label: string }[] = [
    { id: "mine", label: "Mine" },
    { id: "team", label: "Team" },
  ];

  let scope = $state<Scope>("mine");
  let loading = $state(false);
  let error = $state("");
  let starting = $state("");
  let filter = $state("");
  let selectedAgent = $state("");
  let data = $state<TicketsData | null>(null);

  const project = $derived(store.projectByName(nav.project));
  const projectAgent = $derived(project?.agent || "claude");
  const issues = $derived(data?.issues ?? []);

  // The heading is the PROJECT — that is what the human navigated into and what
  // the breadcrumb above already names, and the store resolves its label without
  // a round trip.
  const projectLabel = $derived(store.displayNameFor(nav.project));

  // The Linear team is secondary context, and its UUID is deliberately NOT a
  // fallback: `data.team` is what config keys by, and a 36-character hex string
  // where a name belongs reads as a bug (it was one). An unresolvable team simply
  // shows nothing — the project name above it already says where you are.
  const teamLabel = $derived.by(() => {
    if (!data) return "";
    const { teamName, teamKey } = data;
    if (teamName && teamKey) return `${teamName} (${teamKey})`;
    return teamName || teamKey || "";
  });

  // Filters over what the row SHOWS — identifier, title, state, labels,
  // assignee — so typing "progress", "bug" or a name narrows the list the same
  // way an identifier does. A team's backlog is hundreds of rows; a list that
  // long without a search field is a scrollbar, not a picker.
  const rows = $derived.by(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return issues;
    return issues.filter((t) =>
      [t.identifier, t.title, t.state ?? "", t.assignee ?? "", ...(t.labels ?? [])]
        .join(" ")
        .toLowerCase()
        .includes(q),
    );
  });

  async function load() {
    if (!store.alive) return;
    loading = true;
    error = "";
    try {
      data = await store.tickets(nav.project, scope);
    } catch (e) {
      error = String(e);
      data = null;
    } finally {
      loading = false;
    }
  }

  function pick(s: Scope) {
    if (s === scope) return;
    scope = s;
    void load();
  }

  async function start(t: TicketRow) {
    if (t.alreadyLive) {
      store.setFlash(`${t.identifier} already running`, "warn");
      return;
    }
    if (starting) return;
    starting = t.identifier;
    const r = await store.openTicket({
      project: nav.project,
      identifier: t.identifier,
      uuid: t.uuid,
      branch: t.branch,
      title: t.title,
      agentKind: selectedAgent || undefined,
    });
    starting = "";
    if (r) nav.goCockpit(nav.project);
  }

  function prio(p: number): { label: string; cls: string } {
    switch (p) {
      case 1:
        return { label: "Urgent", cls: "text-bad" };
      case 2:
        return { label: "High", cls: "text-warn" };
      case 3:
        return { label: "Medium", cls: "text-ink" };
      case 4:
        return { label: "Low", cls: "text-faint" };
      default:
        return { label: "—", cls: "text-faint" };
    }
  }

  // A workflow state is TEAM text ("Doing", "Ready for QA"), so the colour comes
  // from the stable state TYPE beside it. The chips are the app's existing pill
  // tokens, and only the states that ask for attention take a fill — a backlog
  // of 300 rows each wearing a chip is a wall, not a signal.
  function stateChip(t: TicketRow): { label: string; cls: string } {
    const label = t.state || "—";
    switch (t.stateType) {
      case "started":
        return { label, cls: "bg-pill-work text-pill-work-fg" };
      case "triage":
        return { label, cls: "bg-pill-urgent text-pill-urgent-fg font-semibold" };
      case "unstarted":
        return { label, cls: "bg-pill-grey text-pill-grey-fg" };
      case "completed":
        return { label, cls: "bg-pill-done text-pill-done-fg" };
      case "canceled":
        return { label, cls: "text-faint line-through" };
      default:
        return { label, cls: "text-faint" };
    }
  }

  onMount(() => {
    void load();
  });
</script>

<div class="flex h-full min-h-0 flex-col p-4">
  <!-- No ‹ back button: MainTopBar's breadcrumb already carries one (the project
       crumb returns to the detail), and two arrows a few pixels apart doing
       different things is worse than either alone. -->
  <div class="mb-3 flex items-center gap-3">
    <span class="min-w-0 truncate text-sm text-faint">
      <span class="text-ink">{projectLabel}</span>
      {#if teamLabel}
        <span class="text-edge">·</span>
        <span>{teamLabel}</span>
      {/if}
      {#if data}
        <span class="text-edge">·</span>
        <span class="num">{rows.length}</span>
        {#if rows.length !== issues.length}<span class="num">of {issues.length}</span>{/if}
        issue{issues.length === 1 ? "" : "s"}
      {/if}
    </span>

    <input
      class="ml-auto w-56 rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder"
      placeholder="filter issues…"
      aria-label="Filter issues"
      bind:value={filter}
    />

    <Select class="w-52 text-sm" bind:value={selectedAgent} aria-label="Coding agent">
      <option value="">Project default ({projectAgent})</option>
      <option value="claude">claude</option>
      <option value="codex">codex</option>
      <option value="opencode">opencode</option>
    </Select>

    <!-- The scope switcher IS the scope label — the old "scope mine" caption
         under it restated the pressed button. -->
    <span class="flex items-center gap-0.5 rounded-md border border-edge p-0.5">
      {#each scopes as s (s.id)}
        <Button size="xs" selected={scope === s.id} onclick={() => pick(s.id)}>{s.label}</Button>
      {/each}
    </span>
    <Button variant="primary" size="md" disabled={loading || !store.alive} onclick={() => load()}>
      <span aria-hidden="true">↻</span> Refresh
    </Button>
  </div>

  <div class="min-h-0 flex-1 overflow-auto rounded-[10px] border border-edge">
    {#if !store.alive}
      <div class="px-3 py-8 text-center text-faint">Daemon offline — start it to browse issues.</div>
    {:else if loading}
      <div class="px-3 py-8 text-center text-faint">Loading issues…</div>
    {:else if error}
      <div class="px-3 py-8 text-center text-bad">{error}</div>
    {:else if issues.length === 0}
      <div class="px-3 py-8 text-center text-faint">No issues in this scope.</div>
    {:else if rows.length === 0}
      <div class="px-3 py-8 text-center text-faint">No issue matches “{filter}”.</div>
    {:else}
      <table class="w-full table-fixed">
        <thead class="label sticky top-0 bg-panel/95 text-left text-faint backdrop-blur">
          <tr>
            <th class="w-[104px] px-3 py-2">Issue</th>
            <th class="px-3 py-2">Title</th>
            <th class="w-[160px] px-3 py-2">Status</th>
            <th class="w-[92px] px-3 py-2">Priority</th>
            {#if scope === "team"}<th class="w-[128px] px-3 py-2">Assignee</th>{/if}
            <th class="w-[92px] px-3 py-2"></th>
          </tr>
        </thead>
        <tbody>
          {#each rows as t (t.uuid)}
            {@const p = prio(t.priority)}
            {@const st = stateChip(t)}
            <tr class="group border-t border-edge/30 hover:bg-sel/50" class:opacity-60={t.alreadyLive}>
              <td class="cursor-pointer px-3 py-2 font-mono text-sm whitespace-nowrap text-faint" onclick={() => start(t)}>
                {#if t.alreadyLive}<span class="mr-1 text-good" title="already running">●</span>{/if}
                {t.identifier}
              </td>
              <td class="cursor-pointer px-3 py-2" onclick={() => start(t)}>
                <div class="flex min-w-0 items-center gap-2">
                  <span class="truncate text-ink">{t.title}</span>
                  <!-- Labels ride the title rather than taking a column of their
                       own: most issues have none, and the ones that do carry one
                       or two. `shrink-0` so a long title truncates instead. -->
                  {#each (t.labels ?? []).slice(0, 3) as l (l)}
                    <span class="shrink-0 rounded border border-edge px-1 py-[1px] text-sm text-faint">{l}</span>
                  {/each}
                  {#if t.estimate}
                    <span class="num shrink-0 text-sm text-faint" title="estimate">{t.estimate}pt</span>
                  {/if}
                </div>
              </td>
              <td class="px-3 py-2" onclick={() => start(t)}>
                <span class="inline-flex max-w-full items-center truncate rounded px-1.5 py-[1px] text-sm {st.cls}">
                  {st.label}
                </span>
              </td>
              <td class="px-3 py-2 text-sm {p.cls}">{p.label}</td>
              {#if scope === "team"}
                <td class="truncate px-3 py-2 text-sm text-faint">{t.assignee || "—"}</td>
              {/if}
              <td class="px-3 py-1 whitespace-nowrap opacity-0 group-hover:opacity-100">
                <div class="flex items-center justify-end">
                  <Button size="xs" disabled={starting === t.identifier} onclick={() => start(t)}>
                    {#if t.alreadyLive}Live{:else}Start <span aria-hidden="true">›</span>{/if}
                  </Button>
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
</div>
