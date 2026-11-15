<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import Modal from "$lib/components/Modal.svelte";
  import Button from "$lib/components/Button.svelte";
  import QRCode from "$lib/components/QRCode.svelte";
  import TierToggles from "./TierToggles.svelte";
  import { PairSession, phones, DEFAULT_PAIR_TIERS } from "$lib/phones.svelte";
  import { toMillis } from "$lib/time";

  let { onClose }: { onClose: () => void } = $props();

  // One session per code: "New code" replaces it, and the old one is closed
  // (which rejects its pairing) before the new Begin goes out.
  let session = $state(new PairSession());
  let code = $state("");
  let tiers = $state<string[]>([...DEFAULT_PAIR_TIERS]);
  let now = $state(Date.now());

  const confirming = $derived(session.state === "confirming");
  const digits = $derived(/^\d{6}$/.test(code));
  const secondsLeft = $derived(
    session.expiresAt ? Math.max(0, Math.ceil((toMillis(session.expiresAt) - now) / 1000)) : 0,
  );
  const replaced = $derived(
    phones.devices.find((d) => d.id === session.status?.replaces)?.name ?? "an existing phone",
  );

  const ENDED: Record<string, string> = {
    rejected: "Phone rejected.",
    expired: "Code expired.",
    cancelled: "Pairing cancelled.",
  };
  const FAILED: Record<string, string> = {
    too_many_attempts: "Too many wrong codes.",
    code_mismatch: "The code did not match.",
    phone_left: "The phone disconnected.",
    registry: "Could not save the phone.",
    full: "Too many paired phones.",
  };
  const endedLine = $derived(
    session.state === "failed"
      ? (FAILED[session.status?.reason ?? ""] ?? "Pairing failed.")
      : (ENDED[session.state] ?? ""),
  );

  onMount(() => {
    void session.begin();
    const tick = setInterval(() => (now = Date.now()), 1000);
    return () => clearInterval(tick);
  });
  onDestroy(() => void session.close());

  // A paired phone refreshes the list and closes the dialog after a beat.
  $effect(() => {
    if (session.state !== "paired") return;
    void phones.refresh();
    const t = setTimeout(onClose, 1000);
    return () => clearTimeout(t);
  });

  async function newCode() {
    const old = session;
    session = new PairSession();
    code = "";
    void old.close();
    await session.begin();
  }

  function submit() {
    if (confirming && digits && !session.busy) void session.confirm(code, tiers);
  }

  // Every way out (✕, backdrop, Close, Reject) refuses a pairing still open.
  function dismiss() {
    void session.close();
    onClose();
  }
</script>

<Modal title="Pair a phone" width="420px" onClose={dismiss}>
  <div class="grid gap-4">
    {#if session.problem}
      <p class="text-bad">{session.problem}</p>
    {:else if !session.started}
      <p class="text-faint">Opening…</p>
    {:else if session.state === "waiting"}
      <div class="grid justify-items-center gap-3">
        <QRCode value={session.payload} size={220} label="Pairing code" />
        <p class="text-faint">Scan with Lola on your phone.</p>
        {#if secondsLeft > 0}
          <p class="text-sm text-faint">Expires in {Math.floor(secondsLeft / 60)}:{String(secondsLeft % 60).padStart(2, "0")}</p>
        {/if}
      </div>
    {:else if confirming}
      <form class="grid gap-4" onsubmit={(e) => { e.preventDefault(); submit(); }}>
        <div class="flex flex-wrap items-center gap-2">
          <span class="text-lg text-ink">{session.status?.deviceName}</span>
          {#if session.status?.nameClash || session.status?.replaces}
            <span class="rounded border border-warn/40 bg-warn/10 px-1.5 text-sm text-warn">New device</span>
          {/if}
          {#if session.status?.via === "relay"}
            <span class="text-sm text-faint">via relay</span>
          {/if}
        </div>
        {#if session.status?.replaces}
          <p class="text-sm text-faint">Replaces {replaced}</p>
        {/if}
        <label class="grid gap-1.5">
          <span class="text-faint">Type the code shown on the phone</span>
          <input
            class="w-full rounded border border-edge bg-canvas px-2 py-1.5 font-mono text-lg tracking-widest text-ink"
            inputmode="numeric"
            autocomplete="off"
            maxlength="6"
            bind:value={code}
            oninput={() => (code = code.replace(/\D/g, "").slice(0, 6))} />
        </label>
        <TierToggles value={tiers} onchange={(next) => (tiers = next)} />
        {#if session.error}
          <p class="text-bad">{session.error}</p>
        {/if}
        {#if session.status?.triesLeft !== undefined}
          <p class="text-sm text-faint">{session.status.triesLeft} tries left</p>
        {/if}
        <div class="flex justify-end gap-2">
          <Button variant="secondary" size="md" onclick={dismiss}>Reject</Button>
          <Button variant="primary" size="md" type="submit" disabled={!digits || session.busy} loading={session.busy}>Confirm</Button>
        </div>
      </form>
    {:else if session.state === "paired"}
      <p class="text-ink">Paired {session.status?.device?.name ?? session.status?.deviceName}.</p>
    {:else}
      <p class="text-ink">{endedLine}</p>
      <div class="flex justify-end gap-2">
        {#if session.state === "expired"}
          <Button variant="secondary" size="md" onclick={newCode}>New code</Button>
        {/if}
        <Button variant="secondary" size="md" onclick={dismiss}>Close</Button>
      </div>
    {/if}
    {#if session.problem}
      <div class="flex justify-end"><Button variant="secondary" size="md" onclick={dismiss}>Close</Button></div>
    {/if}
  </div>
</Modal>
