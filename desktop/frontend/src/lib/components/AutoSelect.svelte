<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { visibleRoster } from "$lib/roster";

  // Select an initial session from the visible, stable sidebar order. Once open,
  // a session remains on screen through filtering, daemon gaps and cleanup.
  // This must remain a leaf: the parent cockpit does not track daemon pushes
  // reliably in production WKWebView (see WKWEBVIEW_REACTIVITY).
  $effect(() => {
    // Explicitly opening a terminal wins over a stale session snapshot. A new
    // session can take another push to arrive; never substitute an older row.
    if (nav.focusedTerm || nav.view !== "cockpit") return;
    const rows = visibleRoster(store.rosterSessions, store.projects.map(p => p.name), n => store.displayNameFor(n), { query: nav.sidebarQuery, triage: nav.triage, scoped: nav.scoped, project: nav.project });
    if (rows.length > 0) {
      nav.openSession(rows.find(r => r.id === nav.selectedId)?.id ?? rows[0].id);
    }
  });
</script>
