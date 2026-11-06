<script lang="ts">
  // The terminal you drive in, always yours in both wheel positions. The agent's own pane is
  // not here: it is the session's Agent tab. The tmux session of the ssh tab is mounted ONLY
  // here, because one tmux session with two xterm clients would fight over its size. No `isolate` or `z-0` around the terminal: that blanks the WebGL
  // canvas in WKWebView.
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { agentName, hostName, spokenSeconds, type SSHConnInfo } from "$lib/ssh";
  import Button from "../Button.svelte";
  import LiveTerminal from "../LiveTerminal.svelte";

  // `chrome` false: the session's footer strip says how to leave, so this draws no bar of its own.
  // The old workspace keeps its bar until it goes.
  let { sessionId, conn, chrome = true }: { sessionId: string; conn: SSHConnInfo; chrome?: boolean } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const agent = $derived(agentName(session?.agent));
  const host = $derived(hostName(conn));
  const shellName = $derived(conn.tab ?? "");
  const shellClosed = $derived(conn.state === "connected" && !conn.tabAlive);
  const requested = $derived(conn.state === "requested");
  const showOverlay = $derived(conn.state === "disconnected");
  const shellLive = $derived(!requested && !shellClosed && !!shellName);
  const name = $derived(shellLive ? shellName : "");

  const hintKey = $derived(`wheel-${conn.id}`);
  let focusedOnce = $state(false);
  const hint = $derived(conn.wheel === "agent" && shellLive && focusedOnce && !view.dismissedHints.has(hintKey));

  // A focus request reaches a mounted terminal through focusRequest, and one that
  // has not mounted yet through autofocus. Only a request consumed here counts, so
  // coming back to this tab later never steals the keyboard.
  let focusRequest = $state(0);
  let wantFocus = $state(false);
  $effect(() => {
    const n = view.terminalFocus;
    if (n === view.seen.terminal) return;
    view.seen.terminal = n;
    wantFocus = true;
    focusRequest++;
  });

  // Offered at a known prompt, or as a line to look at when lola cannot read this shell.
  const goTo = $derived(!!conn.cwd && conn.tabAlive && !conn.termElevated && conn.cwd !== conn.termCwd && (!conn.termTracked || conn.termAtPrompt));

  const border = $derived(
    conn.wheel === "human"
      ? "border-2 border-accent"
      : conn.state === "authenticating"
        ? "border-2 border-orange"
        : "border-2 border-transparent",
  );
</script>

<div class="grid h-full min-h-0 grid-rows-[auto_auto_auto_minmax(0,1fr)] bg-panel" data-terminal-pane>
  <div>
    {#if chrome}
      <div class="relative z-10 flex h-8 items-center gap-0.5 border-b border-edge/60 px-2">
        <span class="mr-2"><Button size="xs" title="Back to the timeline (⌘\\)" onclick={() => ssh.showTimeline(sessionId)}><span aria-hidden="true">←</span> Timeline</Button></span>
        <span class="min-w-0 truncate text-sm text-faint">{host}</span>
      </div>
    {/if}
  </div>
  <div>
    {#if hint}
      <div class="flex items-center gap-2 border-b border-edge/60 px-3 py-1 text-sm text-faint">
        <span class="min-w-0 flex-1">{conn.termTracked ? `Your long-running shell. Commands here appear in the timeline, visible only to you. A line starting with a space stays out.` : `Your own shell. Lola cannot read this one, so its commands stay out of the timeline.`}</span>
        <Button size="xs" icon aria-label="Dismiss" onclick={() => view.dismissedHints.add(hintKey)}>×</Button>
      </div>
    {/if}
  </div>
  <!-- This wrapper always renders: without it the terminal slid into the auto row and
       collapsed to its content's height. -->
  <div>
    {#if goTo}
      <div class="flex items-center gap-2 border-b border-edge/60 px-3 py-1 text-sm text-faint" data-go-to>
        <span class="min-w-0 truncate">The timeline is in <span class="selectable font-mono text-ink">{conn.cwd}</span></span>
        <Button size="xs" class="h-5!" onclick={() => ssh.terminalToCwd(sessionId, conn.id)}>Go there</Button>
      </div>
    {/if}
  </div>
  <!-- p-4 and bg-panel match every other terminal, so its padding stays seamless. -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="relative min-h-0 p-4 {border} transition-colors duration-200" onfocusin={() => (focusedOnce = true)}>
    {#if name}
      {#key name}
        <LiveTerminal
          {name}
          webgl
          interactive
          autofocus={wantFocus}
          {focusRequest}
          onExit={() => view.fetch()}
          onEscapeFocus={() => (chrome ? view.streamFocus++ : ssh.showTimeline(sessionId))}
        />
      {/key}
    {:else if requested}
      <div class="grid h-full place-items-center text-center text-faint">No connection yet. Nothing has been attempted.</div>
    {:else if shellClosed}
      <div class="grid h-full place-items-center">
        <div class="grid max-w-md justify-items-center gap-2 text-center">
          <p class="copy text-ink">Your shell on {host} ended. The connection is still up, and {agent} can keep working.</p>
          <p class="text-sm text-faint">It closes by itself after {spokenSeconds(conn.idleLingerSeconds ?? 900)} with no client.</p>
          <Button variant="secondary" size="sm" onclick={() => ssh.terminal(sessionId, conn.id)}>Open a new shell</Button>
        </div>
      </div>
    {:else}
      <div class="grid h-full place-items-center text-center text-faint">No terminal yet.</div>
    {/if}
    {#if showOverlay}
      <div class="absolute inset-x-0 bottom-3 flex justify-center">
        <Button variant="secondary" size="sm" class="bg-panel! shadow-md" onclick={() => ssh.go(sessionId, conn.id)}>Reconnect</Button>
      </div>
    {/if}
  </div>
</div>
