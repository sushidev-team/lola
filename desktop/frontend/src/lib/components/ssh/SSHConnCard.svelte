<script lang="ts">
  // The connection's own moments, each with a way forward: signing in, a drop, a
  // failure. Reconnecting is a go, so it carries the data warning too. An agent can
  // never get here: only a human's press reconnects.
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import {
    SSH_DATA_WARNING,
    spokenSeconds,
    hostName,
    isIdleDrop,
    mmss,
    plural,
    type SSHConnInfo,
  } from "$lib/ssh";
  import { toMillis } from "$lib/time";
  import Button from "../Button.svelte";

  let {
    sessionId,
    conn,
    kind,
    running = 0,
  }: { sessionId: string; conn: SSHConnInfo; kind: "auth" | "reconnect" | "failed"; running?: number } = $props();

  const host = $derived(hostName(conn));
  const idle = $derived(isIdleDrop(conn));
  const left = $derived(conn.connectDeadline && ssh.view(sessionId).active()?.id !== conn.id ? toMillis(conn.connectDeadline) - sshClock.now : NaN);
  const linger = $derived(spokenSeconds(conn.idleLingerSeconds ?? 900));
  const detail = $derived(conn.stateDetail ?? "");
  const timedOut = $derived(/finish|within|ran out|timed|cancelled/i.test(detail));
  const noSsh = $derived(/not found/i.test(detail));
</script>

{#if kind === "auth"}
  <!-- Nothing recognized as a question, or not yet. It sits where the question's card will, so
       nothing jumps when one is recognized, and it can only point at the terminal. -->
  <section data-card="auth" class="ssh-fade-in flex flex-wrap items-center gap-x-2 gap-y-1 rounded-md border border-edge/60 bg-panel px-3 py-2 text-sm">
    <span class="text-ink">Signing in to {host}.</span>
    <span class="text-faint">Waiting for input?</span>
    <Button size="xs" class="h-5!" onclick={() => ssh.showTerminal(sessionId)}>Open terminal</Button>
    {#if !Number.isNaN(left)}<span class="num ml-auto text-faint">Gives up in {mmss(left)}</span>{/if}
  </section>
{:else if kind === "reconnect"}
  <section data-card="reconnect" class="ssh-fade-in grid gap-2 rounded-md border {idle ? 'border-edge/60' : 'border-warn/35'} bg-panel p-3">
    {#if idle}
      <p class="copy text-ink">The connection closed after {linger} with nothing using it.</p>
    {:else}
      <p class="copy">
        <span class="font-medium text-warn">Connection to {host} dropped.</span>
        {#if running === 1}<span class="text-ink/85"> 1 command was running. Its result is unknown, and it may still be running on the host.</span>{:else if running > 1}<span class="text-ink/85"> {plural(running, "command")} were running. Their results are unknown, and they may still be running on the host.</span>{/if}
      </p>
    {/if}
    <div class="grid gap-0.5 text-sm text-faint">
      <p>Reconnecting may ask for MFA again.</p>
      <p>{SSH_DATA_WARNING}</p>
    </div>
    <div class="flex items-center gap-2">
      <Button variant="secondary" size="sm" onclick={() => ssh.go(sessionId, conn.id)}>Reconnect</Button>
      <Button size="sm" onclick={() => ssh.askClose(sessionId, conn)}>Close connection</Button>
    </div>
  </section>
{:else}
  <section data-card="failed" class="ssh-fade-in grid gap-2 rounded-md border border-bad/35 bg-panel p-3">
    <p class="copy">
      {#if noSsh}
        <span class="font-medium text-bad">ssh was not found on this Mac. Lola needs it to connect.</span>
      {:else if timedOut}
        <span class="font-medium text-bad">{detail}</span>
      {:else}
        <span class="font-medium text-bad">Could not connect to {host}.</span>
        <span class="text-ink/85"> ssh's message is in the terminal.</span>
      {/if}
    </p>
    <div class="flex items-center gap-2">
      <Button variant="secondary" size="sm" onclick={() => ssh.go(sessionId, conn.id)}>Try again</Button>
      <Button size="sm" onclick={() => ssh.askClose(sessionId, conn)}>Close connection</Button>
    </div>
  </section>
{/if}
