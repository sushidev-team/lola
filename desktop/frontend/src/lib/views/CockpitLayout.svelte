<script lang="ts">
  import Rail from "$lib/components/Rail.svelte";
  import SessionsColumn from "$lib/components/SessionsColumn.svelte";
  import AutoSelect from "$lib/components/AutoSelect.svelte";

  // The cockpit body. There is NO fullscreen ⇄ split `{#if}` toggle here anymore:
  // swapping an `{#if}` branch that mounts/unmounts a LiveTerminal (WebGL) FREEZES
  // the enclosing component's template effect in the production WKWebView (verified
  // live — the same toggle with plain placeholders worked, adding a terminal to
  // either branch wedged it). So the split view is ALWAYS mounted and "focus"
  // (fullscreen) is done by SessionsColumn expanding its EXISTING detail terminal
  // to a fixed overlay via CSS — no remount, no freeze. See SessionsColumn.svelte.
</script>

<div class="grid h-full min-h-0 gap-2 p-2" style="grid-template-columns:300px minmax(0,1fr)">
  <!-- Keeps a live selection so the lower panel has a session to show. -->
  <AutoSelect />

  <!-- left rail -->
  <aside class="min-h-0 overflow-hidden">
    <Rail />
  </aside>

  <!-- main column — a component (not inline markup) so it reacts to the store -->
  <SessionsColumn />
</div>
