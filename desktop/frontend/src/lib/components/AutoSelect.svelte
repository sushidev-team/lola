<script lang="ts">
  import { store, scopedSessions } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";

  // Keeps a live selection: pick the first row when nothing is selected, and
  // re-pick if the selected session drops out of the list (killed / filtered).
  //
  // This lives in its OWN child component on purpose. In the production WKWebView
  // the Cockpit VIEW container does not re-run its effects on the async daemon
  // push (store.sessions arrives empty at first paint and the view never reacts to
  // the later fill — see WKWEBVIEW_REACTIVITY in Cockpit.svelte), so the same
  // effect placed there never fired and the lower panel stayed on "select a
  // session". A leaf component's own effects DO react to the store, so hosting the
  // selection logic here fixes it. Renders nothing.
  $effect(() => {
    const rows = scopedSessions(store.sessions, nav.scoped, nav.project);
    if (rows.length > 0 && !rows.some((r) => r.id === nav.selectedId)) {
      nav.select(rows[0].id);
    }
  });
</script>
