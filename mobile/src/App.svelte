<script lang="ts">
  import { onMount } from "svelte";
  import { store } from "$lib/store.svelte";
  import { appearance } from "$lib/theme-runtime.svelte";
  import { connection } from "@mobile/lib/connection.svelte";
  import { daemonLabel } from "@mobile/lib/daemonname";
  import { DEFAULT_REMOTE_PORT, endpointId, parsePort } from "@mobile/lib/endpoint";
  import { nav } from "@mobile/lib/nav.svelte";
  import { installAppState } from "@mobile/lib/appstate";
  import { installDevLink, type DevLinkTarget } from "@mobile/lib/devlink";
  import { installDynamicType } from "@mobile/lib/dynamictype";
  import { applyMobileTokens } from "@mobile/lib/mobiletokens";
  import { pairing } from "@mobile/lib/pairing.svelte";
  import TabBar from "@mobile/lib/components/TabBar.svelte";
  import Activity from "@mobile/views/Activity.svelte";
  import Connect from "@mobile/views/Connect.svelte";
  import ProjectDetail from "@mobile/views/ProjectDetail.svelte";
  import Projects from "@mobile/views/Projects.svelte";
  import PRPicker from "@mobile/views/PRPicker.svelte";
  import Sessions from "@mobile/views/Sessions.svelte";
  import Settings from "@mobile/views/Settings.svelte";
  import Splash from "@mobile/views/Splash.svelte";
  import Terminal from "@mobile/views/Terminal.svelte";
  import TicketPicker from "@mobile/views/TicketPicker.svelte";

  // The whole app: three screens, four tabs, and the rules for moving between
  // them.
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
        // Either half is a pairing: `key` for a Keychain that refused and left
        // it in memory, `keyRef` for the ordinary case where the plugin holds
        // it and the plaintext deliberately never crosses the bridge.
        if (!prev || (prev.key === "" && prev.keyRef === "")) return;
        // Name the MACHINE being dialled, not the address. A boot that hangs
        // here is exactly the case where knowing WHICH Mac is unreachable is
        // the whole diagnosis — and the address is the half that changes
        // between home and the office, so it is only the fallback.
        const bootPort = parsePort(prev.draft.port) ?? DEFAULT_REMOTE_PORT;
        bootMessage = `Connecting to ${daemonLabel(
          endpointId(prev.draft.host, bootPort),
          `${prev.draft.host}:${bootPort}`,
        )}…`;
        if (await connection.connect(prev.draft, prev.key, false, [], prev.keyRef)) {
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

  // A LAUNCH LINK'S DESTINATION, HELD UNTIL THERE IS A CONNECTION TO APPLY IT
  // TO. These two effects exist because the pane half of a development link was
  // being lost, which made the terminal screen — the screen this whole app is a
  // bet on — unreachable by link and therefore unscreenshottable, the single
  // thing `DevLinkTarget` exists to prevent.
  //
  // WHAT ACTUALLY HAPPENS, measured on the Simulator rather than guessed at.
  // The plugin accepts the link and posts it ("dev link accepted … via=
  // dev-launch … pane=true" in the device log), the offer routes to the connect
  // screen, and the connect screen's own dial then fails — the boot restore is
  // already holding a connection to the same daemon, and the second attempt
  // ends as "connection failed: network". `onconnected` never fires, so
  // `landed` never runs, and the restore's connection carries on serving a
  // session list with the target gone. Nothing on screen distinguishes that
  // from a link that was never delivered.
  //
  // So the destination is REMEMBERED rather than consumed on arrival, and
  // applied by whichever connection is actually live. The offer itself still
  // travels its ordinary path untouched — this reads it, it does not drain it —
  // so the connect screen, the credential handling and `devLinkActive` behave
  // exactly as before.
  //
  // THE FENCE IS `launch`, AND IT IS THE ONLY ONE NEEDED. That is this
  // process's own launch environment: setting it means having started the app,
  // which is the same reason `devLinkSource` already lets that door dial on its
  // own, while anything the OS URL router delivered stays `link` and is ignored
  // here. A destination grants nothing beyond navigation — it names a pane, and
  // a pane that does not exist on the connected daemon simply reports itself
  // gone in the terminal's own banner. Deliberately NOT also matched against
  // the live endpoint: the remembered endpoint is whichever address the app was
  // first paired on (a LAN address as easily as loopback), so that comparison
  // rejected the ordinary case and put the feature straight back where it was.
  //
  // No banner is set here, correctly: a connection restored from the Keychain
  // did not arrive by link and must not claim that it did.
  let linkTarget = $state<DevLinkTarget | null>(null);

  $effect(() => {
    const p = pairing.pending;
    if (p?.source === "launch" && p.target) linkTarget = p.target;
  });

  $effect(() => {
    const t = linkTarget;
    if (!t || !connection.ready) return;
    linkTarget = null;
    applyTarget(t);
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

  // THE DAEMON NAMES ITSELF, and this is where the app hears it. `cmd=status`
  // carries the machine's hostname on an authenticated answer (never in the
  // mDNS advertisement, which deliberately carries no hostname at all), so the
  // app can say "connecting to marvin" rather than naming an address that
  // changes with every network. A person's own rename still wins over it.
  $effect(() => {
    const host = store.status?.host ?? "";
    if (host !== "") connection.learnName(host);
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
    if (target) applyTarget(target);
    else nav.toSessions();
  }

  /**
   * Put the app where a development link asked for.
   *
   * A destination is several independent things and any of them may be absent:
   * a pane, a triage bucket, a search, a tab, a project, a picker and a sheet.
   * The ORDER here is the whole of the function, and it runs outside-in: the
   * filter is applied first because it describes the list rather than the
   * navigation, then the tab and the project — which say what is UNDER
   * everything else — then the screen is chosen (a pane means the terminal, no
   * pane means the tab shell), and only then are the things opened OVER it: the
   * picker, then the sheet. The sheet is last because `toTerminal` and
   * `toSessions` both clear it, a modal belonging to the screen it was opened
   * over; the picker survives them, because it belongs to the Projects tab's
   * own depth rather than to a screen.
   *
   * Nothing here is a capability. Each field names somewhere the person holding
   * the phone could have reached with one tap; what the link buys is that a
   * SCRIPT can reach it too, which is the only way these screens can be
   * photographed at all.
   */
  function applyTarget(t: DevLinkTarget): void {
    if (t.triage) nav.triage = t.triage;
    if (t.query) nav.query = t.query;

    // THE TAB IS SET BEFORE THE SCREEN, and directly rather than through
    // `nav.toTab`, because the screen decision is the two lines below it: the
    // tab says which of the four destinations is UNDER whatever happens next,
    // and a link that names both a pane and a tab means "open this terminal,
    // and put me on that tab when I come back out".
    //
    // A filter with no tab of its own lands on the sessions list, because a
    // triage bucket and a search are statements ABOUT that list — applying one
    // while some other tab is showing would filter a screen nobody can see.
    // `t.tab` is already narrowed to the vocabulary by devlink.ts, which
    // matches it with nav's own `isTab` exactly as it matches a sheet name with
    // `isSheetName`. An unrecognised value arrives here as "" and the tab is
    // left alone, which is what every other field in a link does.
    if (t.tab) nav.tab = t.tab;
    else if (t.project) nav.tab = "projects";
    else if (t.triage || t.query) nav.tab = "sessions";

    // A PROJECT IS A DEPTH INSIDE THE PROJECTS TAB, not a screen of its own, so
    // it is written straight onto `nav` beside the tab rather than through
    // `nav.toProject` — that helper forces the tab and clears the picker, which
    // is right for a row being tapped and wrong for a link that may have asked
    // for both a project and the picker over it. Like every other field here it
    // is only applied when the link named one, so a link that names none leaves
    // whatever depth the app was already at alone.
    if (t.project) nav.project = t.project;

    if (t.pane) nav.toTerminal(t.session, t.pane);
    else nav.toSessions();
    // The picker before the sheet, and both after the screen: a picker is a
    // place, a sheet is a modal over one. Neither `toTerminal` nor `toSessions`
    // touches `pick`, which is what lets a link name a pane AND a picker — open
    // this terminal, and land on that picker when you come back out — exactly
    // as it can already name a pane and a tab.
    if (t.pick) nav.toPick(t.pick);
    if (t.sheet) nav.openSheet(t.sheet);
  }


  // THE PHONE'S OWN TOKENS, repainted with the flavor.
  //
  // `appearance` writes the shared token set (theme-runtime's applyFlavor) and
  // knows nothing about the seven names this app adds on top of it — the tab
  // bar's ground, the prose tier, the hairline, the soft chip fills. They are
  // derived from the SAME `Flavor` object, so they are written here, from an
  // effect that re-runs whenever it changes: on boot, on a settings change, and
  // on the daemon answering `GetTheme` after a cached first paint.
  //
  // The static values compiled into app.css cover the gap before this runs, so
  // there is no flash — they are macchiato's, the flavor the design was drawn
  // in, and a mocha default only differs by a few points of ground.
  $effect(() => {
    applyMobileTokens(appearance.flavor);
  });

  // Derived rather than stored, so the banner cannot outlive the connection it
  // describes: a dropped link connection stops rendering it without anything
  // having to remember to clear a flag.
  const devBanner = $derived(pairing.devLinkActive && connection.ready);

  /**
   * Is the bottom bar drawn?
   *
   * ONLY ON THE TAB SHELL, which is exactly the three exclusions the bar needs
   * and no more: the splash is `booting`, the connect form is
   * `screen === "connect"`, and a terminal is `screen === "terminal"` — full
   * screen by design, with its own accessory bar in the space a tab bar would
   * want and every touch inside the pane already spoken for.
   *
   * DELIBERATELY NOT `connection.ready`. "A connection exists" is tempting and
   * wrong: this app stays on the last snapshot behind a banner when the network
   * goes (PLAN.md — an off-network phone must never be shown the pairing
   * screen), so gating on readiness would make the whole bottom bar vanish the
   * moment WiFi dropped, stranding somebody on the Activity tab with no way
   * back to the sessions list. Being on the tab shell at all already means a
   * connection was made; whether it is up right now is what the banner says.
   */
  const showTabBar = $derived(!booting && nav.screen === "sessions");
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
  {:else if nav.tab === "activity"}
    <Activity />
  {:else if nav.tab === "projects"}
    <!-- THE PROJECTS TAB STACKS: a list, a project's detail drilled into from
         it, and a picker opened over that detail. All three are the same tab —
         the bar stays drawn and stays lit — which is why they are a depth here
         rather than three members of `Screen`; nav.svelte.ts's `project`
         comment argues that at length, and `nav.back()` unwinds this stack in
         the same order.

         THE PROJECT IS TESTED FIRST, OUTERMOST, even though the picker is
         deeper. Both pickers read `nav.project` themselves and would ask the
         daemon about "" without it, so a `pick` that somehow arrived without a
         project — a development link naming only one, say — falls back to the
         list rather than to a picker that can only fail. Depth still decides
         between the remaining two. -->
    {#if nav.project === ""}
      <Projects />
    {:else if nav.pick === "prs"}
      <PRPicker />
    {:else if nav.pick === "tickets"}
      <TicketPicker />
    {:else}
      <ProjectDetail />
    {/if}
  {:else if nav.tab === "settings"}
    <Settings />
  {:else}
    <!-- The default arm rather than an explicit `nav.tab === "sessions"` test,
         so a tab this build has not caught up to still renders the list instead
         of a blank screen. Same fail-toward-something rule theme.ts's own
         `displayFor` takes for an unknown state. -->
    <Sessions />
  {/if}
  </div>
  <!-- BELOW the screen container, as a sibling: the bar is chrome, not part of
       any screen, and every screen above it is already `h-full` inside a flex
       column. Drawing it inside would make each of the four views responsible
       for leaving room for it, which is four places to get the home indicator
       wrong instead of one. It pays the bottom inset itself for the same
       reason. -->
  {#if showTabBar}
    <TabBar />
  {/if}
</div>
