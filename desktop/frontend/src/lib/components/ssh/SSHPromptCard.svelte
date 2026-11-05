<script lang="ts">
  // A question the human's shell tab is waiting on, in place at the end of the stream.
  //
  // Every word of the question came from the other end, or from a program a host told to
  // print it, so this must read as someone else asking: the exact line in the mono the
  // recorded facts use, and who asks. No lock, no logo, nothing in lola's voice that could
  // pass for lola's own sign-in.
  //
  // The answer is a secret. It lives in this component's `value` and nowhere else: not the
  // draft, not the view, not the history. The field clears on send, before the reply.
  import { onMount } from "svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import { store } from "$lib/store.svelte";
  import { agentName, hostName, mmss, type SSHConnInfo, type SSHPromptInfo } from "$lib/ssh";
  import { toMillis } from "$lib/time";
  import Button from "../Button.svelte";

  let { sessionId, conn, prompt }: { sessionId: string; conn: SSHConnInfo; prompt: SSHPromptInfo } = $props();

  const view = $derived(ssh.view(sessionId));
  const host = $derived(hostName(conn));
  const signIn = $derived(prompt.stage === "signin");
  const confirm = $derived(prompt.kind === "confirm");
  const busy = $derived(view.promptBusy === prompt.id);
  // The header counts down for the connection on screen.
  const left = $derived(signIn && conn.connectDeadline && view.active()?.id !== conn.id ? toMillis(conn.connectDeadline) - sshClock.now : NaN);
  const retry = $derived(!confirm && (prompt.attempt ?? 0) > 0);
  // The agent shares the host user with this shell, so a job it left running can write to the
  // terminal and read the answer. lola cannot tell, and the command named above may be the
  // human's own. It only knows the agent has run things here, so it says that.
  const agentMayAsk = $derived(!signIn && !!conn.agentRan);
  const agent = $derived(agentName(store.sessionById(sessionId)?.agent));

  let value = $state("");
  let field = $state<HTMLInputElement | null>(null);

  // A card arrives unasked, so it only takes focus nobody is holding. An empty composer or a
  // widget still has someone's next keystrokes, and those would land in the password field. A
  // yes/no never takes focus: a stray Enter must not answer one. Nor does a question that may
  // be the agent's own job asking: see `agentMayAsk`.
  onMount(() => {
    if (confirm || !field || agentMayAsk) return;
    const a = document.activeElement;
    if (!a || a === document.body) field.focus();
  });

  function send() {
    const secret = value;
    if (!secret || busy) return;
    value = "";
    void ssh.answerPrompt(sessionId, conn, prompt, "send", secret);
  }

  function onKey(e: KeyboardEvent) {
    e.stopPropagation();
    if (e.key === "Enter" && !e.isComposing) {
      e.preventDefault();
      send();
    } else if (e.key === "Escape") {
      e.preventDefault();
      view.composerFocus++;
    }
  }
</script>

<section
  data-card="prompt"
  data-prompt-kind={prompt.kind}
  data-prompt-stage={prompt.stage}
  class="ssh-fade-in grid grid-cols-[minmax(0,1fr)] gap-1.5 rounded-md border border-orange/40 bg-panel px-3 py-2"
>
  <div class="flex min-w-0 items-baseline gap-2 text-sm text-faint">
    <span class="min-w-0 truncate">
      {#if signIn}Signing in to <span class="text-ink">{host}</span>{:else}<span class="text-ink">{host}</span> asks{#if prompt.command}<span class="font-mono">{" · "}{prompt.command}</span>{/if}{/if}
    </span>
    {#if !Number.isNaN(left)}<span class="num whitespace-nowrap">Gives up in {mmss(left)}</span>{/if}
    <span class="ml-auto"><Button size="xs" class="h-5!" onclick={() => ssh.showTerminal(sessionId)}>Open terminal</Button></span>
  </div>

  {#each prompt.context ?? [] as line, i (i)}
    <p class="selectable font-mono text-sm [overflow-wrap:anywhere] whitespace-pre-wrap text-faint" data-prompt-context>{line}</p>
  {/each}
  <p class="selectable font-mono [overflow-wrap:anywhere] whitespace-pre-wrap text-ink" data-prompt-line title="These words came from the connection, not from Lola.">{prompt.line}</p>

  {#if agentMayAsk}<p class="text-sm text-warn" data-prompt-agent>{agent} ran commands here. This could be its job asking.</p>{/if}

  {#if confirm}
    <div class="flex items-center gap-1.5">
      <Button variant="secondary" size="sm" loading={busy} onclick={() => ssh.answerPrompt(sessionId, conn, prompt, "yes")}>Yes</Button>
      <Button variant="secondary" size="sm" disabled={busy} onclick={() => ssh.answerPrompt(sessionId, conn, prompt, "no")}>No</Button>
    </div>
  {:else}
    <div class="grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-1.5">
      <!-- No form and no name, so nothing offers to remember it. -->
      <input
        bind:this={field}
        bind:value
        type="password"
        autocomplete="off"
        autocapitalize="off"
        spellcheck="false"
        data-1p-ignore
        data-lpignore="true"
        disabled={busy}
        aria-label={prompt.line}
        class="min-w-0 rounded border border-edge bg-canvas px-2 py-1 font-mono text-ink outline-none! focus-visible:border-accent disabled:opacity-60"
        onkeydown={onKey}
      />
      <Button variant="secondary" size="sm" loading={busy} disabled={!value} onclick={send}>Send</Button>
      <Button size="sm" disabled={busy} title="Sends Ctrl-C" onclick={() => ssh.answerPrompt(sessionId, conn, prompt, "cancel")}>Cancel</Button>
    </div>
  {/if}

  {#if retry}<p class="text-sm text-warn" role="status" data-prompt-retry>Not accepted. Try again.</p>{/if}
</section>
