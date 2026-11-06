<script lang="ts">
  // The third axis beside the agent pill and the delivery badge: which host, and the
  // one fact about it most worth knowing. Every word is a daemon fact; the agent's
  // headline lives on the activity line, behind its ring.
  //
  // It is a click target rather than a <button>, because a kanban card already IS one
  // and a nested button is not parseable. Approval does not happen here: the card's
  // rules need the whole dock.
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import { chipFor, hostName, type SSHSessionInfo } from "$lib/ssh";
  import SSHTierTag from "./SSHTierTag.svelte";
  import HoverCard from "../HoverCard.svelte";

  let { sessionId, info }: { sessionId: string; info: SSHSessionInfo | null | undefined } = $props();

  const hasGrant = $derived(!!info?.connections?.some((c) => c.grant));
  const now = $derived(hasGrant ? sshClock.now : Date.now());
  const chip = $derived(chipFor(info, now));
  const next = $derived(info?.nextApproval);

  function open(e: MouseEvent) {
    e.stopPropagation();
    ssh.open(sessionId, { conn: chip?.conn.id, dock: !!info?.needsHuman });
  }
</script>

{#if chip}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <span class="inline-flex min-w-0 cursor-pointer items-center whitespace-nowrap" onclick={open} data-ssh-chip>
    <HoverCard class="inline-flex min-w-0 items-center gap-1.5 text-sm outline-none">
      {#snippet card()}
        <div class="grid gap-1.5">
          {#if info?.needsHumanWhy}<p class="text-ink">{info.needsHumanWhy}</p>{/if}
          {#if next}
            <p class="font-mono break-all text-ink">{next.command}</p>
            <p class="flex items-center gap-1.5 text-faint"><span>{next.host}</span><SSHTierTag tier={next.tier} /></p>
          {/if}
          <p class="text-faint">Click to review</p>
        </div>
      {/snippet}
      <SSHTierTag tier={chip.conn.tier} short />
      <!-- The host clips first: the state word is what a glance came for. -->
      <span class="min-w-0 truncate text-ink">{hostName(chip.conn)}</span>
      <span class="text-faint" aria-hidden="true">·</span>
      <span class="num shrink-0 {chip.fact.cls}">{chip.fact.words}</span>
      {#if chip.more > 0}<span class="num text-faint">+{chip.more}</span>{/if}
    </HoverCard>
  </span>
{/if}
