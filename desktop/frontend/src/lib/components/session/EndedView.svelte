<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  // A session that left the list, still readable for as long as the store keeps it. A merge is the
  // best moment of a session's life and the cleanup follows it within a minute, so without this
  // the story would vanish exactly when someone comes to read how it went.
  //
  // Read-only: no composer, no tray, no dock. The rows themselves still open, because reading is
  // what this is for, and the ones that could act ask for a live session first (a receipt's
  // actions, a widget's Open, a plate's Share). Everything else is the ordinary story, which reads
  // lola's record and the transcript by name and needs no live session.
  import { untrack } from "svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { talk } from "$lib/talk.svelte";
  import { store } from "$lib/store.svelte";
  import TimelineSlot from "./TimelineSlot.svelte";
  import Button from "../Button.svelte";
  import { reviewWorkspace } from "$lib/reviewworkspace.svelte";

  let { sessionId, style = "" }: { sessionId: string; style?: string } = $props();

  const ended = $derived(talk.ended(sessionId));
  // The field went with the session, and writing a draft was work. It stays where it can be read
  // and copied, even though it has nowhere to go any more.
  const view = $derived(ssh.view(sessionId));
  const draft = $derived(view.agentDraft.trim());

  $effect(() => {
    const sid = sessionId;
    untrack(() => {
      void ssh.view(sid).fetch();
      void talk.refresh(sid);
    });
  });
</script>

{#key sessionId}
  <div class="relative grid min-h-0 min-w-0 bg-canvas [container-type:size]" {style} data-ended-view data-conversation>
    <div class="absolute inset-0 grid grid-rows-[auto_minmax(72px,1fr)_auto]">
      <TimelineSlot {sessionId} />
      <div class="cv-band grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 pt-2 pb-3 text-sm text-faint" data-ended-line>
        {#if draft}
          <div class="col-span-2 grid grid-cols-[minmax(0,1fr)_auto] items-start gap-3" data-ended-draft>
            <div class="grid min-w-0 gap-1">
              <span>Your unsent draft</span>
              <p class="selectable max-h-32 overflow-y-auto whitespace-pre-wrap text-ink">{draft}</p>
            </div>
            <Button variant="secondary" size="xs" onclick={() => void navigator.clipboard?.writeText(draft)}>Copy</Button>
          </div>
        {/if}
        <p>This session has ended. Its worktree is gone.</p>
        <Button variant="secondary" size="xs" onclick={() => reviewWorkspace.open(sessionId)}>Review material</Button>
        {#if ended?.prUrl}
          <Button variant="secondary" size="xs" onclick={() => store.openURL(ended.prUrl ?? "")}>Open PR <span aria-hidden="true"><Icon name="external-link" size={14} /></span></Button>
        {/if}
      </div>
    </div>
  </div>
{/key}
