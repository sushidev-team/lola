<script lang="ts">
  import { onMount } from "svelte";
  import Icon from "$lib/components/Icon.svelte";
  import { transport } from "@mobile/native";
  import { installAppState } from "@mobile/lib/appstate";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { copy } from "@mobile/lib/copy";
  import { connectGlyph, connectTone, groupCode, noticeWords, SCAN_AGAIN, stateWords } from "@mobile/lib/paircopy";
  import { connection } from "@mobile/lib/connection.svelte";
  import { pairing } from "@mobile/lib/pairing.svelte";
  import { router } from "@mobile/lib/router.svelte";

  // Pair with a Mac: scan, then wait while the person types the code on the Mac
  // (spec §5.2). Every outcome is one glyph, one title, one line and one button.
  // The glyph+title block starts at the same offset (38% of the screen body) in every
  // state, and the call to action sits in a fixed-height slot at the bottom that is
  // reserved even when empty, so nothing jumps between states. The waiting state puts
  // a text Cancel in the slot; a dropped link offers Reconnect, with Pair again as text.
  //
  // Invariants:
  //  - Pairing logic lives in `lib/pairing.svelte.ts`; this file only renders
  //    its view and routes its buttons.
  //  - A denied camera offers Settings only; coming back to the app is what
  //    tries the scan again, since access is granted outside it.
  //  - The camera opens on appear for a scan state only; a notice or a cancelled
  //    scan waits for a tap, so a bad code never loops the scanner.

  onMount(() => {
    const top = router.top;
    pairing.open(top.name === "connect" ? top.state : "scan");
    const offForeground = installAppState(() => {
      if (pairing.notice === "cameraOff") void pairing.scan();
    });
    return () => {
      offForeground();
      pairing.close();
    };
  });

  const words = $derived(pairing.notice ? noticeWords(pairing.notice) : stateWords(pairing.view));
  const waiting = $derived(pairing.view === "code");
  const cameraOff = $derived(pairing.notice === "cameraOff");
  const glyph = $derived(connectGlyph(pairing.view, pairing.notice));
  const tone = $derived(connectTone(glyph));
  // "Scan code" is the first-run button; once a scan ended without a pairing (cancelled, unreadable,
  // unreachable) the same button reads "Scan again".
  let scanEnded = $state(false);
  let wasBusy = false;
  $effect(() => {
    const b = pairing.busy;
    if (wasBusy && !b) scanEnded = true;
    wasBusy = b;
  });
  const dropped = $derived(pairing.view === "dropped" && !pairing.notice);
  const action = $derived.by(() => {
    if (waiting || cameraOff || pairing.view === "no-camera" || dropped) return null;
    if (pairing.view === "scan") return scanEnded || pairing.notice ? SCAN_AGAIN : copy.connect.scan.action;
    return stateWords(pairing.view).action ?? SCAN_AGAIN;
  });

  let reconnecting = $state(false);
  async function reconnect(): Promise<void> {
    if (reconnecting) return;
    reconnecting = true;
    try {
      if (await connection.reconnect()) router.replace({ name: "tab", tab: "inbox" });
    } finally {
      reconnecting = false;
    }
  }

  /** The quiet second action: 17px accent text, 50px tall. */
  const TEXT_BUTTON = "h-[50px] w-full touch-manipulation text-body text-accent active:opacity-60";

  function openSettings(): void {
    void transport.openURL({ url: "app-settings:" }).catch(() => {});
  }
</script>

<main
  class="mx-auto flex h-full w-full max-w-md flex-col px-4 pt-[var(--lola-top-inset,env(safe-area-inset-top,0px))]"
  data-state={pairing.view}
>
  <!-- The icon + title block always starts 38% down; what follows it never moves it. -->
  <div class="relative min-h-0 flex-1">
    <div class="absolute inset-x-0 top-[38%] flex flex-col items-center gap-2 text-center">
      <Icon name={glyph} size={56} class={tone} data-glyph={glyph} />
      <h1 class="text-title font-bold text-ink">{words.title}</h1>
      <p class="text-body font-normal text-subtext">{words.body}</p>

      {#if waiting}
        <p
          class="mt-4 w-full rounded-2xl bg-panel px-4 py-5 font-mono text-code tracking-[0.12em] text-ink tabular-nums"
          aria-label={pairing.code.split("").join(" ")}
          data-testid="pair-code"
        >
          {groupCode(pairing.code)}
        </p>
        <p class="flex items-center gap-2 text-subhead font-normal text-faint" role="status">
          <Icon name="loader-circle" size={16} class="animate-spin motion-reduce:animate-none" />
          <span>{copy.connect.code.status}</span>
        </p>
      {/if}
    </div>
  </div>

  <!-- A fixed slot: reserved even when empty. The first button always lands in the same place. -->
  <div class="flex h-[calc(124px+env(safe-area-inset-bottom,0px))] shrink-0 flex-col gap-2 pb-[calc(env(safe-area-inset-bottom,0px)+16px)]" data-cta-slot>
    {#if waiting}
      <button type="button" class={TEXT_BUTTON} onclick={() => void pairing.rescan()}>{copy.connect.code.cancel}</button>
    {:else if cameraOff}
      <TouchButton variant="primary" prominent onclick={openSettings}>{copy.connect.cameraOff.action}</TouchButton>
    {:else if dropped}
      <TouchButton variant="primary" prominent loading={reconnecting} onclick={() => void reconnect()}>
        {copy.connect.dropped.reconnect}
      </TouchButton>
      <button type="button" class={TEXT_BUTTON} onclick={() => void pairing.rescan()}>{copy.connect.notPaired.action}</button>
    {:else if action}
      <TouchButton variant="primary" prominent loading={pairing.busy} onclick={() => void pairing.rescan()}>
        {action}
      </TouchButton>
    {/if}
  </div>
</main>
