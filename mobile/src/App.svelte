<script lang="ts" module>
  import type { Component } from "svelte";
  import type { Route } from "@mobile/lib/router.svelte";

  type Loaded = { default: Component };
  type Loader = () => Promise<Loaded>;

  // One lazy chunk per screen, from a FIXED map: a D1 item replaces the file at
  // its path and never edits this one. Route to screen is a pure function.
  const SCREENS = {
    connect: () => import("./screens/Connect.svelte"),
    inbox: () => import("./screens/Inbox.svelte"),
    projects: () => import("./screens/Projects.svelte"),
    projectDetail: () => import("./screens/ProjectDetail.svelte"),
    prPicker: () => import("./screens/PRPicker.svelte"),
    ticketPicker: () => import("./screens/TicketPicker.svelte"),
    git: () => import("./screens/Git.svelte"),
    settings: () => import("./screens/Settings.svelte"),
    session: () => import("./screens/session/Session.svelte"),
    bridgeGuard: () => import("./screens/dev/BridgeGuard.svelte"),
  } as const satisfies Record<string, Loader>;
  type ScreenKey = keyof typeof SCREENS;

  export function screenKey(r: Route): ScreenKey {
    switch (r.name) {
      case "connect":
        return "connect";
      case "session":
        return "session";
      case "dev":
        return "bridgeGuard";
      case "tab":
        if (r.tab === "projects") {
          if (!r.project) return "projects";
          if (r.pick === "git" || r.pick === "graph") return "git";
          return r.pick === "prs" ? "prPicker" : r.pick === "tickets" ? "ticketPicker" : "projectDetail";
        }
        return r.tab;
    }
  }

  // A resolved import is kept, so a screen that re-renders awaits the same
  // promise and never flashes its pending state.
  const screenCache = new Map<ScreenKey, Promise<Loaded>>();
  function screen(key: ScreenKey): Promise<Loaded> {
    let p = screenCache.get(key);
    if (!p) {
      p = SCREENS[key]();
      screenCache.set(key, p);
    }
    return p;
  }

  // Sheets are `sheets/<name>.svelte`, resolved by name; a sheet whose file has
  // not landed renders nothing. Each sheet mounts its own `Sheet` (it owns the
  // title and the detent) and closes through `router.close()`.
  const sheetLoaders: Record<string, Loader> = Object.fromEntries(
    Object.entries(
      import.meta.glob<Loaded>(["./sheets/*.svelte", "!./sheets/*.test.svelte"]),
    ).map(([path, load]) => [path.replace(/^.*\/([^/]+)\.svelte$/, "$1"), load]),
  );
  const sheetCache = new Map<string, Promise<Loaded>>();
  function sheet(name: string): Promise<Loaded> | null {
    const load = sheetLoaders[name];
    if (!load) return null;
    let p = sheetCache.get(name);
    if (!p) {
      p = load();
      sheetCache.set(name, p);
    }
    return p;
  }

  /** Debug-only routes exist everywhere but a production build. */
  const DEBUG_BUILD = import.meta.env.MODE !== "production";
</script>

<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { Spring } from "svelte/motion";
  import ConfirmDialog from "$lib/components/ConfirmDialog.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { nav as deskNav } from "$lib/nav.svelte";
  import { appearance } from "$lib/theme-runtime.svelte";
  import Banner, { connectionBanner } from "@mobile/lib/components/Banner.svelte";
  import FlashToast from "@mobile/lib/components/FlashToast.svelte";
  import TabBar from "@mobile/lib/components/TabBar.svelte";
  import { installAppState } from "@mobile/lib/appstate";
  import { connection } from "@mobile/lib/connection.svelte";
  import { copy } from "@mobile/lib/copy";
  import { applyDevLink, installDevLink } from "@mobile/lib/devlink";
  import { applyRoute, followFocus, sheetClosed, sheetForOverlay } from "@mobile/lib/desknav";
  import { installDynamicType } from "@mobile/lib/dynamictype";
  import { attachAttention } from "@mobile/lib/attention";
  import { haptic } from "@mobile/lib/haptics";
  import { installKeyboardInset } from "@mobile/lib/keyboardinset";
  import { applyMobileTokens, MOTION, reducedMotion } from "@mobile/lib/mobiletokens";
  import { router, type ConnectState } from "@mobile/lib/router.svelte";

  // The app shell: the router outlet, the banners above it, the tab bar below it
  // and the sheet host over it (spec §4.2, §5.1, §6.4).
  //
  // Invariants:
  //  - Losing the connection never routes. Only `not_paired` and an explicit
  //    unpair put Connect on the stack; everything else is a banner over the
  //    screen the person was on. `key_changed` keeps the screen too.
  //  - Nothing here identifies or authenticates a daemon: the connection store
  //    holds public ids and names, the pairing record is native.
  //  - The boot holds a blank ground only until the paired list is read (the
  //    Keychain answers in milliseconds), so a paired phone never flashes
  //    Connect; the dial itself happens behind the Inbox and its banner.
  //  - The first banner pays the status-bar inset and the screens below it are
  //    told not to (`--lola-top-inset`).

  let booting = $state(true);
  let devLinked = $state(false);

  function toConnect(state: ConnectState): void {
    router.stack = [{ name: "connect", state }];
    router.sheet = "";
  }

  onMount(() => {
    const offType = installDynamicType();
    const offKeyboard = installKeyboardInset((px) => {
      document.documentElement.style.setProperty(
        "--kb",
        `max(0px, calc(${px}px - env(safe-area-inset-bottom, 0px)))`,
      );
      document.documentElement.toggleAttribute("data-kb", px > 0);
    });
    // Registered here and not on a screen: the plugin keeps a pending link until
    // something consumes it, and a cold launch delivers it before any screen mounts.
    const offDevLink = installDevLink(
      (link) => {
        devLinked = true;
        applyDevLink(router, link);
      },
      () => connection.daemons.length > 0,
    );
    // The plugin closes the socket on the way to the background; the connection
    // applies its own gates, so this is unconditional.
    const offAppState = installAppState(() => void connection.reconnect());
    // A tap on the wrist when a session newly needs the person (lib/attention.ts).
    const offAttention = attachAttention();

    // WebKit scrolls the document to reveal a caret even with scrolling disabled.
    const onFocusIn = (e: FocusEvent): void => {
      if ((e.target as HTMLElement | null)?.closest?.("[data-phone-desk] textarea")) {
        requestAnimationFrame(() => window.scrollTo(0, 0));
      }
    };
    document.addEventListener("focusin", onFocusIn);

    void (async () => {
      const paired = await connection.refresh();
      booting = false;
      if (paired.length === 0) toConnect("scan");
      else void connection.boot();
    })();

    return () => {
      document.removeEventListener("focusin", onFocusIn);
      offAppState();
      offAttention();
      offDevLink();
      offKeyboard();
      offType();
    };
  });

  // The phone's own tokens follow the flavor.
  $effect(() => {
    applyMobileTokens(appearance.flavor);
  });

  // Revocation is the one connection state with a screen: the Mac removed this
  // phone. Every other state is a banner.
  $effect(() => {
    if (connection.diagnosis.kind === "not_paired" && untrack(() => router.top.name) !== "connect") {
      haptic("notPaired");
      toConnect("not-paired");
    }
  });

  // An explicit unpair of the last Mac leads to Connect. Only the 1 -> 0
  // transition counts: a pairing that has finished but not yet refreshed the
  // list must not be bounced back.
  let pairedCount = 0;
  $effect(() => {
    const n = connection.daemons.length;
    if (pairedCount > 0 && n === 0 && untrack(() => router.top.name) !== "connect") toConnect("scan");
    pairedCount = n;
  });

  // Desktop components read the desktop nav singleton; the phone writes it from
  // its route and reads two signals back (spec §4.2).
  $effect(() => {
    applyRoute(router.top, deskNav);
  });
  $effect(() => {
    const next = followFocus(router.top, deskNav);
    if (next) untrack(() => router.push(next));
  });
  $effect(() => {
    const s = sheetForOverlay(deskNav);
    if (s !== "") untrack(() => router.open(s));
  });
  let lastSheet = router.sheet;
  $effect(() => {
    const now = router.sheet;
    if (lastSheet !== now) sheetClosed(deskNav, lastSheet);
    lastSheet = now;
  });

  const connBanner = $derived(
    router.top.name === "connect" ? null : connectionBanner(connection.diagnosis, connection.label),
  );
  // The Inbox draws the connection banner itself, below its large title (spec §5.2);
  // drawing it here too would show it twice. The dev-link banner stays here.
  const onInbox = $derived(router.top.name === "tab" && router.top.tab === "inbox");
  const banner = $derived(onInbox ? null : connBanner);
  const devBanner = $derived(devLinked && connection.ready);
  // A sheet that has a body to show; everything behind it is inert meanwhile.
  const sheetOpen = $derived(router.sheet !== "" && sheetLoaders[router.sheet] !== undefined);
  const bannerCount = $derived((devBanner ? 1 : 0) + (banner ? 1 : 0));

  // Haptics fire on the transition into a state, never on a re-render of it.
  const bannerId = $derived(connBanner?.id ?? "");
  $effect(() => {
    if (bannerId === "key_changed") haptic("refusal");
  });
  const confirming = $derived(confirm.request !== null);
  $effect(() => {
    if (confirming) haptic("refusal");
  });

  function bannerAction(intent: "pair" | "details" | undefined): void {
    if (intent === "pair") toConnect("scan");
    else if (intent === "details") router.open("connection");
  }

  // --- Push and pop (spec §6.4) --------------------------------------------
  // The pushed screen sits over the one beneath. Its position is one spring,
  // parked off screen while nothing is pushed; an edge swipe drives it with the
  // finger and releases through the same spring. A pop that is not a swipe
  // removes the layer at once.

  const reduced = reducedMotion();
  const stacked = $derived(router.stack.length > 1);
  const under = $derived(stacked ? router.stack[router.stack.length - 2] : router.stack[0]);
  const over = $derived(stacked ? router.top : null);
  // Never onto Connect (`router.back()` refuses it), and not on the Terminal
  // segment, which owns its touches.
  const swipeable = $derived(
    over !== null && under.name !== "connect" && !(over.name === "session" && over.seg === "terminal"),
  );

  // The outlet is the full window width on a phone.
  const parked = (): number => window.innerWidth;
  const x = new Spring(window.innerWidth, MOTION.push);

  $effect(() => {
    if (stacked) {
      if (reduced) void x.set(0, { instant: true });
      else x.target = 0;
    } else {
      void x.set(parked(), { instant: true });
    }
  });

  let swipe: { id: number; startX: number; lastX: number; lastT: number; v: number } | null = null;

  function swipeDown(e: PointerEvent): void {
    if (!swipeable || e.clientX > MOTION.edgeSwipeStartPt) return;
    swipe = { id: e.pointerId, startX: e.clientX, lastX: e.clientX, lastT: e.timeStamp, v: 0 };
    (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
  }

  function swipeMove(e: PointerEvent): void {
    if (!swipe || e.pointerId !== swipe.id) return;
    const dt = e.timeStamp - swipe.lastT;
    if (dt >= 8) {
      swipe.v = ((e.clientX - swipe.lastX) / dt) * 1000;
      swipe.lastX = e.clientX;
      swipe.lastT = e.timeStamp;
    }
    void x.set(Math.max(0, e.clientX - swipe.startX), { instant: true });
  }

  async function swipeUp(e: PointerEvent): Promise<void> {
    if (!swipe || e.pointerId !== swipe.id) return;
    const { v } = swipe;
    swipe = null;
    const w = parked();
    if (x.current / w > MOTION.popFraction || v > MOTION.popVelocity) {
      if (!reduced) await x.set(w);
      router.back();
    } else {
      x.target = 0;
    }
  }
</script>

<!-- `fixed inset-0`: the root IS the window, so no 1-2px strip of the WebView's own ground shows at an
     edge. `data-phone-app` scopes the phone-only restyles in app.css (the confirm dialog as an action
     sheet). While a sheet is open everything behind it is `inert`, so VoiceOver cannot leave the
     dialog; the sheet, the toast and the confirm dialog are siblings OUTSIDE that wrapper. -->
<div data-phone-app data-frame-clip class="fixed inset-0 flex flex-col overflow-hidden bg-canvas text-ink">
  {#if booting}
    <div class="min-h-0 flex-1" aria-busy="true"></div>
  {:else}
    <div class="contents" inert={sheetOpen} data-screens>
    {#if devBanner}
      <Banner tone="warn" body={copy.banner.devLink.label} inset />
    {/if}
    {#if banner}
      <Banner
        tone={banner.tone}
        body={banner.body}
        action={banner.action}
        role={banner.id === "key_changed" ? "alert" : "status"}
        inset={!devBanner}
        onaction={() => bannerAction(banner.intent)}
      />
    {/if}

    {#snippet outlet(route: Route)}
      {#if route.name !== "dev" || DEBUG_BUILD}
        {#await screen(screenKey(route)) then m}
          {@const Screen = m.default}
          {#key route.name === "tab" && screenKey(route) === "git" ? route.project : undefined}
            <Screen />
          {/key}
        {/await}
      {/if}
    {/snippet}

    <div
      data-frame-clip
      class="relative min-h-0 flex-1 overflow-hidden"
      style={bannerCount > 0 ? "--lola-top-inset: 0px" : ""}
    >
      <div class="absolute inset-0" inert={stacked} aria-hidden={stacked}>
        {@render outlet(under)}
      </div>
      {#if over}
        <div
          class="absolute inset-0 bg-canvas {reduced ? 'layer-fade' : ''}"
          style="transform: translateX({reduced ? 0 : x.current}px)"
        >
          {@render outlet(over)}
          {#if swipeable}
            <div
              class="absolute inset-y-0 left-0 z-10 w-5 touch-none"
              data-edge-swipe
              role="presentation"
              onpointerdown={swipeDown}
              onpointermove={swipeMove}
              onpointerup={swipeUp}
              onpointercancel={swipeUp}
            ></div>
          {/if}
        </div>
      {/if}
    </div>

    {#if router.top.name === "tab"}
      <TabBar />
    {/if}
    </div>

    {#if router.sheet !== ""}
      {@const loading = sheet(router.sheet)}
      {#if loading}
        {#await loading then m}
          {@const SheetBody = m.default}
          <SheetBody />
        {/await}
      {/if}
    {/if}
  {/if}

  <FlashToast />
  <ConfirmDialog />
</div>

<style>
  .layer-fade {
    animation: layer-fade 150ms ease-out;
  }
  @keyframes layer-fade {
    from {
      opacity: 0;
    }
  }
</style>
