<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  // The strip under every view that is not the conversation. Its left end is where the composer's
  // state sits in the conversation, and that shared position is what makes the agent axis
  // findable: bottom left, in every view. It always exists in those views and has a fixed height,
  // so the PTY never resizes because something arrived in it.
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import { talk } from "$lib/talk.svelte";
  import { terms, isChangesTab } from "$lib/terms.svelte";
  import { feedback } from "$lib/feedback.svelte";
  import { agentName, plural } from "$lib/ssh";
  import { conversationOffered, elapsedWords, stateGlyph } from "$lib/sessionview";
  import { waitingQueue } from "$lib/queue";
  import { toMillis } from "$lib/time";
  import Button from "../Button.svelte";
  import StateGlyph from "../StateGlyph.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const session = $derived(store.sessionById(sessionId));
  const view = $derived(ssh.view(sessionId));
  const glyph = $derived(session ? stateGlyph(session, nav.seenAt(sessionId)) : null);
  const working = $derived(glyph?.glyph === "◐");
  const gone = $derived(glyph?.glyph === "✕");
  const agent = $derived(agentName(session?.agent));

  const now = $derived(working ? sshClock.now : Date.now());
  const clock = $derived(working ? elapsedWords(now - toMillis(session?.agentStateSince as unknown as string)) : "");
  const stateWord = $derived(gone ? `${agent} exited` : glyph?.label ?? "");

  const caps = $derived(talk.state(sessionId)?.caps);
  const canStop = $derived(store.alive && working && !!caps?.stop && !session?.goalId);
  function stop() {
    talk.stop(sessionId).catch((err) => store.setFlash(String(err), "warn"));
  }

  const onChanges = $derived(session ? isChangesTab(terms.activeTab(sessionId)) : false);
  const drafted = $derived(onChanges ? feedback.count(sessionId) : 0);
  // A queued send waits while a person holds the terminal, because a flush could land mid-word.
  const queued = $derived(view.items.filter((i) => i.human && !i.human.delivered).length);

  const waiting = $derived(waitingQueue(store.rosterSessions).length);
  const offered = $derived(!!session && conversationOffered(session));
  const canRevive = $derived(session?.status === "dead" || session?.status === "session_ended");

  function sendDrafts() {
    void feedback.send(sessionId);
  }
</script>

<footer class="grid h-7 min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 border-t border-edge/60 bg-canvas px-3 text-sm" data-session-footer>
  <div class="flex min-w-0 items-center gap-2">
    {#if !store.alive}
      <span class="text-faint" aria-hidden="true"><Icon name="circle" size={14} /></span>
      <span class="truncate text-ink-2" data-daemon-down>Daemon down. This terminal still works.</span>
    {:else if glyph}
      <span class="grid w-4 shrink-0 {working ? 'ssh-pulse motion-reduce:animate-none' : ''}"><StateGlyph label={glyph.label} glyph={glyph.glyph} cls={glyph.cls} mono={glyph.mono} /></span>
      <span class="whitespace-nowrap {glyph.glyph === '◉' ? 'text-orange' : 'text-ink-2'}">{stateWord}{#if clock}{" "}<span class="num">{clock}</span>{/if}</span>
      {#if canStop}
        <Button variant="danger" size="xs" class="h-5!" title="Stop this turn. The session stays open." onclick={stop}><span aria-hidden="true"><Icon name="square" size={14} /></span> Stop <span class="text-faint">⌘.</span></Button>
      {/if}
      {#if gone && canRevive}
        <Button size="xs" class="h-5!" onclick={() => store.revive(sessionId)}>{session?.location ? "Reopen" : "Revive"}</Button>
      {/if}
    {/if}
  </div>

  <div class="min-w-0 truncate text-faint">
    {#if onChanges && drafted > 0}
      <span class="num">{plural(drafted, "comment")}</span> drafted
      <Button variant="primary" size="xs" class="ml-2 h-5!" loading={feedback.isSending(sessionId)} title="Deliver every queued comment as one message, typed only when the agent is waiting at its prompt" onclick={sendDrafts}>Send to {agent}</Button>
    {:else if !onChanges && queued > 0}
      <span class="num">{queued}</span> queued · waits until you leave
    {/if}
  </div>

  <div class="flex items-center gap-1">
    {#if waiting > 0 && store.alive}
      <Button size="xs" class="h-5! text-orange!" title="Next session waiting on you" onclick={() => ssh.nextWaiting()}>Waiting <span class="num">{waiting}</span> <span class="text-faint">⌘J</span></Button>
    {/if}
    {#if session}
      {#if onChanges && !offered}
        <Button size="xs" class="h-5!" title="Leave Changes" onclick={() => terms.toggleDiff(sessionId)}>Back <span class="text-faint">⌘⇧D</span></Button>
      {:else if offered}
        <Button size="xs" class="h-5!" onclick={() => session && ssh.leaveTerminal(session)}>Conversation <span class="text-faint">⌘\</span></Button>
      {:else}
        <Button size="xs" class="h-5!" onclick={() => nav.focusSessions()}>Sessions <span class="text-faint">⌃Q</span></Button>
      {/if}
    {/if}
  </div>
</footer>
