<script lang="ts">
  // Every session in one stable list. Nothing moves by itself: the order is config project order,
  // then when a session first appeared, then its id, and never state or recency, so a row stays
  // where you learned it was. store.sessions sorts attention first, which is why this does not
  // use it.
  //
  // A row is a card-shaped nav row, hand-rolled like NavRow: 70px, three lines, no border. A click
  // opens the session, and never closes one: nav.openSession is idempotent.
  //
  // Reads the store itself, as a leaf: a container computing rows and passing them down does not
  // re-render on the daemon push in the production WKWebView.
  import { onMount, tick, untrack } from "svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { openShell } from "$lib/projectActions";
  import { endedLine, lastTurnStopped, rosterGroups, stateGlyph } from "$lib/sessionview";
  import { visibleRoster, rosterActivity } from "$lib/roster";
  import { stageFor } from "$lib/stage";
  import { isChord } from "$lib/keys";
  import { tooltip } from "$lib/tooltip";
  import { talk } from "$lib/talk.svelte";
  import { fluidAgo } from "$lib/time";
  import Button from "./Button.svelte";
  import Chevron from "./Chevron.svelte";
  import StateGlyph from "./StateGlyph.svelte";

  onMount(() => {
    void ssh.loadHosts();
  });

  // Asked again whenever a session leaves the list, since that is when one becomes ended, and now
  // and then for the ones that age out. A respawned id is live again, so it is not listed twice.
  const liveCount = $derived(store.rosterSessions.length);
  // Made here and read by the rows, because a view created by the reaction reading it is not
  // tracked by that reaction, and a row would then not see its own draft until the next push.
  const views = $derived(new Map(store.rosterSessions.map((s) => [s.id, ssh.view(s.id)])));
  $effect(() => {
    liveCount;
    void talk.loadEnded();
    const t = setInterval(() => void talk.loadEnded(), 60_000);
    return () => clearInterval(t);
  });
  const ended = $derived(talk.endedList().filter((e) => !store.sessionById(e.id)));
  let endedPicked = $state(false);
  // The story on show is always visible in the list, whoever opened it.
  const endedOpen = $derived(endedPicked || ended.some((e) => e.id === open));

  const visible = $derived(visibleRoster(store.rosterSessions, store.projects.map(p => p.name), name => store.displayNameFor(name), { query: nav.sidebarQuery, triage: nav.triage, scoped: nav.scoped, project: nav.project }));
  const filteredEnded = $derived(ended.filter(e => !nav.triage && (!nav.scoped || e.project === nav.project) && (!nav.sidebarQuery.trim() || `${e.title} ${e.issue} ${e.id}`.toLowerCase().includes(nav.sidebarQuery.trim().toLowerCase()))));
  let root = $state<HTMLElement>();
  let focusSeen = untrack(() => nav.sidebarFocus);
  $effect(() => {
    const request = nav.sidebarFocus;
    if (request === focusSeen) return;
    focusSeen = request;
    void tick().then(() => {
      const rows = root?.querySelectorAll<HTMLButtonElement>('[data-roster-row], [data-roster-ended]');
      const picked = Array.from(rows ?? []).find(el => el.dataset.rosterRow === nav.selectedId || el.dataset.rosterEnded === nav.selectedId) ?? rows?.[0];
      picked?.focus();
      picked?.scrollIntoView?.({ block: 'nearest' });
      if (!picked) document.querySelector<HTMLInputElement>('[aria-label="Search sessions"]')?.focus();
    });
  });
  function navigate(event: KeyboardEvent) {
    if (isChord(event)) return;
    const buttons = Array.from(root?.querySelectorAll<HTMLButtonElement>('[data-roster-row], [data-roster-ended]') ?? []);
    const at = buttons.indexOf(event.target as HTMLButtonElement);
    if (at < 0) return;
    const next = ['ArrowDown', 'j'].includes(event.key) ? at + 1 : ['ArrowUp', 'k'].includes(event.key) ? at - 1 : ['Home', 'g'].includes(event.key) ? 0 : ['End', 'G'].includes(event.key) ? buttons.length - 1 : null;
    if (next == null) return;
    event.preventDefault(); event.stopPropagation();
    const button = buttons[Math.max(0, Math.min(buttons.length - 1, next))];
    if (button) {
      nav.openSession(button.dataset.rosterRow || button.dataset.rosterEnded || '', true);
      void tick().then(() => {
        const target = Array.from(root?.querySelectorAll<HTMLButtonElement>('[data-roster-row], [data-roster-ended]') ?? []).find(el => el.dataset.rosterRow === nav.focusedTerm || el.dataset.rosterEnded === nav.focusedTerm);
        target?.focus(); target?.scrollIntoView?.({ block: 'nearest' });
      });
    }
  }
  const STAGE_TEXT = { bad: 'text-bad', orange: 'text-orange', warn: 'text-warn', info: 'text-info', good: 'text-good', done: 'text-magenta', neutral: 'text-faint' };
  const groups = $derived(
    rosterGroups(
      visible,
      store.projects.map((p) => p.name),
      (name) => store.displayNameFor(name),
    ),
  );
  // Line 2 and the ages are minutes old at their freshest, so the shared one-second clock is more
  // than they need, but it costs nothing extra to read and it stops when nobody does.
  const now = $derived(sshClock.now);
  const open = $derived(nav.focusedTerm || nav.selectedId);
  const hasHosts = $derived(ssh.hosts.length > 0);

  const LINE_CLS = { orange: "text-orange", bad: "text-bad", faint: "text-faint" };
</script>

<nav bind:this={root} class="grid min-w-0 gap-3 px-2 py-2" aria-label="Sessions">
  {#each groups as g (g.key)}
    {@const showEmpty = !nav.sidebarQuery && !nav.triage && !nav.scoped && (g.kind === "temporary" || (g.kind === "ssh" && hasHosts))}
    {#if g.rows.length || showEmpty}
      <section aria-label={g.label}>
        <div class="flex h-6 items-center justify-between px-2">
          <h2 class="label min-w-0 truncate text-faint">{g.label} <span class="num">{g.rows.length || ''}</span></h2>
          {#if g.kind === "temporary"}
            <Button size="xs" icon class="h-5! w-5!" aria-label="New temporary shell" title="New temporary shell (⌘⇧N)" onclick={() => void openShell()}>+</Button>
          {:else if g.kind === "ssh"}
            <Button size="xs" icon class="h-5! w-5!" aria-label="New SSH session" title="New SSH session (⌘⇧H)" onclick={() => ssh.newSession()}>+</Button>
          {/if}
        </div>
        {#each g.rows as s (s.id)}
          {@const glyph = stateGlyph(s, nav.seenAt(s.id))}
          {@const line = rosterActivity(s, now, !!talk.state(s.id)?.queueHeld || lastTurnStopped(views.get(s.id)?.items))}
          {@const stage = stageFor(s.delivery, s.reacting)}
          {@const waited = glyph.glyph === "◉" ? fluidAgo(s.agentStateSince as unknown as string, now).text : ""}
          <button
            type="button"
            class="sidebar-session-row {open === s.id ? 'bg-sel' : 'hover:bg-sel/50'}"
            aria-current={open === s.id ? "true" : undefined}
            use:tooltip={{ text: [s.title || s.issue || s.id, glyph.label, stage?.label, line.text].filter(Boolean).join("\n") }}
            onkeydown={navigate}
            data-roster-row={s.id}
            onclick={() => nav.openSession(s.id)}
            oncontextmenu={(e) => { nav.select(s.id); sessionMenu.open(s.id, e); }}
          >
            <span class="row-span-3 self-start pt-0.5" aria-label={glyph.label}><StateGlyph glyph={glyph.glyph} cls={glyph.cls} mono={glyph.mono} /></span>
            <span class="flex min-w-0 items-baseline gap-1.5">
              <span class="min-w-0 truncate text-ink">{s.title || s.branch || s.issue || (s.status === "shell" ? "Shell" : "Session")}</span>
              {#if views.get(s.id)?.hasDraft}<span class="shrink-0 text-faint" aria-label="Has a draft" data-draft-mark>✎</span>{/if}
            </span>
            <span class="num text-sm text-faint">{waited}</span>
            <span class="col-start-2 col-end-4 flex min-w-0 items-baseline gap-1.5 text-sm" data-roster-meta>
              {#if s.issue}<span class="shrink-0 text-ink-2">{s.issue}</span>{/if}
              {#if s.prNumber}<span class="shrink-0 text-faint">{s.issue ? '· ' : ''}#{s.prNumber}</span>{/if}
              {#if stage}<span class="min-w-0 truncate {STAGE_TEXT[stage.tone]}">{s.issue || s.prNumber ? '· ' : ''}{stage.label}</span>{:else}<span class="truncate text-faint">{s.location === 'ssh' ? 'SSH' : s.location === 'temporary' ? 'Temporary shell' : s.branch || glyph.label}</span>{/if}
            </span>
            <span class="col-start-2 col-end-4 min-w-0 truncate text-sm {line.tone === 'faint' && open === s.id ? 'text-ink-2' : LINE_CLS[line.tone]}" data-roster-line>{line.text}</span>
          </button>
        {/each}
      </section>
    {/if}
  {/each}

  {#if filteredEnded.length}
    <section aria-label="Ended">
      <Button size="xs" class="h-6! w-full! justify-start! px-2! text-faint!" aria-expanded={!!(endedOpen || nav.sidebarQuery)} onclick={() => (endedPicked = !endedPicked)} data-ended-toggle>
        <span class="label">Ended <span class="num">{filteredEnded.length}</span></span>
        <Chevron open={!!(endedOpen || nav.sidebarQuery)} />
      </Button>
      {#if endedOpen || nav.sidebarQuery}
        {#each filteredEnded as e (e.id)}
          <button
            type="button"
            class="grid h-11 w-full grid-cols-[16px_minmax(0,1fr)] grid-rows-[18px_16px] content-center items-baseline gap-x-1.5 rounded-md px-2 text-left transition-colors {open === e.id ? 'bg-sel' : 'hover:bg-sel/50'}"
            aria-current={open === e.id ? "true" : undefined}
            title={e.title || e.id}
            onkeydown={navigate}
            data-roster-ended={e.id}
            onclick={() => nav.openSession(e.id)}
          >
            <span class="text-center text-faint" aria-hidden="true">✕</span>
            <span class="min-w-0 truncate text-ink-2">{e.title || e.issue || e.id}</span>
            <span class="col-start-2 min-w-0 truncate text-sm text-faint">{endedLine(e, now)}</span>
          </button>
        {/each}
      {/if}
    </section>
  {/if}
  {#if !visible.length && !filteredEnded.length}
    <p class="px-2 py-4 text-sm text-faint" role="status">{nav.sidebarQuery || nav.triage || nav.scoped ? 'No sessions match these filters.' : 'No sessions yet. Start one with +.'}</p>
  {/if}
</nav>
