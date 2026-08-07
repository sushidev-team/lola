<script lang="ts">
  import { store, scopedSessions } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { TRIAGE_FILTERS, matchesTriage } from "$lib/filters";
  import { KANBAN_COLUMNS, statusText } from "$lib/theme";
  import NavRow from "./NavRow.svelte";

  // The four proportional meters became six clickable, counted rows: a meter
  // showed a ratio nobody acted on, a row filters the list. Reads the store
  // itself (leaf component) — a parent computing these and passing them down
  // does not re-render on the async daemon push in the production WKWebView.

  // The PROJECT-SCOPED but UN-TRIAGED list: the counts must describe what
  // clicking a row would show, not what the active filter already hid.
  const rows = $derived(scopedSessions(store.sessions, nav.scoped, nav.project));

  const counts = $derived.by(() => {
    const m: Record<string, number> = {};
    for (const f of TRIAGE_FILTERS) m[f] = rows.filter((s) => matchesTriage(s.status, f)).length;
    return m;
  });

  // The bucket dot borrows its colour from the bucket's own first status, so it
  // can never disagree with the pill/table colours theme.ts hands out.
  function dotCls(title: string): string {
    const c = KANBAN_COLUMNS.find((k) => k.title === title);
    return c ? statusText(c.statuses[0]) : "text-faint";
  }
</script>

<nav class="px-3 pt-3" aria-label="Triage">
  <h2 class="label px-2 pb-1 text-faint">Triage</h2>
  <NavRow
    label="All sessions"
    count={rows.length}
    glyph="○"
    glyphCls="text-faint"
    active={nav.triage === ""}
    title="show every session"
    onclick={() => nav.setTriage("")}
  />
  {#each TRIAGE_FILTERS as f (f)}
    <NavRow
      label={f}
      count={counts[f] ?? 0}
      glyph="●"
      glyphCls={dotCls(f)}
      active={nav.triage === f}
      title="show only {f.toLowerCase()}"
      onclick={() => nav.setTriage(f)}
    />
  {/each}
</nav>
