<script lang="ts">
  import { onMount } from "svelte";
  import { store } from "$lib/store.svelte";
  import { appearance } from "$lib/theme-runtime.svelte";
  import { connection } from "@mobile/lib/connection.svelte";
  import { nav } from "@mobile/lib/nav.svelte";
  import { installAppState } from "@mobile/lib/appstate";
  import { installDevLink, type DevLinkTarget } from "@mobile/lib/devlink";
  import { installDynamicType } from "@mobile/lib/dynamictype";
  import { pairing } from "@mobile/lib/pairing.svelte";
  import Connect from "@mobile/views/Connect.svelte";
  import Sessions from "@mobile/views/Sessions.svelte";
  import Splash from "@mobile/views/Splash.svelte";
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
  //
  // THAT RULE NOW HAS TEETH ON THE BOOT PATH TOO, which it did not before. A
  // cold launch that has a remembered Mac and its key and simply cannot reach it
  // used to fall through to the connect form — the pairing screen, for the one
  // case that is definitionally not revocation. It goes to the session list
  // instead, with the banner and the retry ladder running. The form is still
  // where a REFUSAL lands, because a refused key, a mismatched pin and a version
  // skew each need a human and a field; `diagnosis.retryable` is exactly that
  // distinction and is what the boot branches on.

  // BOOT STATE. The restore path is asynchronous — the keychain, then a dial —
  // so until it answers the app cannot know whether it is heading for the
  // session list or the connect form. Rendering the form meanwhile asks for
  // credentials that may already be stored and then replaces itself a second
  // later, so the splash holds the screen instead. It is released on EVERY
  // outcome, including the failures, which is why the release sits in a finally
  // rather than after the happy path.
  let booting = $state(true);
  let bootMessage = $state("Starting…");

  // A boot must never be able to strand someone on a logo. The restore path is
  // already bounded (Connection#awaitTransport gives up after 4s), but this is
  // the backstop for anything that is not: whatever happens, the form is
  // reachable. It is deliberately longer than the transport wait, so the
  // specific failure wins the race and the generic one is only ever a
  // last resort.
  const BOOT_CEILING_MS = 8000;

  onMount(() => {
    // Paint the flavor before anything else. Step one is synchronous from
    // localStorage, so a non-default theme never flashes; step two asks the
    // daemon and is a no-op if the shim cannot answer yet.
    void appearance.init();

    // The system text size, measured once and tracked. The stylesheet's type
    // scale is rem against the root size this writes; see dynamictype.ts for
    // why a measurement is the only way to read the setting at all.
    const offType = installDynamicType();

    // Development URLs, if this is a build that has them. Registered HERE and
    // not on the connect screen because the plugin retains a pending link until
    // something consumes it: a cold launch delivers the URL while the WebView
    // is still loading, and a listener that waited for a screen to mount would
    // be handed a link nobody could use.
    // The source travels with the payload: a URL from the OS router may only
    // fill the form, a link from this process's own launch environment may
    // connect. `devLinkSource` decides, and fails closed toward the form.
    const offDevLink = installDevLink((p, source, target) =>
      pairing.offer(p, source, target),
    );

    // Subscribe to the daemon push events. Idempotent, and safe to call before
    // a transport exists: the shim's first poll simply fails and the store
    // reports "connecting" rather than inventing an empty list.
    store.start();

    // Coming back from the background. The plugin closes the socket on the way
    // out — see appstate.ts — so something has to reopen it, and this is that
    // something. It is registered here rather than on a screen because the app
    // can be suspended from any of them, and the connection applies its own
    // gates (an explicit disconnect, a connect already in flight, no stored
    // key), so this callback is unconditional on purpose.
    const offAppState = installAppState(() => {
      void (async () => {
        if (await connection.reconnect()) void store.refresh();
      })();
    });

    // If a previous run remembered an endpoint AND its key survived in the
    // keychain, go straight past the connect screen. Anything less than both
    // lands on the form with what is known already filled in.
    const ceiling = setTimeout(() => {
      booting = false;
    }, BOOT_CEILING_MS);

    void (async () => {
      try {
        bootMessage = "Checking for a saved connection…";
        const prev = await connection.restore();
        if (!prev || prev.key === "") return;
        // Name the endpoint being dialled. A boot that hangs here is the case
        // where knowing WHICH daemon is unreachable is the whole diagnosis.
        bootMessage = `Connecting to ${prev.draft.host}:${prev.draft.port}…`;
        if (await connection.connect(prev.draft, prev.key, false)) {
          void store.refresh();
          nav.toSessions();
        } else if (connection.diagnosis.retryable) {
          // Unreachable, not refused: the credential is intact and the daemon
          // is simply not answering right now. Show the list behind its banner
          // and keep trying quietly — `retryLater` arms the ladder without
          // paying a second connect timeout on top of the one just spent.
          nav.toSessions();
          connection.retryLater();
        }
      } finally {
        clearTimeout(ceiling);
        booting = false;
      }
    })();

    return () => {
      clearTimeout(ceiling);
      offAppState();
      offDevLink();
      offType();
    };
  });

  // Reaching `ready` from anywhere means the list is now worth showing. Written
  // as an effect rather than folded into the connect handler so an
  // auto-reconnect that succeeds while the user is staring at the form also
  // moves them along.
  $effect(() => {
    // `landed` has already moved on by the time this runs for a hand-off, so the
    // screen check is what keeps a deep link from being overwritten by the list.
    if (connection.ready && nav.screen === "connect") nav.toSessions();
  });

  // A hand-off can land at any moment — the OS delivers a URL whenever it
  // decides to, including during a cold launch and while the sessions list is
  // open — and only the connect screen knows how to apply one. Routing here
  // rather than inside the inbox keeps `pairing` free of any idea of
  // navigation, so it stays a plain module a test can drive.
  $effect(() => {
    if (pairing.pending && nav.screen !== "connect") nav.toConnect();
  });

  // A hand-off that lands mid-boot ends the boot. Otherwise the offer would sit
  // behind the logo until the ceiling fired, which reads as the link having been
  // ignored.
  $effect(() => {
    if (pairing.pending) booting = false;
  });

  /**
   * Where a fresh connection lands.
   *
   * The list, unless a DEVELOPMENT link named a pane — in which case the app
   * opens it directly. That exists for one reason: the terminal is the screen
   * this whole app is a bet on and it was the only screen a reviewer could not
   * produce a screenshot of, because it is reached solely by tapping a row and
   * the Simulator offers no way to synthesise that tap. Routing it HERE rather
   * than inside the connect screen keeps navigation out of `pairing`, exactly
   * as the offer inbox already does.
   */
  function landed(target?: DevLinkTarget | null): void {
    void store.refresh();
    if (target) nav.toTerminal(target.session, target.pane);
    else nav.toSessions();
  }

  // Derived rather than stored, so the banner cannot outlive the connection it
  // describes: a dropped link connection stops rendering it without anything
  // having to remember to clear a flag.
  const devBanner = $derived(pairing.devLinkActive && connection.ready);
</script>

<!-- h-dvh, not h-screen: the dynamic viewport unit is the one that accounts for
     Safari's collapsing chrome, and a terminal that is 60px taller than the
     window puts the accessory bar under the home indicator. -->
<div class="flex h-dvh w-full flex-col overflow-hidden bg-canvas text-ink">
  {#if devBanner}
    <!-- THE OBLIGATION THAT COMES WITH `lola-dev://`. The plugin will only turn
         a URL into a connection in a debug build, and in exchange the app has
         to say so for as long as that connection is up — which is what makes
         the scheme a labelled test fixture rather than a hidden back door. It
         is deliberately NOT dismissible, it sits above every screen rather than
         on one of them, and it pays back the top inset itself so the screens
         below keep their own layout unchanged. -->
    <div
      class="shrink-0 border-b border-warn/40 bg-warn/10 px-4 pb-1.5 pt-safe-t text-center"
      role="status"
    >
      <span class="text-sm text-warn">Connected by a development link</span>
    </div>
  {/if}
  <!-- When the banner is up it has paid the top inset, so the screens must not
       pay it again — see --spacing-safe-t in app.css. -->
  <div class="min-h-0 flex-1" style={devBanner ? "--lola-top-inset: 0px" : ""}>
  {#if booting}
    <Splash message={bootMessage} />
  {:else if nav.screen === "connect"}
    <Connect onconnected={landed} />
  {:else if nav.screen === "terminal"}
    <Terminal onback={() => nav.toSessions()} />
  {:else}
    <Sessions />
  {/if}
  </div>
</div>
