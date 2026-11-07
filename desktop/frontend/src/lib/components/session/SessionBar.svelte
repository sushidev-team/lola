<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  // Session identity and actions live here. Delivery details and Dev controls
  // live in the overview; the title hover card keeps checkout and agent facts.
  //
  // A fixed 44px with no wrap, so a chip arriving can never reflow the PTY below. Still the
  // window's drag band: every control here is a real button, which app.css opts out of it.
  //
  // Below 900px of main column the action goes into the session menu.
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { bestOf } from "$lib/bestof.svelte";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import UsageMark from "../UsageMark.svelte";
  import { toMillis } from "$lib/time";
  import { agentName } from "$lib/ssh";
  import { isAgentless } from "$lib/sessionview";
  import { talk } from "$lib/talk.svelte";
  import { widthOf } from "./measure";
  import Button from "../Button.svelte";
  import HoverCard from "../HoverCard.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const session = $derived(store.sessionById(sessionId));
  // A session that left the list has only its record, and nothing here can act on it.
  const ended = $derived(session ? undefined : talk.ended(sessionId));
  const title = $derived(session?.title || session?.branch || session?.issue || ended?.title || ended?.issue || "Session");
  const issue = $derived(session?.issue || ended?.issue || "");
  const agentless = $derived(!!session && isAgentless(session));

  let width = $state(0);
  // 0 means not measured yet, which is the wide layout: never narrow a bar on a guess.
  const narrow = $derived(width > 0 && width < 880);

  const hoverRows = $derived.by<[string, string][]>(() => {
    const s = session;
    if (!s) return [];
    const rows: [string, string][] = [];
    if (!agentless) rows.push(["Agent", agentName(s.agent)]);
    if (s.model) rows.push(["Model", s.model]);
    if (s.branch) rows.push(["Branch", s.branch]);
    if (s.worktree) rows.push([s.location === "main" ? "Checkout" : "Worktree", s.worktree]);
    if (s.sandbox) rows.push(["Sandbox", s.sandbox]);
    if (s.ports?.length) rows.push(["Ports", `:${s.ports.join(", :")}`]);
    if (s.firstSeen && Number.isFinite(toMillis(s.firstSeen as unknown as string))) rows.push(["Started", new Date(toMillis(s.firstSeen as unknown as string)).toLocaleString()]);
    return rows;
  });

  // Conflicts first, because nothing else on this list stops a merge. One at most, never solid.
  // Revive is not here: the footer owns it in every view, composer and strip alike, and it is that
  // state's focal point, so a second one up here would only compete with it.
  const action = $derived.by<{ label: string; run: () => void; loading?: boolean } | null>(() => {
    const s = session;
    if (!s) return null;
    if (s.delivery === "merge_conflict" || s.status === "merge_conflict") {
      return { label: "Resolve conflicts", run: () => store.resolveConflict(s.id), loading: store.resolvePending[s.id] };
    }
    if (s.bestOf && !s.bestOf.winner) return { label: "Compare attempts", run: () => bestOf.open(s.id) };
    return null;
  });

</script>

<div class="grid h-full min-w-0 flex-1 grid-cols-[minmax(0,1fr)_auto] items-center gap-2" {@attach widthOf((w) => (width = w))} data-session-bar>

  <div class="flex min-w-0 items-center gap-3">
    <HoverCard class="flex min-w-0 items-baseline gap-2 outline-none">
      {#snippet card()}
        <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1" data-session-facts>
          {#each hoverRows as [k, v] (k)}
            <dt class="text-faint">{k}</dt>
            <dd class="min-w-0 break-all text-ink">{v}</dd>
          {/each}
        </dl>
      {/snippet}
      {#if issue}<span class="selectable shrink-0 text-sm text-faint">{issue}</span>{/if}
      {#if session}
        <Button variant="bare" size="xs" class="h-auto! min-w-0 shrink! truncate px-0! text-[13px]! font-medium text-ink" title="Rename session" onclick={() => nav.openOverlay("rename-session", sessionId)}>
          <span class="min-w-0 truncate">{title}</span>
        </Button>
      {:else}
        <span class="min-w-0 truncate font-medium text-ink">{title}</span>
      {/if}
    </HoverCard>
    {#if ended}<span class="min-w-0 truncate text-sm text-faint" data-ended-tag>Ended{#if ended.reason}{" · "}{ended.reason}{/if}</span>{/if}


  </div>

  <div class="flex shrink-0 items-center gap-3">
    {#if session?.usage}<UsageMark usage={session.usage} class="text-xs text-faint" />{/if}
    {#if action && !narrow}
      <Button variant="secondary" size="xs" loading={action.loading} onclick={action.run} data-session-action>{action.label}</Button>
    {/if}
    {#if session}<Button icon size="xs" aria-label="Session actions" aria-haspopup="menu" title="Session actions" onclick={(e) => sessionMenu.open(sessionId, e)}><Icon name="ellipsis" size={14} /></Button>{/if}
  </div>
</div>
