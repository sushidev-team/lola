<script lang="ts">
  import { onMount } from "svelte";
  import { store } from "$lib/store.svelte";
  import { appearance } from "$lib/theme-runtime.svelte";
  import { connection } from "@mobile/lib/connection.svelte";
  import { nav } from "@mobile/lib/nav.svelte";
  import Connect from "@mobile/views/Connect.svelte";
  import Sessions from "@mobile/views/Sessions.svelte";
  import Terminal from "@mobile/views/Terminal.svelte";

  // The whole app: three screens and the rules for moving between them.
  //
  // DELIBERATELY NOT the desktop's App.svelte. That file is a keyboard router
  // and a macOS menu bridge — bare keys for actions, Cmd chords delegated to
  // AppKit, an overlay stack, a two-pane cockpit. None of it means anything on a
  // phone, where there are no chords, no menu bar, and one screen at a time.
  //
  // ONE RULE WORTH STATING. Losing the connection does NOT send the user back to
  // Connect. PLAN.md is explicit that an off-network phone must land on the last
  // snapshot behind a banner naming the actual reason, and must never be shown
  // the pairing screen — because that screen is what REVOCATION looks like, and
  // if the app cannot tell "your WiFi changed" from "this device was revoked",
  // neither can the person holding it. So a dropped connection stays where it
  // is, and only an explicit disconnect returns here.

  onMount(() => {
    // Paint the flavor before anything else. Step one is synchronous from
    // localStorage, so a non-default theme never flashes; step two asks the
    // daemon and is a no-op if the shim cannot answer yet.
    void appearance.init();

    // Subscribe to the daemon push events. Idempotent, and safe to call before
    // a transport exists: the shim's first poll simply fails and the store
    // reports "connecting" rather than inventing an empty list.
    store.start();

    // If a previous run remembered an endpoint AND its key survived in the
    // keychain, go straight past the connect screen. Anything less than both
    // lands on the form with what is known already filled in.
    void (async () => {
      const prev = await connection.restore();
      if (!prev || prev.key === "") return;
      if (await connection.connect(prev.draft, prev.key, false)) {
        void store.refresh();
        nav.toSessions();
      }
    })();
  });

  // Reaching `ready` from anywhere means the list is now worth showing. Written
  // as an effect rather than folded into the connect handler so an
  // auto-reconnect that succeeds while the user is staring at the form also
  // moves them along.
  $effect(() => {
    if (connection.ready && nav.screen === "connect") nav.toSessions();
  });
</script>

<!-- h-dvh, not h-screen: the dynamic viewport unit is the one that accounts for
     Safari's collapsing chrome, and a terminal that is 60px taller than the
     window puts the accessory bar under the home indicator. -->
<div class="h-dvh w-full overflow-hidden bg-canvas text-ink">
  {#if nav.screen === "connect"}
    <Connect
      onconnected={() => {
        void store.refresh();
        nav.toSessions();
      }}
    />
  {:else if nav.screen === "terminal"}
    <Terminal onback={() => nav.toSessions()} />
  {:else}
    <Sessions />
  {/if}
</div>
