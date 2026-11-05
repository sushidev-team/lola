<script lang="ts">
  // The moment trust is spent. Every field is drawn FROM the block and connection:
  // nothing here summarizes them. The command is whole, never truncated, and the
  // confirm button names the host, because you read what you approve.
  //
  // It has four defences against a reflex. The confirm control is dead for 800ms
  // after a card appears and again whenever the shown command changes, and it stays
  // dead until the end of the command has been on screen. Nothing autofocuses it, and
  // `Enter` does nothing on a card. `Y` confirms only while this card holds focus, so
  // a terminal holding the keyboard can never approve, and a held key never repeats.
  //
  // The body is the card's ONE scroller and the decision is pinned below it, so a
  // long command can never push Deny and Confirm off the card.
  //
  // The same card, in place in the stream, is a proposal's run check: the human
  // asked for it, so it says "You are running this", and it never skips arming.
  import { store } from "$lib/store.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import {
    COMMAND_SCROLL_LINES,
    SSH_ARM_MS,
    isEnded,
    SSH_DATA_WARNING,
    SSH_UNSANDBOXED_NOTE,
    SSH_USER_IS_THE_LIMIT,
    agentName,
    expiryWords,
    canGrant,
    commandLineCount,
    connectsLine,
    formatSeconds,
    grantOptions,
    hostName,
    tierAskLine,
    tierBandClass,
    tierOf,
    type SSHBlock,
    type SSHConnInfo,
  } from "$lib/ssh";
  import { toMillis } from "$lib/time";
  import Button from "../Button.svelte";
  import Disclosure from "../Disclosure.svelte";
  import Checkbox from "../Checkbox.svelte";
  import Select from "../Select.svelte";
  import SSHTierTag from "./SSHTierTag.svelte";

  let {
    sessionId,
    conn,
    block = undefined,
    index = 0,
    of = 0,
    proposal = undefined,
    onConfirm = undefined,
    onCancel = undefined,
  }: {
    sessionId: string;
    conn: SSHConnInfo;
    block?: SSHBlock;
    index?: number;
    of?: number;
    proposal?: { command: string; cwd: string; revision: number };
    onConfirm?: () => void;
    onCancel?: () => void;
  } = $props();

  const session = $derived(store.sessionById(sessionId));
  const agent = $derived(agentName(session?.agent));
  const host = $derived(hostName(conn));
  const liveConns = $derived(ssh.view(sessionId).connections.filter((c) => !isEnded(c)).length);
  const command = $derived(proposal?.command ?? block?.command ?? "");
  const cwd = $derived(proposal?.cwd ?? block?.cwd ?? "");
  const connects = $derived(connectsLine(conn));
  const lines = $derived(commandLineCount(command));
  const sandboxed = $derived(conn.sandboxed);

  // Re-arms whenever what the human is reading changes. A derived string, because every
  // fetch replaces the block OBJECT: an effect reading `block?.id` directly re-runs on
  // that, which disabled the button for 800ms on each refresh and made it blink.
  const reading = $derived(`${block?.id ?? ""}\n${command}`);
  let armed = $state(false);
  $effect(() => {
    reading;
    armed = false;
    const t = setTimeout(() => (armed = true), SSH_ARM_MS);
    return () => clearTimeout(t);
  });

  // The TUI refuses `Y` until a long command has been opened in full. This is the same
  // rule: what is below the fold may be the dangerous line, so it has to have been seen.
  let body = $state<HTMLDivElement | null>(null);
  let commandEnd = $state<HTMLSpanElement | null>(null);
  let readEnd = $state(false);
  function checkRead() {
    if (!body || !commandEnd) return;
    if (commandEnd.getBoundingClientRect().top <= body.getBoundingClientRect().bottom) readEnd = true;
  }
  $effect(() => {
    reading;
    readEnd = false;
    const el = body;
    if (!el) return;
    checkRead();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(checkRead);
    ro.observe(el);
    return () => ro.disconnect();
  });
  const canConfirm = $derived(armed && readEnd);

  let denying = $state(false);
  let reason = $state("");
  let denyEl = $state<HTMLInputElement | null>(null);
  $effect(() => {
    if (denying) denyEl?.focus();
  });

  // Off by default, every time. Production never renders this control, whatever
  // GrantMaxMinutes says: canGrant reads the tier first.
  const grantable = $derived(!proposal && canGrant(conn));
  let granting = $state(false);
  let grantMinutes = $state(15);
  const grantChoices = $derived(grantOptions(conn.grantMaxMinutes));
  $effect(() => {
    if (grantChoices.length && !grantChoices.includes(grantMinutes)) {
      grantMinutes = grantChoices.includes(15) ? 15 : grantChoices[0];
    }
  });

  const confirmLabel = $derived(
    proposal ? `Run on ${host}` : granting && grantable ? `Run and allow for ${grantMinutes} min` : `Run once on ${host}`,
  );
  const expiresIn = $derived(block?.approvalExpiresAt ? (toMillis(block.approvalExpiresAt) - sshClock.now) / 1000 : NaN);
  const tierLine = $derived(tierAskLine(conn));

  function confirm() {
    if (!canConfirm) return;
    if (proposal) onConfirm?.();
    else if (block) void ssh.approve(sessionId, block, granting && grantable ? grantMinutes : 0);
  }

  function deny() {
    if (block) void ssh.denyBlock(sessionId, block, reason.trim());
    denying = false;
    reason = "";
  }

  function onKey(e: KeyboardEvent) {
    const typing = (e.target as HTMLElement | null)?.tagName === "INPUT";
    if (typing) return;
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    // Holding a key must not answer anything.
    if (e.repeat && (e.key === "Y" || e.key === "N")) {
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    // The body is scrollable from here, because the confirm can wait on it.
    if (e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "PageDown" || e.key === "PageUp") {
      const step = e.key.startsWith("Page") ? (body?.clientHeight ?? 0) * 0.8 : 48;
      body?.scrollBy?.({ top: e.key === "ArrowUp" || e.key === "PageUp" ? -step : step });
      e.preventDefault();
      e.stopPropagation();
      return;
    }
    // Enter is deliberately nothing here. Shifted keys only, like every heavy key.
    if (e.key === "Y") {
      e.preventDefault();
      e.stopPropagation();
      confirm();
    } else if (e.key === "N") {
      e.preventDefault();
      e.stopPropagation();
      if (proposal) onCancel?.();
      else denying = true;
    } else if (e.key === "Escape" && proposal) {
      e.preventDefault();
      e.stopPropagation();
      onCancel?.();
    } else if (e.key.length === 1) {
      e.stopPropagation();
    }
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_no_noninteractive_tabindex -->
<section
  data-card={proposal ? "proposal" : "approval"}
  tabindex="-1"
  aria-label={proposal ? `Run this yourself on ${host}?` : `${agent} wants to run a command on ${host}, ${tierOf(conn.tier)}`}
  class="ssh-fade-in grid min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden rounded-md border border-orange/40 bg-panel outline-none focus-visible:border-orange {proposal ? 'max-h-[75cqh]' : ''}"
  onkeydown={onKey}
>
  <div class="h-0.5 {tierBandClass(conn.tier)}" aria-hidden="true"></div>
  <div bind:this={body} class="grid min-h-0 content-start gap-1.5 overflow-y-auto px-2.5 py-2 text-sm" onscroll={checkRead} data-card-body>
    <!-- The question, where, and the one or two things that change the answer. The rest
         is behind Details: a card this size gets read, and a wall of caveats does not. -->
    <div class="ssh-card-title flex flex-wrap items-center gap-x-2 gap-y-1">
      <span class="h-1.5 w-1.5 rounded-full bg-orange" aria-hidden="true"></span>
      <h3 class="text-sm font-medium text-orange">{proposal ? "Run this yourself?" : `${agent} wants to run`}</h3>
      <!-- The header right above names the host. Production repeats here because it is the
           one tier worth a second look, and a second host because the header may not be it. -->
      {#if tierOf(conn.tier) === "production"}<SSHTierTag tier={conn.tier} />{/if}
      {#if liveConns > 1}<span class="min-w-0 truncate text-sm text-ink">{host}</span>{/if}
      {#if block?.elevated}<span class="rounded-sm border border-warn/60 px-1 text-sm text-warn" title="This command raises its rights." data-elevated>sudo</span>{/if}
      {#if !sandboxed}<span class="text-sm text-warn" title={SSH_UNSANDBOXED_NOTE} data-unsandboxed>Unsandboxed</span>{/if}
      {#if !proposal && !Number.isNaN(expiresIn) && expiresIn < 300}<span class="text-sm text-warn">Expires in {expiryWords(expiresIn)}</span>{/if}
      {#if of > 1}<span class="num ml-auto text-sm text-faint">{index} of {of}</span>{/if}
    </div>

    <!-- Whole, never truncated, and in this one scroller: there is no second one to hide its tail in. -->
    <div class="ssh-card-command grid gap-1 rounded border border-edge/60 bg-canvas px-2.5 py-2 font-mono text-sm">
      {#if cwd}<div class="selectable truncate text-faint" title={cwd} data-card-cwd>{cwd}</div>{/if}
      <div data-command class="selectable whitespace-pre-wrap [overflow-wrap:anywhere] text-ink">{command}<span bind:this={commandEnd} aria-hidden="true"></span></div>
    </div>

    <!-- Beside the facts, in the agent's voice: this reason is a claim. -->
    {#if block?.why}
      <div class="grid grid-cols-[14px_minmax(0,1fr)] items-baseline gap-1.5" data-claim="why">
        <span class="inline-block h-2 w-2 translate-y-px justify-self-center rounded-full border border-faint" aria-hidden="true"></span>
        <span class="selectable text-ink/85">{block.why}</span>
      </div>
    {/if}

    {#if grantable}
      <label class="flex flex-wrap items-center gap-2" title="A grant is not limited to this command. It ends when you take over.">
        <Checkbox bind:checked={granting} />
        <span>Allow everything on this host for</span>
        <span class="w-24"><Select aria-label="Grant length" bind:value={grantMinutes} disabled={!granting}>
          {#each grantChoices as m (m)}<option value={m}>{m} min</option>{/each}
        </Select></span>
      </label>
    {/if}

    <Disclosure label="Details" class="-ml-1.5">
      <div class="mt-1 ml-1.5 grid gap-1.5 text-sm text-ink/70" data-warnings>
        <dl class="grid grid-cols-[max-content_minmax(0,1fr)] items-baseline gap-x-3 gap-y-0.5">
          <dt class="text-faint">Connects</dt>
          <dd class="min-w-0 font-mono [overflow-wrap:anywhere] {connects.known ? 'text-ink' : 'text-warn'}">{connects.text}</dd>
          <dt class="text-faint">Runs in</dt>
          <dd class="min-w-0 font-mono [overflow-wrap:anywhere]">{#if cwd}<span class="text-ink">{cwd}</span>{:else}<span class="text-faint">login directory</span>{/if}</dd>
          {#if block?.timeoutSeconds}
            <dt class="text-faint">Timeout</dt>
            <dd class="num text-ink">{formatSeconds(block.timeoutSeconds)}</dd>
          {/if}
        </dl>
        <p>{SSH_DATA_WARNING}</p>
        <p>{SSH_USER_IS_THE_LIMIT}</p>
        {#if !sandboxed}<p class="text-warn">{SSH_UNSANDBOXED_NOTE}</p>{/if}
        {#if !proposal}
          <p>
            {#if tierLine}{tierLine}{/if}
            {#if !Number.isNaN(expiresIn)}<span>Expires in {expiryWords(expiresIn)}.</span>{/if}
          </p>
        {/if}
      </div>
    </Disclosure>
  </div>

  <!-- Pinned. The decision is never below the fold. -->
  <div class="grid gap-1.5 border-t border-edge/60 px-2.5 py-1.5" data-card-actions>
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
      <div class="flex items-center gap-2">
        <span class="text-sm text-ink/70">{agent} gets your reason with its next command.</span>
        <span class="ml-auto flex gap-2">
          <Button size="md" onclick={() => (denying = false)}>Back</Button>
          <Button variant="danger" size="md" class="text-ink! enabled:hover:text-bad!" onclick={deny}>Deny</Button>
        </span>
      </div>
    {:else}
      <div class="flex items-center gap-2">
        {#if proposal}
          <Button size="sm" class="text-ink!" onclick={onCancel}>Cancel</Button>
        {:else}
          <Button variant="danger" size="sm" class="text-ink! enabled:hover:text-bad!" onclick={() => (denying = true)}>Deny…</Button>
        {/if}
        {#if !readEnd}
          <span class="ml-auto min-w-0 text-right text-sm text-warn" data-read-hint>{lines > COMMAND_SCROLL_LINES ? `${lines} lines. ` : ""}Scroll to the end to run this</span>
        {/if}
        <Button
          variant="primary"
          size="sm"
          disabled={!canConfirm}
          class="transition-opacity duration-200 {readEnd ? 'ml-auto' : ''}"
          onclick={confirm}
        >{confirmLabel}</Button>
      </div>
    {/if}
  </div>
</section>
