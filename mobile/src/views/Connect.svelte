<script lang="ts">
  import Checkbox from "$lib/components/Checkbox.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { INSECURE_MIN_KEY_LEN, connection } from "@mobile/lib/connection.svelte";
  import { DEFAULT_REMOTE_PORT, classifyHost, type EndpointDraft } from "@mobile/lib/endpoint";
  import { installKeyboardInset } from "@mobile/lib/keyboardinset";
  import type { DevLinkTarget } from "@mobile/lib/devlink";
  import { pairing } from "@mobile/lib/pairing.svelte";
  import {
    pairFailureMessage,
    toDraft,
    type PairNotice,
    type PairPayload,
    type PairSource,
  } from "@mobile/lib/pairpayload";
  import { scanCapability, scanForPairing, type ScanCapability } from "@mobile/lib/scan";
  import { isPersistent } from "@mobile/lib/secretstore";

  // Where the phone is told which Mac to talk to.
  //
  // TWO WAYS IN, ONE WAY THROUGH. A hand-off can arrive from Lola's own camera
  // or, on a build that registers the scheme, from the OS URL router; a human
  // can also type all four values. Every one of them ends at `apply()`, which
  // fills the same `draft`/`key` the form is bound to and calls the same
  // `connection.connect`. That is deliberate: a scanned endpoint that skipped
  // `validateDraft` would be the one input path able to hand the transport
  // something the form would have refused, and a second connect path is a
  // second place for the idle/busy/error handling to drift.
  //
  // WHY A SCAN MAY DIAL AND A ROUTED URL MAY NOT. The user aimed the camera at
  // that square one second ago, so a scan finishing in a connection is the
  // feature. A `link` is whatever some app asked iOS to deliver — PLAN.md's
  // objection to URL-routed pairing is precisely that anybody can send one — so
  // it fills the form, names what it wants to reach, and waits for a tap.
  //
  // `launch` is the THIRD source and it may dial, because it is not the same
  // door: it comes from this process's own launch environment or argv, which
  // only whoever STARTS the process can set — a debugger on a device, `simctl`
  // on a Simulator. Anyone able to do that already owns the machine. It is also
  // the only source an agent has: from iOS 26 the system draws an untappable
  // confirmation over every `simctl openurl`, so a routed URL can fill this
  // form and nothing can submit it. Both development sources wear the banner.
  //
  // THE SCREEN'S OTHER JOB IS THE FAILURE MESSAGE. A connection can fail for
  // four reasons that are indistinguishable at the socket and have completely
  // different fixes — wrong key, wrong pin, not on the network, and iOS local
  // network permission denied. The last one is why this screen matters: iOS
  // reports a denied permission as an ordinary unreachable host, asks exactly
  // once, never asks again, and offers no API to check. Without a screen that
  // names it, the user checks their WiFi forever. `diagnose` does that
  // classification, `scan`/`pairpayload` do the hand-off's; this renders both.

  let { onconnected }: { onconnected: (target?: DevLinkTarget | null) => void } = $props();

  let draft = $state<EndpointDraft>({ host: "", port: "", spkiPin: "" });
  let key = $state("");
  let remember = $state(true);
  let restored = $state(false);
  let showKey = $state(false);

  /** null while the camera is still being asked about. See the render guard. */
  let scan = $state<ScanCapability | null>(null);
  let scanning = $state(false);
  /** A scan or hand-off problem. Separate from the connect diagnosis below. */
  let notice = $state<PairNotice | null>(null);
  /** Typed entry, open when there is no scanner to offer instead. */
  let manual = $state(false);
  /** Soft-keyboard height, paid back as padding so a field can scroll clear. */
  let kbInset = $state(0);
  /**
   * Whether what is in the form right now arrived from a `lola-dev://` URL.
   *
   * Carried to the moment of connecting rather than set by the link itself,
   * because the plugin's banner obligation is about a CONNECTION that arrived
   * that way — and the link never connects on its own, so the flag has to
   * survive until the human taps Connect.
   */
  let fromDevLink = $state(false);
  /**
   * Where a development link asked the app to land once connected.
   *
   * Plain `let` for the same reason `handoff` is: nothing renders it, it must
   * survive from the moment a link arrives to the moment a connection succeeds
   * — which for a routed link is however long the human takes to tap Connect —
   * and it must not re-run the effects that read the form.
   */
  let devLinkTargetPending: DevLinkTarget | null = null;
  /**
   * Whether a scan or a link has filled this form.
   *
   * Guards the remembered-endpoint restore below, which resolves asynchronously
   * and would otherwise overwrite a hand-off that arrived while it was in
   * flight. Plain `let`, not `$state`: nothing renders it, and it must be
   * readable by the restore effect WITHOUT that effect re-running when a
   * payload lands.
   */
  let handoff = false;

  const problems = $derived(connection.problems);
  const problemFor = (field: string) => problems.find((p) => p.field === field)?.message ?? "";

  const d = $derived(connection.diagnosis);
  // "closed" without a reason is the state before anything was tried, which must
  // not be reported as a failure — a cold start showing a red banner reads as a
  // broken app.
  const tried = $derived(connection.phase === "closed" && (!!connection.error || !!connection.refusal));
  const failure = $derived<PairNotice | null>(
    tried
      ? { tone: d.kind === "unreachable" ? "warn" : "bad", title: d.title, detail: d.detail, hint: d.hint }
      : null,
  );

  const kind = $derived(classifyHost(draft.host));

  /** Typing over a link's values makes them the human's, so drop the label. */
  function edited(): void {
    fromDevLink = false;
    devLinkTargetPending = null;
    notice = null;
  }

  // Ask the camera once. The answer is a property of the hardware, and asking
  // it up front is what lets a Simulator — which has no camera and cannot be
  // given one — show no Scan button at all rather than a control that looks
  // live and does nothing.
  $effect(() => {
    void (async () => {
      const c = await scanCapability();
      scan = c;
      if (!c.available) manual = true;
    })();
  });

  $effect(() => {
    void (async () => {
      const prev = await connection.restore();
      // A HAND-OFF OUTRANKS WHAT WAS REMEMBERED, and the check has to be here —
      // after the await — rather than at the top. Both of these fill the same
      // two fields, and on a cold launch they race: the OS delivers a URL while
      // the WebView is still loading, so the payload can land first and this
      // resolves afterwards and overwrites it. That is not hypothetical. The
      // key comes back EMPTY wherever there is no native secret store — a
      // browser dev session, or a plugin binary older than this bundle — which
      // means the clobber replaces a working hand-off with a host and a blank
      // credential: a link that fills the form correctly and then, one tick
      // later, silently empties the one field nobody can guess. On a current
      // device build the key does come back, and the race is then merely
      // between two credentials rather than between one and nothing; the guard
      // is right either way, because a hand-off names the daemon the user is
      // holding a screen in front of.
      if (!prev || handoff) return;
      draft = prev.draft;
      key = prev.key;
      restored = true;
      manual = true; // show what is about to be dialled rather than hiding it
    })();
  });

  // The hand-off inbox. A scan drops its payload here too, so the scanner and
  // the URL router converge before either of them reaches `apply`.
  $effect(() => {
    const offer = pairing.pending;
    if (!offer) return;
    pairing.pending = null;
    void apply(offer.payload, offer.source, offer.target ?? null);
  });

  // capacitor.config.ts sets Keyboard.resize: None, so the WebView keeps its
  // full height and the raised keyboard simply covers the lower fields — which
  // on this screen is the access key, the last thing anybody types. Paying the
  // height back as bottom padding gives the scroll container somewhere to go.
  $effect(() => installKeyboardInset((px) => (kbInset = px)));

  /** The one place a payload becomes a connection attempt. */
  async function apply(
    p: PairPayload,
    source: PairSource,
    target: DevLinkTarget | null = null,
  ): Promise<void> {
    handoff = true;
    devLinkTargetPending = target;
    const next = toDraft(p);
    draft = next.draft;
    key = next.key;
    manual = true;
    connection.problems = [];

    // Both development routes are development connections and both must wear
    // the banner. Only the door they came through differs.
    fromDevLink = source === "link" || source === "launch";

    if (source === "scan" || source === "launch") {
      notice = null;
      // Always remember a hand-off: scan-once-then-retype-anyway is the exact
      // failure this feature exists to remove.
      if (await connection.connect(next.draft, next.key, true, next.alternates)) {
        // A scan is an ordinary connection and wears no banner; a launch link
        // is a development one and wears it for as long as it is up.
        pairing.devLinkActive = source === "launch";
        onconnected(devLinkTargetPending);
      }
      return;
    }

    // A URL from the OS router. Show it, do not dial it — anybody on the device
    // can send one, and that is precisely what the banner and this pause exist
    // for. `launch` above is a different door: setting this process's own
    // environment means having started it.
    notice = {
      tone: "warn",
      title: "A link wants to connect this phone",
      detail: `It is pointing at ${next.draft.host || "an address"}. Nothing has been sent yet.`,
      hint: "Check that this is your Mac, then tap Connect.",
    };
  }

  async function runScan(): Promise<void> {
    if (scanning) return;
    scanning = true;
    notice = null;
    try {
      const r = await scanForPairing();
      if (!r.ok) {
        notice = r.notice; // null for a cancel, which deserves no message
        // A scanner that turns out not to exist should stop being offered.
        if (r.outcome.kind === "unavailable") {
          scan = { available: false, reason: r.outcome.reason };
          manual = true;
        }
        return;
      }
      if (!r.result.ok) {
        notice = pairFailureMessage(r.result);
        return;
      }
      pairing.offer(r.result.payload, "scan");
    } finally {
      scanning = false;
    }
  }

  async function submit(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    notice = null;
    if (await connection.connect(draft, key, remember)) {
      // A link fills the form and waits here, so this is where a dev-URL
      // connection is actually established and where it gets labelled.
      pairing.devLinkActive = fromDevLink;
      onconnected(devLinkTargetPending);
    }
  }

  const inputCls =
    "w-full rounded-md border border-edge bg-canvas px-3 py-3 text-base text-ink outline-none " +
    "focus:border-accent placeholder:text-placeholder";

  // The screen's one action, at the weight a phone's only action needs. The
  // `primary` variant's `bg-accent-fill` is the accent tinted deep into the
  // canvas — right for a 28px chip among a dozen desktop controls, and at 48pt
  // full-width it reads as a DISABLED button. `accent` + `on-accent` is the
  // sanctioned pairing for a filled surface (both are measured tokens; no new
  // colour is introduced). The trailing `!` is not optional: a plain `bg-accent`
  // has the same specificity as the variant's own and the winner would be
  // decided by Tailwind's order in the compiled sheet.
  const FILLED = "bg-accent! text-on-accent! enabled:hover:bg-accent!";
</script>

{#snippet banner(n: PairNotice)}
  <div
    class="flex flex-col gap-1.5 rounded-md border px-4 py-3
           {n.tone === 'warn' ? 'border-warn/40 bg-warn/10' : 'border-bad/40 bg-bad/10'}"
    role="status"
  >
    <span class="font-medium {n.tone === 'warn' ? 'text-warn' : 'text-bad'}">{n.title}</span>
    <span class="copy text-sm text-ink">{n.detail}</span>
    {#if n.hint}<span class="copy text-sm text-faint">{n.hint}</span>{/if}
  </div>
{/snippet}

<!-- Both insets are inline rather than `pt-safe-t`/`pb-safe-b` utilities, and
     for two different reasons. The TOP goes through --lola-top-inset so the
     dev-link banner can pay it instead when it is up — see the note in app.css
     for why that indirection cannot be folded into the spacing token. The
     BOTTOM is arithmetic: the home-indicator inset plus whatever the soft
     keyboard is currently covering. -->
<div
  class="h-full overflow-y-auto overscroll-contain bg-canvas"
  style="padding-top: var(--lola-top-inset, env(safe-area-inset-top, 0px));
         padding-bottom: calc(env(safe-area-inset-bottom) + {kbInset}px + 1.5rem)"
>
  <form class="mx-auto flex w-full max-w-md flex-col gap-6 px-5 pt-8" onsubmit={submit}>
    <header class="flex flex-col gap-2">
      <!-- text-2xl is the phone's large-title step (see app.css). At text-xl
           this read as a web heading rather than an iOS screen title. -->
      <h1 class="text-2xl font-medium text-ink">Connect to lola</h1>
      <p class="copy text-sm text-faint">
        This phone talks straight to your Mac. No relay and no account — if it cannot reach that
        Mac, nothing here can.
      </p>
    </header>

    {#if scan?.available}
      <div class="flex w-full flex-col gap-2">
        <TouchButton
          variant="primary"
          wide
          type="button"
          class={FILLED}
          loading={scanning}
          onclick={runScan}
        >
          {scanning ? "Scanning…" : "Scan the code"}
        </TouchButton>
        <p class="text-center text-sm text-faint">
          On the Mac, open Lola’s settings and pick Remote.
        </p>
      </div>
    {/if}

    <!-- BOTH banners live here, above the form, and the failure one used to
         live at the very bottom. That put the app's most important sentence
         after the Connect button, below the fold on a 393pt screen: the first
         real screenshot of a wrong access key showed "Nothing answered. The
         address may be wrong, or the" and cut, with the line naming the iOS
         local-network permission entirely off-screen. A message about the
         fields belongs next to the fields. -->
    {#if failure}{@render banner(failure)}{/if}
    {#if notice}{@render banner(notice)}{/if}

    {#if scan && !manual}
      <!-- Typed entry stays a full peer, one tap behind the camera. -->
      <TouchButton variant="secondary" wide type="button" onclick={() => (manual = true)}>
        Enter the details instead
      </TouchButton>
    {/if}

    {#if manual}
      {#if scan?.available}
        <div class="flex w-full items-center gap-3" aria-hidden="true">
          <span class="h-px flex-1 bg-edge"></span>
          <span class="text-sm text-faint">or type them in</span>
          <span class="h-px flex-1 bg-edge"></span>
        </div>
      {/if}

      {#if restored && !key && !isPersistent()}
        <!-- The one field nobody can guess is the one that is always empty on a
             cold launch, and nothing on screen said so: the address, port and
             pin all come back, the key does not, and a user who did not build
             this app has no way to know that is by design rather than a bug. -->
        <p class="copy text-sm text-faint">
          This Mac was remembered. Only the access key is missing — it is never stored.
        </p>
      {/if}

      <!-- One card, two groups: where the Mac is, then how to prove we may
           talk to it. Four identically-weighted rectangles in a row was the
           screen's biggest legibility problem — nothing said which pair was
           an address and which pair was a credential. -->
      <div class="panel flex w-full flex-col gap-5 p-4">
        <label class="flex flex-col gap-1.5">
          <span class="text-sm text-faint">Address</span>
          <input
            class={inputCls}
            type="text"
            inputmode="url"
            autocapitalize="none"
            autocorrect="off"
            spellcheck="false"
            placeholder="192.168.1.5 or marsmac.local"
            bind:value={draft.host}
            oninput={edited}
          />
          {#if problemFor("host")}
            <span class="text-sm text-bad">{problemFor("host")}</span>
          {:else if kind === "loopback"}
            <span class="copy text-sm text-faint">
              A loopback address only works from the Simulator, or through an SSH forward.
            </span>
          {/if}
        </label>

        <label class="flex flex-col gap-1.5">
          <span class="text-sm text-faint">Port</span>
          <!-- Narrow on purpose: a four-digit number in a full-width box reads
               as one more field for prose. The `!` is not decoration — `inputCls`
               carries `w-full`, a plain `w-32` has exactly the same specificity,
               and the first build of this shipped a full-width port box because
               Tailwind's sheet order picked the winner. -->
          <input
            class="{inputCls} num w-32!"
            type="text"
            inputmode="numeric"
            placeholder={String(DEFAULT_REMOTE_PORT)}
            bind:value={draft.port}
            oninput={edited}
          />
          {#if problemFor("port")}<span class="text-sm text-bad">{problemFor("port")}</span>{/if}
        </label>

        <div class="h-px bg-edge"></div>

        <label class="flex flex-col gap-1.5">
          <span class="text-sm text-faint">Certificate pin</span>
          <!-- A TEXTAREA, not an input, and that is the whole fix. The pin is
               44 monospace characters; in a single-line box on a 393pt screen
               six of them are cut off the right edge with no ellipsis and no
               wrap — and the six that vanish include the trailing "=" the
               placeholder tells you to check for. It is also the one value on
               this screen whose correctness cannot be verified any other way,
               and it is a hash of a public key rather than a secret, so showing
               all of it costs nothing. Newlines are stripped on the way in: a
               paste out of a terminal routinely carries one, and the daemon
               compares the string exactly. -->
          <textarea
            class="{inputCls} resize-none break-all font-mono text-sm"
            rows="2"
            autocapitalize="none"
            spellcheck="false"
            placeholder="44 characters ending in ="
            value={draft.spkiPin}
            oninput={(e) => {
              draft.spkiPin = e.currentTarget.value.replace(/\s+/g, "");
              edited();
            }}
            onkeydown={(e) => {
              if (e.key === "Enter") e.preventDefault();
            }}
          ></textarea>
          {#if problemFor("pin")}
            <span class="text-sm text-bad">{problemFor("pin")}</span>
          {:else if !draft.spkiPin}
            <!-- Only while the field is empty. Help that stays after the job is
                 done is just clutter, and this screen had 90 words of it. -->
            <span class="copy text-sm text-faint">
              The Mac prints this when the listener starts, as “SPKI pin …”. It is a hash of a
              public key, not a secret.
            </span>
          {/if}
        </label>

        <label class="flex flex-col gap-1.5">
          <span class="flex items-center justify-between gap-3">
            <span class="text-sm text-faint">Access key</span>
            <!-- Was an underlined 20pt hyperlink: the only action on the screen
                 that was not a button and not reachable by a thumb. -->
            <TouchButton
              variant="ghost"
              type="button"
              class="-my-2 text-accent-ink!"
              onclick={() => (showKey = !showKey)}
            >
              {showKey ? "Hide" : "Show"}
            </TouchButton>
          </span>
          <!-- type=password until asked, because a shared bearer secret typed on
               a phone is typed in public more often than one typed at a desk. -->
          <input
            class="{inputCls} font-mono text-sm"
            type={showKey ? "text" : "password"}
            autocapitalize="none"
            autocorrect="off"
            spellcheck="false"
            bind:value={key}
            oninput={edited}
          />
          <!-- No second row of dots under a field that is already showing
               dots. The old `maskKey(key)` echo rendered 24 more bullets
               directly beneath a type=password input, which conveyed nothing
               the control did not and read as a rendering artefact. -->
          {#if problemFor("key")}
            <span class="text-sm text-bad">{problemFor("key")}</span>
          {:else if !key}
            <span class="copy text-sm text-faint">
              At least {INSECURE_MIN_KEY_LEN} characters. Run <span class="font-mono">make
                mobile-info</span> on the Mac, or open Lola’s settings there and pick Remote.
            </span>
          {/if}
        </label>
      </div>

      <label class="tap-row flex w-full items-center gap-3">
        <Checkbox bind:checked={remember} />
        <!-- The caption used to name only the EXCEPTION ("the key is kept only
             until the app closes"), never the rule, so it read as a
             contradiction of its own label and left it unclear whether ticking
             the box did anything at all. -->
        <span class="flex flex-col">
          <span class="text-ink">Remember this Mac</span>
          <span class="copy text-sm text-faint">
            {#if isPersistent()}
              Keeps its address, port, certificate pin and access key.
            {:else}
              Keeps its address, port and certificate pin. The access key is never stored and has
              to be typed again each launch.
            {/if}
          </span>
        </span>
      </label>

      <TouchButton
        variant="primary"
        wide
        type="submit"
        class={FILLED}
        loading={connection.busy}
      >
        {connection.busy ? "Connecting…" : "Connect"}
      </TouchButton>
    {/if}
  </form>
</div>
