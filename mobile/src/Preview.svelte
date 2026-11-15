<script lang="ts">
  import { onMount } from "svelte";
  import App from "./App.svelte";
  import type { Fixture } from "./fixtures";
  import { applyDevLink } from "@mobile/lib/devlink";
  import { connection } from "@mobile/lib/connection.svelte";
  import { parseLocation, router } from "@mobile/lib/router.svelte";

  // The fixture runner (spec §5.6): the real app in a 390 x 844 frame, on a
  // scripted Mac, opened at the route the URL names.
  //
  // Invariants:
  //  - The route is applied the way a development link is (`applyDevLink`), after
  //    the app's own boot has chosen its first screen, so a fixture can show
  //    Connect on a phone that has nothing paired and a screen on one that has.
  //  - `kb` only sets the inset the keyboard module would; no keyboard exists.

  let { fixture, path, kb }: { fixture: Fixture; path: string; kb: number } = $props();

  // `?bare` fills the real viewport (a Simulator WebView) instead of the 390 x 844 frame.
  const bare = new URLSearchParams(location.search).has("bare");

  onMount(() => {
    if (kb > 0) {
      document.documentElement.toggleAttribute("data-kb", true);
      document.documentElement.style.setProperty("--kb", `max(0px, calc(${kb}px - env(safe-area-inset-bottom, 0px)))`);
    }
    void (async () => {
      // The app reads the paired list first; a macrotask later its boot has finished.
      await connection.refresh();
      await new Promise<void>((resolve) => setTimeout(resolve, 0));
      const at = path === "" ? null : parseLocation(path, { paired: connection.daemons.length > 0 });
      if (at) applyDevLink(router, { route: at.route, sheet: at.sheet, tab: at.tab });
      await fixture.after?.();
    })();
  });
</script>

<div class="stage" class:bare>
  <div class="phone" class:bare data-fixture={fixture.name}>
    <App />
  </div>
</div>

<style>
  .stage {
    display: flex;
    justify-content: center;
    min-height: 100dvh;
    background: var(--color-crust);
  }
  .phone {
    position: relative;
    width: 390px;
    height: 844px;
    overflow: hidden;
    transform: translateZ(0);
  }
  .stage.bare {
    display: block;
    min-height: 0;
  }
  .phone.bare {
    width: 100%;
    height: 100dvh;
  }
  /* The app's root is a full-window column; the frame stands in for the window. */
  .phone > :global(*) {
    height: 100%;
  }
</style>
