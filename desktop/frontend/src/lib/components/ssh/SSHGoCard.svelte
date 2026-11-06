<script lang="ts">
  // A running session asked to connect. Nothing connects until this is pressed, and
  // a request needs no expiry: a request nobody has seen is not a decision.
  import type { Snippet } from "svelte";
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import {
    SSH_ARM_MS,
    SSH_DATA_WARNING,
    SSH_UNSANDBOXED_NOTE,
    agentName,
    connectsLine,
    goCommandLine,
    hostName,
    tierBandClass,
    type SSHConnInfo,
  } from "$lib/ssh";
  import Button from "../Button.svelte";
  import SSHTierTag from "./SSHTierTag.svelte";

  let { sessionId, conn, index = 0, of = 0, pager = undefined }: { sessionId: string; conn: SSHConnInfo; index?: number; of?: number; pager?: Snippet } = $props();

  const session = $derived(store.sessionById(sessionId));
  const agent = $derived(agentName(session?.agent));
  const connects = $derived(connectsLine(conn));

  // A derived string, because every push replaces the conn OBJECT, and an effect reading
  // conn.id directly would re-arm (and blink) on each one.
  const connId = $derived(conn.id);
  let armed = $state(false);
  $effect(() => {
    connId;
    armed = false;
    const t = setTimeout(() => (armed = true), SSH_ARM_MS);
    return () => clearTimeout(t);
  });

  let body = $state<HTMLDivElement | null>(null);
  let denying = $state(false);
  let reason = $state("");
  let denyEl = $state<HTMLInputElement | null>(null);
  $effect(() => {
    if (denying) denyEl?.focus();
  });

  function go() {
    if (armed) void ssh.go(sessionId, conn.id);
  }
  function deny() {
    void ssh.deny(sessionId, conn.id, reason.trim());
    denying = false;
  }
  function onKey(e: KeyboardEvent) {
    if ((e.target as HTMLElement | null)?.tagName === "INPUT") return;
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    // Holding a key must not answer anything.
    if (e.repeat && (e.key === "Y" || e.key === "N")) {
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "PageDown" || e.key === "PageUp") {
      const step = e.key.startsWith("Page") ? (body?.clientHeight ?? 0) * 0.8 : 48;
      body?.scrollBy?.({ top: e.key === "ArrowUp" || e.key === "PageUp" ? -step : step });
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    if (e.key === "Y") {
      e.preventDefault();
      e.stopPropagation();
      go();
    } else if (e.key === "N") {
      e.preventDefault();
      e.stopPropagation();
      denying = true;
    } else if (e.key.length === 1) {
      e.stopPropagation();
    }
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_no_noninteractive_tabindex -->
<section
  data-card="go"
  tabindex="-1"
  aria-label="{agent} asks to connect to {hostName(conn)}"
  class="ssh-fade-in grid min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden rounded-[10px] border border-orange bg-panel outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-orange"
  onkeydown={onKey}
>
  <div class="h-0.5 {tierBandClass(conn.tier)}" aria-hidden="true"></div>
  <div bind:this={body} class="grid min-h-0 content-start gap-2.5 overflow-y-auto p-3" data-card-body>
    <div class="flex items-center gap-2">
      <h3 class="font-medium text-orange">{agent} asks to connect</h3>
      {#if pager}<span class="ml-auto">{@render pager()}</span>{:else if of > 1}<span class="num ml-auto text-sm text-faint">{index} of {of}</span>{/if}
    </div>
    <dl class="grid grid-cols-[max-content_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1">
      <dt class="text-sm text-faint">Host</dt>
      <dd class="flex items-center gap-1.5"><span class="font-medium text-ink">{hostName(conn)}</span> <SSHTierTag tier={conn.tier} /></dd>
      <dt class="text-sm text-faint">Connects</dt>
      <dd class="font-mono text-sm [overflow-wrap:anywhere] {connects.known ? 'text-ink' : 'text-ink-2'}">{connects.text}</dd>
      {#if session?.title}
        <dt class="text-sm text-faint">Session</dt>
        <dd class="truncate text-ink">{session.title}</dd>
      {/if}
    </dl>
    <!-- Lola's own lines first: the agent's text below can be any length. -->
    <div class="grid gap-0.5 text-sm text-ink-2" data-warnings>
      <p>Nothing connects until you press this.</p>
      <p>{goCommandLine(conn)}</p>
      <p>{SSH_DATA_WARNING}</p>
      {#if !conn.sandboxed}<p class="text-warn">{SSH_UNSANDBOXED_NOTE}</p>{/if}
    </div>
    {#if conn.why}
      <div class="grid grid-cols-[14px_minmax(0,1fr)] items-baseline gap-1.5" data-claim="why">
        <span class="inline-block h-2 w-2 translate-y-px justify-self-center rounded-full border border-faint" aria-hidden="true"></span>
        <span class="copy selectable text-ink-2">{agent} says why: {conn.why}</span>
      </div>
    {/if}
    {#if conn.scope}
      <div class="grid grid-cols-[14px_minmax(0,1fr)] items-baseline gap-1.5" data-claim="scope">
        <span class="inline-block h-2 w-2 translate-y-px justify-self-center rounded-full border border-faint" aria-hidden="true"></span>
        <span class="copy selectable text-ink-2">{agent} says it will: {conn.scope}</span>
      </div>
    {/if}
  </div>
  <!-- Pinned. The decision is never below the fold. -->
  <div class="grid gap-1.5 border-t border-edge/60 px-3 py-2" data-card-actions>
    {#if denying}
      <input
        bind:this={denyEl}
        class="w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none placeholder:text-placeholder focus:border-accent"
        placeholder="Tell {agent} why (optional)"
        aria-label="Reason for denying"
        bind:value={reason}
        onkeydown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            deny();
          } else if (e.key === "Escape") {
            e.preventDefault();
            e.stopPropagation();
            denying = false;
          }
        }}
      />
      <div class="flex justify-end gap-2">
        <Button size="md" onclick={() => (denying = false)}>Back</Button>
        <Button variant="danger" size="md" class="text-ink! enabled:hover:text-bad!" onclick={deny}>Deny</Button>
      </div>
    {:else}
      <div class="flex items-center justify-between gap-2">
        <Button variant="danger" size="md" class="text-ink! enabled:hover:text-bad!" onclick={() => (denying = true)}>Deny…</Button>
        <Button variant="primary" size="md" disabled={!armed} class="transition-opacity duration-200" onclick={go}>Open terminal and connect</Button>
      </div>
    {/if}
  </div>
</section>
