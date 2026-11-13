<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
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
  import { tick, untrack } from "svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { reviewWorkspace } from "$lib/reviewworkspace.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
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
  import HubChip from "./HubChip.svelte";


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
  const visible = $derived(visibleRoster(store.rosterSessions, store.projects.map(p => p.name), name => store.displayNameFor(name), { query: nav.sidebarQuery, triage: nav.triage, scoped: nav.scoped, project: nav.project }));
  const filteredEnded = $derived(ended.filter(e => nav.triage === 'Ended' && (!nav.scoped || e.project === nav.project) && (!nav.sidebarQuery.trim() || `${e.title} ${e.issue} ${e.id}`.toLowerCase().includes(nav.sidebarQuery.trim().toLowerCase()))));
  let root = $state<HTMLElement>();
  let focusSeen = untrack(() => nav.sidebarFocus);
  $effect(() => {
    const request = nav.sidebarFocus;
    if (request === focusSeen) return;
    focusSeen = request;
    void tick().then(() => {
      const rows = root?.querySelectorAll<HTMLButtonElement>('[data-roster-row]:not([hidden] *), [data-roster-ended]');
      const picked = Array.from(rows ?? []).find(el => el.dataset.rosterRow === nav.selectedId || el.dataset.rosterEnded === nav.selectedId) ?? rows?.[0];
      picked?.focus();
      picked?.scrollIntoView?.({ block: 'nearest' });
      if (!picked) document.querySelector<HTMLInputElement>('[aria-label="Search sessions"]')?.focus();
    });
  });
  function navigate(event: KeyboardEvent) {
    if (isChord(event)) return;
    const buttons = Array.from(root?.querySelectorAll<HTMLButtonElement>('[data-roster-row]:not([hidden] *), [data-roster-ended]') ?? []);
    const at = buttons.indexOf(event.target as HTMLButtonElement);
    if (at < 0) return;
    const next = event.key === 'ArrowDown' ? at + 1 : event.key === 'ArrowUp' ? at - 1 : event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : null;
    if (next == null) return;
    event.preventDefault(); event.stopPropagation();
    const button = buttons[Math.max(0, Math.min(buttons.length - 1, next))];
    if (button) {
      nav.openSession(button.dataset.rosterRow || button.dataset.rosterEnded || '', true);
      void tick().then(() => {
        const target = Array.from(root?.querySelectorAll<HTMLButtonElement>('[data-roster-row]:not([hidden] *), [data-roster-ended]') ?? []).find(el => el.dataset.rosterRow === nav.focusedTerm || el.dataset.rosterEnded === nav.focusedTerm);
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

  let collapsed = $state(new Set<string>());
  function toggleGroup(key: string) {
    const next = new Set(collapsed);
    if (next.has(key)) next.delete(key); else next.add(key);
    collapsed = next;
  }
  // Search opens groups once per query change; a user can still fold results.
  $effect(() => { if (nav.sidebarQuery.trim()) collapsed = new Set(); });

  const LINE_CLS = { orange: "text-orange", bad: "text-bad", faint: "text-faint" };
</script>

<nav bind:this={root} class="sidebar-roster" aria-label="Sessions">
  {#each groups as g (g.key)}
    {#if g.rows.length}
      <section aria-label={g.label}>
        <div class="sidebar-project-heading">
          <h2 class="min-w-0 flex-1">
            <Button variant="bare" size="xs" class="sidebar-group-toggle" aria-expanded={!collapsed.has(g.key)} aria-controls={`roster-group-${g.key}`} onclick={() => toggleGroup(g.key)}>
              <Chevron open={!collapsed.has(g.key)} class="text-faint" />
              <span class="min-w-0 truncate">{g.label}</span><span class="num sidebar-project-count">{g.rows.length}</span>
            </Button>
          </h2>

        </div>
        <div id={`roster-group-${g.key}`} hidden={collapsed.has(g.key)}>
        {#each g.rows as s (s.id)}
          {@const glyph = stateGlyph(s, nav.seenAt(s.id))}
          {@const line = rosterActivity(s, now, !!talk.state(s.id)?.queueHeld || lastTurnStopped(views.get(s.id)?.items))}
          {@const stage = stageFor(s.delivery, s.reacting)}
          {@const atReview = s.delivery === 'review_pending' && (glyph.label === 'Resting' || glyph.label === 'Finished a turn')}
          {@const mark = s.delivery === 'merged' ? { glyph: 'done', label: 'Merged', cls: 'text-good' } : atReview ? { glyph: 'review', label: 'Awaiting review', cls: 'text-magenta' } : glyph}
          {@const metadataStage = atReview ? null : stage}
          {@const title = s.title || s.branch || s.issue || (s.status === 'shell' ? 'Shell' : 'Session')}
          {@const issue = s.issue && s.issue !== title ? s.issue : ''}
          {@const context = !s.issue && !stage ? (s.location === 'ssh' ? 'SSH' : s.location === 'temporary' ? 'Temporary shell' : s.branch !== title ? s.branch : '') : ''}
          {@const hasMeta = !!(issue || metadataStage || context || s.hub)}
          {@const waited = glyph.glyph === "◉" ? fluidAgo(s.agentStateSince as unknown as string, now).text : ""}
          <div class="sidebar-session-item" class:selected={open === s.id} class:has-pr={!!s.prNumber} class:dev-active={!!s.devActive} class:compact={!hasMeta || line.compact} class:dormant={glyph.label === 'Gone' || glyph.label === 'Orphaned'}>
          <button
            type="button"
            class="sidebar-session-row"
            aria-current={open === s.id ? "true" : undefined}
            use:tooltip={{ text: [s.title || s.issue || s.id, mark.label, stage?.label, line.detail].filter(Boolean).join("\n") }}
            onkeydown={navigate}
            data-roster-row={s.id}
            onclick={() => nav.openSession(s.id)}
            oncontextmenu={(e) => { nav.select(s.id); sessionMenu.open(s.id, e); }}
          >
            <span class="sidebar-state-mark row-span-3 self-start" role="img" aria-label={mark.label}><StateGlyph class="sidebar-status-icon" glyph={mark.glyph} label={mark.label} cls={mark.cls} /></span>
            <span class="sidebar-session-title flex min-w-0 items-baseline gap-1.5">
              <span class="min-w-0 truncate text-ink">{title}</span>
              {#if views.get(s.id)?.hasDraft}<span class="shrink-0 text-faint" aria-label="Has a draft" data-draft-mark><Icon name="pencil" size={11} /></span>{/if}
            </span>
            <span class="sidebar-session-age num text-faint">{waited}</span>
            {#if hasMeta}
            <span class="sidebar-session-meta col-start-2 col-end-4 flex min-w-0 items-baseline gap-1.5" data-roster-meta>
              {#if s.hub}<HubChip hub={s.hub} class="max-w-[60%]" />{/if}
              {#if issue}<span class="shrink-0 text-ink-2">{issue}</span>{/if}
              {#if metadataStage}<span class="min-w-0 truncate {STAGE_TEXT[metadataStage.tone]}">{issue ? '· ' : ''}{metadataStage.label}</span>{:else if context}<span class="truncate text-faint">{context}</span>{/if}
              {#if line.compact && !atReview && line.text}<span class="min-w-0 truncate text-faint" data-roster-line>· {line.text}</span>{/if}
            </span>
            {/if}
            {#if !line.compact || !hasMeta}
            <span class="sidebar-session-activity col-start-2 col-end-4 min-w-0 truncate {line.tone === 'faint' && open === s.id ? 'text-ink-2' : LINE_CLS[line.tone]}" data-roster-line>{line.text}</span>
            {/if}
          </button>
          {#if s.devActive}<span class="sidebar-dev-indicator" role="img" aria-label="Dev active" use:tooltip={{ text: 'Dev active' }}></span>{/if}
          <Button icon size="xs" class="sidebar-session-menu" aria-label="Actions for {s.title || s.issue || s.id}" aria-haspopup="menu" aria-expanded={sessionMenu.request?.id === s.id}
            title="Session actions" onclick={e => {
              const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
              nav.select(s.id);
              sessionMenu.open(s.id, e);
              if (e.detail === 0) sessionMenu.request = { id: s.id, x: rect.right, y: rect.bottom };
            }}>
            <Icon name="ellipsis" size={14} />
          </Button>
          {#if s.prNumber}
            <div class="sidebar-session-pr {stage ? STAGE_TEXT[stage.tone] : 'text-faint'}" data-roster-pr>
              {#if atReview && !s.prStale}
                <Button variant="primary" size="xs" class="sidebar-review-link" aria-label="Review PR #{s.prNumber}"
                  title="Review PR #{s.prNumber}" onclick={() => reviewWorkspace.open(s.id)}>Review</Button>
              {:else if s.prUrl}
                <Button variant="bare" size="xs" class="sidebar-pr-link" aria-label="Open pull request #{s.prNumber}"
                  title="Open pull request #{s.prNumber}{stage ? ` · ${stage.label}` : ''}"
                  onclick={() => store.openURL(s.prUrl)}>
                  <Icon name="git-pull-request" size={14} />
                  <span class="num">#{s.prNumber}</span>
                </Button>
              {:else}<span class="sidebar-pr-label num">#{s.prNumber}</span>{/if}
            </div>
          {/if}
          </div>
        {/each}
        </div>
      </section>
    {/if}
  {/each}

  {#if filteredEnded.length}
    <section aria-label="Ended">
        {#each filteredEnded as e (e.id)}
          {@const merged = (e.prNumber ?? 0) > 0 && e.reason === `PR #${e.prNumber} merged`}
          <button
            type="button"
            class="grid h-11 w-full grid-cols-[16px_minmax(0,1fr)] grid-rows-[18px_16px] content-center items-baseline gap-x-1.5 rounded-md px-2 text-left cursor-pointer {open === e.id ? 'bg-sel' : 'hover:bg-sel/50'}"
            aria-current={open === e.id ? "true" : undefined}
            title={e.title || e.id}
            onkeydown={navigate}
            data-roster-ended={e.id}
            onclick={() => nav.openSession(e.id)}
          >
            <span class="self-center" role="img" aria-label={merged ? 'Merged' : 'Session closed'}><StateGlyph class="sidebar-status-icon" glyph={merged ? 'done' : '✕'} /></span>
            <span class="min-w-0 truncate text-ink-2">{e.title || e.issue || e.id}</span>
            <span class="col-start-2 min-w-0 truncate text-sm text-faint">{endedLine(e, now)}</span>
          </button>
        {/each}
    </section>
  {/if}
  {#if !visible.length && !filteredEnded.length}
    <p class="px-2 py-4 text-sm text-faint" role="status">{nav.sidebarQuery || nav.scoped ? 'No sessions match these filters.' : nav.triage === 'Ended' ? 'No ended sessions.' : nav.triage ? 'No sessions need you.' : 'No sessions yet. Start one with +.'}</p>
  {/if}
</nav>
