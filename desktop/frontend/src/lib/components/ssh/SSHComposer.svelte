<script lang="ts">
  // One field, and the key decides where it goes: by default ↩ asks the agent and ⌘↩ runs
  // on the host. Tapping ⌘ twice swaps them, on every tier, for a stretch of typing
  // commands. The worst mistake is a sentence for the agent executing on a host, so the
  // swap always shows as a different field, with the tier's colour down its side.
  //
  // Holding ⌘ shows what the chord would do before anything happens. All of it is read from
  // the field's own key events, so these keys mean nothing unless the field has focus.
  //
  // Asking never interrupts: a pane mid-turn is never typed into, so a message waits for
  // the agent's next command or its resting prompt. Stop is what stops it.
  import { tick } from "svelte";
  import { store } from "$lib/store.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import {
    SSH_MAX_SAY_BYTES,
    SSH_REMOTE_OUTLIVES,
    agentName,
    blockElapsedMs,
    byteLength,
    composerLine,
    formatBytes,
    formatMs,
    hostName,
    needsTerminal,
    plural,
    runPrompt,
    runRoute,
    tierOf,
    isTerminal,
  } from "$lib/ssh";
  import Button from "../Button.svelte";
  import LivePulse from "../LivePulse.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const HOLD_MS = 180;
  const TAP_MS = 250;
  const DOUBLE_TAP_MS = 400;

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const conn = $derived(view.active());
  const agent = $derived(agentName(session?.agent));
  const line = $derived(composerLine(session ?? {}, conn?.wheel));
  const host = $derived(conn ? hostName(conn) : "the host");
  const canRun = $derived(conn?.state === "connected");
  const bytes = $derived(byteLength(view.draft));
  const nearLimit = $derived(bytes >= SSH_MAX_SAY_BYTES * 0.8);
  const over = $derived(bytes > SSH_MAX_SAY_BYTES);
  const inFlight = $derived(view.items.filter((i) => i.block?.status === "running" && i.block?.origin !== "terminal" && (!conn || i.block?.conn === conn.id)));
  const route = $derived(runRoute(conn, agent));
  const rail = $derived(({ production: "bg-bad", staging: "bg-warn", dev: "bg-accent" } as const)[tierOf(conn?.tier)]);
  // A command in your shell that printed nothing, or stopped mid-line, may be asking for
  // something lola did not recognize. A question it did recognize has its own card.
  const asked = $derived(!!conn?.prompt);
  const openLine = $derived(!!conn?.termOpenLine);
  const waitingShell = $derived(!asked && view.tabItems.some((i) => i.block?.origin === "terminal" && i.block?.status === "running" && (openLine || !i.block?.stdoutBytes)));

  let field = $state<HTMLTextAreaElement | null>(null);
  let busy = $state(false);
  let walked = $state(-1); // position in the command history; -1 is the draft
  let held = $state(false); // ⌘ held on the field: show what ⌘↩ would do
  let holdTimer: ReturnType<typeof setTimeout> | undefined;
  let lastTap = 0; // when ⌘ was last tapped alone
  let tapDown = 0; // when ⌘ went down, 0 once another key has made it a chord
  const runsByDefault = $derived(view.runDefault);
  const running = $derived(canRun && runsByDefault !== held);

  $effect(() => {
    const n = view.composerFocus;
    if (n === view.seen.composer) return;
    view.seen.composer = n;
    void tick().then(() => {
      field?.focus();
      field?.setSelectionRange(view.draft.length, view.draft.length);
    });
  });

  // It grows to 6 lines, then scrolls.
  $effect(() => {
    view.draft;
    const el = field;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 6 * 18 + 12)}px`;
  });

  // Your command came back from the shell as a block: open it, as with any you ran.
  $effect(() => {
    if (!view.expectShellBlock) return;
    const it = [...view.tabItems].reverse().find((i) => i.block?.origin === "terminal" && i.block?.actor === "human");
    if (!it?.block || isTerminal(it.block.status)) return;
    view.expectShellBlock = false;
    view.openBlock(it.block.id);
  });

  async function ask() {
    const value = view.draft.trim();
    if (!value || busy || over) return;
    busy = true;
    if (await ssh.say(sessionId, value)) view.draft = "";
    busy = false;
  }

  async function run() {
    const value = view.draft.trim();
    if (!value || busy || !conn) return;
    if (!canRun) {
      view.setFlash(`Not connected, so nothing can run on ${host}.`, "text-warn");
      return;
    }
    const interactive = needsTerminal(value);
    if (interactive && !route.inShell) {
      view.setFlash("This looks interactive, and your shell cannot take it right now.", "text-warn");
      return;
    }
    busy = true;
    if (await ssh.runCommand(sessionId, conn.id, value)) {
      view.draft = "";
      walked = -1;
      if (interactive) openTerminal(); // it wants a real terminal, and is now running in one
    }
    busy = false;
  }

  function historyStep(step: number) {
    const h = view.history;
    if (!h.length) return;
    const from = walked < 0 ? h.length : walked;
    const next = from + step;
    if (next < 0) return;
    if (next >= h.length) {
      walked = -1;
      view.draft = "";
      return;
    }
    walked = next;
    view.draft = h[next];
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === "Meta") {
      if (!e.repeat) metaDown();
      return;
    }
    tapDown = 0; // ⌘C and friends are chords, not taps
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      release();
      if ((e.metaKey || e.ctrlKey) !== runsByDefault) void run();
      else void ask();
    } else if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      field?.blur(); // leaves the field and keeps the draft
    } else if ((e.key === "ArrowUp" && !view.draft) || (walked >= 0 && (e.key === "ArrowUp" || e.key === "ArrowDown"))) {
      // Command history, only from an empty field, so it never fights moving in a message.
      e.preventDefault();
      historyStep(e.key === "ArrowUp" ? -1 : 1);
    }
  }

  // After a beat, so ⌘C or a double tap does not flash the other look.
  function metaDown() {
    tapDown = Date.now();
    clearTimeout(holdTimer);
    holdTimer = setTimeout(() => (held = true), HOLD_MS);
  }
  function onKeyUp(e: KeyboardEvent) {
    if (e.key !== "Meta") return;
    release();
    const now = Date.now();
    if (!tapDown || now - tapDown > TAP_MS) {
      lastTap = 0;
      return;
    }
    tapDown = 0;
    if (now - lastTap > DOUBLE_TAP_MS) {
      lastTap = now;
      return;
    }
    lastTap = 0;
    view.runDefault = !view.runDefault;
  }
  function release() {
    clearTimeout(holdTimer);
    held = false;
  }

  function openTerminal() {
    ssh.showTerminal(sessionId);
  }
</script>

<div class="grid gap-1.5 border-t border-edge/60 px-3 py-2">
  {#if inFlight.length && conn?.wheel === "human"}
    <div class="grid gap-1 text-sm" data-driving>
      {#each inFlight as it (it.id)}
        {#if it.block}
          {@const b = it.block}
          <div class="flex min-w-0 items-center gap-2">
            <span class="selectable min-w-0 flex-1 truncate font-mono text-ink">{b.command}</span>
            <span class="num shrink-0 text-faint">{formatMs(blockElapsedMs(b, sshClock.now))}</span>
            <Button variant="danger" size="xs" title={SSH_REMOTE_OUTLIVES} onclick={() => ssh.stop(sessionId, b)}>Stop</Button>
          </div>
        {/if}
      {/each}
      <span class="text-faint">{plural(inFlight.length, "command")} still running.</span>
    </div>
  {:else if line.line && !line.driving && line.line !== `${agent} is waiting.`}
    <div class="flex items-center gap-1.5 text-sm {line.cls}" data-state-line>
      {#if line.pulse}<LivePulse agentState="working" />{/if}
      <span class="min-w-0">{line.line}</span>
      {#if line.action === "show"}
        <Button size="xs" class="h-5! text-orange!" onclick={() => ssh.showAgentPane(sessionId)}>Show</Button>
      {:else if line.action === "revive"}
        <Button variant="accent" size="xs" class="h-5!" onclick={() => store.revive(sessionId)}>Revive</Button>
      {/if}
    </div>
  {/if}
  {#if view.flash}<p class="text-sm {view.flashCls}" role="status">{view.flash}</p>{/if}
  {#if waitingShell}
    <div class="flex items-center gap-2 text-sm text-faint" data-shell-waiting>
      <span class="min-w-0">Waiting for input?</span>
      <Button size="xs" class="h-5!" onclick={openTerminal}>Open terminal</Button>
    </div>
  {/if}

  <div
    class="relative overflow-hidden rounded-md border transition-colors focus-within:border-accent {running ? 'border-edge bg-canvas' : 'border-edge bg-panel'}"
    data-composer={running ? "run" : "ask"}
  >
    {#if running}
      <span class="absolute inset-y-0 left-0 w-[3px] {rail}" aria-hidden="true"></span>
    {/if}
    <!-- The same box in both states: holding ⌘ must not move the timeline above it. -->
    <div class="flex items-start px-3 pt-2">
      {#if running}<span class="py-1 pr-1.5 font-mono text-accent-ink" aria-hidden="true">$</span>{/if}
      <textarea
        bind:this={field}
        bind:value={view.draft}
        rows="1"
        spellcheck={!running}
        class="max-h-[120px] min-w-0 flex-1 resize-none bg-transparent py-1 text-ink outline-none placeholder:text-placeholder focus-visible:outline-none! {running ? 'font-mono' : ''}"
        placeholder={running ? `Command to run on ${host}` : line.placeholder}
        aria-label={running ? `Command to run on ${host}` : line.placeholder}
        oninput={() => (walked = -1)}
        onkeydown={onKey}
        onkeyup={onKeyUp}
        onblur={release}
      ></textarea>
    </div>
    <!-- Both destinations, always in view, so the shortcuts teach themselves. -->
    <div class="flex items-center gap-1 px-1.5 pt-0.5 pb-1.5" data-composer-actions>
      <span class="selectable mr-auto min-w-0 truncate px-1.5 font-mono text-sm text-faint" data-run-prompt>{running ? runPrompt(conn) : ""}</span>
      <Button size="xs" class={running ? "text-faint!" : ""} disabled={!view.draft.trim() || busy || over} aria-label="Send to {agent}" title="Send to {agent}. ⇧↩ inserts a line. Tap ⌘ twice to swap ↩ and ⌘↩." onclick={ask}>
        <span class="num opacity-70" aria-hidden="true">{runsByDefault ? "⌘↩" : "↩"}</span> Ask {agent}
      </Button>
      <Button size="xs" class={running ? "text-ink! bg-sel!" : ""} disabled={!view.draft.trim() || busy || !canRun} aria-label="Run on {host}" title={canRun ? route.note : `Not connected, so nothing can run on ${host}.`} onclick={run}>
        <span class="num opacity-70" aria-hidden="true">{runsByDefault ? "↩" : "⌘↩"}</span> Run on {host}
      </Button>
    </div>
  </div>
  {#if nearLimit}
    <p class="num text-right text-sm text-warn">{formatBytes(bytes)} of {formatBytes(SSH_MAX_SAY_BYTES)}</p>
  {/if}
</div>
