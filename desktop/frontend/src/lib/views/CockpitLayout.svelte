<script lang="ts">
  import SessionsColumn from "$lib/components/SessionsColumn.svelte";
  import AutoSelect from "$lib/components/AutoSelect.svelte";

  // The cockpit body. There is NO fullscreen ⇄ split `{#if}` toggle here anymore:
  // swapping an `{#if}` branch that mounts/unmounts a LiveTerminal (WebGL) FREEZES
  // the enclosing component's template effect in the production WKWebView (verified
  // live — the same toggle with plain placeholders worked, adding a terminal to
  // either branch wedged it). So the split view is ALWAYS mounted and "focus"
  // (fullscreen) is done by SessionsColumn expanding its EXISTING detail terminal
  // to a fixed overlay via CSS — no remount, no freeze. See SessionsColumn.svelte.
  //
  // The left rail is gone: triage, projects and activity moved into the app-level
  // <Sidebar>, so the cockpit is a single full-width column. It stays a GRID (not
  // a bare block) because grid cells stretch to the container in WKWebView, which
  // is what gives SessionsColumn its height.
</script>

<!-- No padding: the cockpit is a stack of full-bleed BANDS separated by hairlines,
     not a tray of floating cards. The 12px inset used to be spent three times over
     (here, the panel border, the panel's own padding) before any session was
     drawn. Each band owns its inner padding instead. -->
<div class="grid h-full min-h-0" style="grid-template-columns:minmax(0,1fr)">
  <!-- Keeps a live selection so the lower panel has a session to show. -->
  <AutoSelect />

  <!-- main column — a component (not inline markup) so it reacts to the store -->
  <SessionsColumn />
</div>
