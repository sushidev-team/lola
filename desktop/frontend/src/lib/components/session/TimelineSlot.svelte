<script lang="ts">
  // Where the story goes: what lola could not read, the agent's own account, then the story itself.
  // The bands sit outside the scroller, so they stay put while it moves.
  //
  // `contents`: its children are rows of the column's grid (.ssh-col in app.css).
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { feed } from "$lib/feed.svelte";
  import { talk } from "$lib/talk.svelte";
  import StoryLine from "./StoryLine.svelte";
  import FeedNotice from "./FeedNotice.svelte";
  import Timeline from "./Timeline.svelte";
  import SSHOutput from "../ssh/SSHOutput.svelte";

  let {
    sessionId,
    covered = false,
    side = false,
    onside,
  }: { sessionId: string; covered?: boolean; side?: boolean; onside?: () => void } = $props();

  const view = $derived(ssh.view(sessionId));
  const f = $derived(feed.view(sessionId));
  // talkState judges health every second, and a feed reply only when there was something to fetch.
  const health = $derived(talk.state(sessionId)?.health || f.health);
  const maxBlock = $derived(view.outputMax ? view.block(view.outputMax) : undefined);
</script>

<div class="contents" data-timeline-slot>
  <div class="cv-bands">
    {#if !store.alive}
      <p class="border-b border-edge/60 px-3 py-1.5 text-sm text-bad" role="status" data-ssh-offline>The daemon is down. This is the last record it sent, and nothing here can act until it is back. Your terminal still works.</p>
    {/if}
    <!-- A daemon that is down answers nothing, and that says nothing about the log. -->
    {#if store.alive}<FeedNotice {sessionId} health={health} />{/if}
    {#if !side}<StoryLine {sessionId} {onside} />{/if}
  </div>
  <Timeline {sessionId} />
  {#if maxBlock}
    <div class="absolute inset-0 z-20 grid min-h-0 grid-rows-[minmax(0,1fr)] bg-canvas p-3">
      <SSHOutput {sessionId} block={maxBlock} full />
    </div>
  {/if}
</div>
