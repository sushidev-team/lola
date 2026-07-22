<script lang="ts">
  import CockpitLayout from "./CockpitLayout.svelte";

  // Thin wrapper. The actual cockpit body (and the fullscreen ⇄ split toggle) live
  // in <CockpitLayout>, a component this container MOUNTS — because this view
  // container's own template does not reliably re-run on nav/store changes in the
  // production WKWebView, whereas a mounted child component's template does. See
  // CockpitLayout.svelte and store.svelte.ts for the two reactivity fixes.
</script>

<CockpitLayout />
