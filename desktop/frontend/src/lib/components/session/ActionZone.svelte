<script lang="ts">
  // What you can do now, bottom up: composer, queue rows, tray. Nothing above moves when something
  // here changes height, because the column's grid gives the story whatever is left.
  //
  // `contents`: its children are rows of the column's grid (.ssh-col in app.css). The attribute is
  // how App's Esc ladder recognises "the composer's own field" as opposed to a terminal's, since
  // both are textareas.
  //
  // A connection ending no longer takes the composer away, because the agent is still there. Only
  // a direct session with no live host has nobody left to talk to.
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { nav } from "$lib/nav.svelte";
  import { stateGlyph } from "$lib/sessionview";
  import Button from "../Button.svelte";
  import StateGlyph from "../StateGlyph.svelte";
  import FirstOpen from "./FirstOpen.svelte";
  import { SSH_ENDED_OUTLIVES, isEnded } from "$lib/ssh";
  import SSHHandback from "../ssh/SSHHandback.svelte";
  import SSHEnded from "../ssh/SSHEnded.svelte";
  import Composer from "./Composer.svelte";
  import QueueRows from "./QueueRows.svelte";
  import Tray from "./Tray.svelte";

  let { sessionId, covered = false }: { sessionId: string; covered?: boolean } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const shown = $derived(view.connections.filter((c) => c.state !== "denied"));
  const direct = $derived(session?.location === "ssh");
  const glyph = $derived(session ? stateGlyph(session, nav.seenAt(sessionId)) : null);
  const noneLeft = $derived(direct && shown.length > 0 && shown.every(isEnded));
  const scoped = $derived(shown.find((c) => c.id === view.scope));
  const outlives = $derived(!!scoped && isEnded(scoped) && (scoped.commandsRun ?? 0) > 0);
</script>

<div class="contents" data-action-zone>
  <div class="cv-tray-row cv-sb grid min-h-0 grid-rows-[minmax(0,1fr)]"><Tray {sessionId} {covered} /></div>
  <div class="cv-desk-row cv-sb">
  <div class="cv-band cv-dock">
  <div class="cv-desk grid max-h-[70cqh] min-w-0 gap-2 overflow-y-auto py-3">
    {#if session?.goalId}
      <!-- A goal's controller owns delivery, and a queued message would never flush. See section
           "Goal sessions" in docs/unified-session.md. -->
      <div class="grid h-7 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2 text-sm text-faint" data-goal-line>
        <span class="grid w-3.5" title={glyph?.label}>{#if glyph}<StateGlyph label={glyph.label} glyph={glyph.glyph} cls={glyph.cls} mono={glyph.mono} />{/if}</span>
        <p class="min-w-0 truncate">A goal runs this session.</p>
        <Button variant="secondary" size="xs" onclick={() => session && nav.goDetail(session.project)}>Open goal</Button>
      </div>
    {:else if view.handback}
      <SSHHandback {sessionId} />
    {:else if noneLeft}
      <SSHEnded {sessionId} />
    {:else}
      {#if outlives}<p class="text-sm text-faint" data-ended-note>{SSH_ENDED_OUTLIVES}</p>{/if}
      <FirstOpen {sessionId} {covered} />
      <QueueRows {sessionId} />
      <Composer {sessionId}  />
    {/if}
  </div>
  </div>
  </div>
</div>
