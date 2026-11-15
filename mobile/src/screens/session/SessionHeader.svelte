<script lang="ts">
  import { nav as deskNav } from "$lib/nav.svelte";
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { stateGlyph } from "$lib/sessionview";
  import { displayFor } from "$lib/theme";
  import { copy } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";
  import BackIcon from "@mobile/lib/icons/BackIcon.svelte";
  import Icon from "$lib/components/Icon.svelte";

  // The Session screen's 44 pt header (spec §5.2): back, one-line title, the
  // state glyph, and the menu. A session that is not in the store keeps the back
  // button and loses the glyph and the menu, because there is nothing to act on.

  let {
    session,
    id,
    onback,
    onmenu,
  }: {
    session?: SessionInfo;
    id: string;
    onback: () => void;
    onmenu: () => void;
  } = $props();

  const title = $derived(session?.title || session?.issue || id);
  const glyph = $derived(session ? stateGlyph(session, deskNav.seenAt(session.id)) : null);

  // The status dot is read off the (agentState, delivery) PAIR, never the rollup word.
  const dot = $derived.by((): { cls: string; word: string } | null => {
    if (!session) return null;
    const d = session.delivery;
    const display = displayFor(session.agentState);
    if (d === "ci_failed" || d === "merge_conflict" || d === "changes_requested") return { cls: "bg-bad", word: "Needs fixing" };
    if (d === "approved") return { cls: "bg-good", word: "Ready to merge" };
    if (display === "needs_you") return { cls: "bg-orange", word: "Needs you" };
    if (display === "working") return { cls: "bg-accent status-pulse", word: "Working" };
    return { cls: "bg-faint", word: display === "gone" ? "Ended" : display === "shell" ? "Shell" : "Idle" };
  });

  // The back button names where it goes: the route below this one on the stack.
  const backLabel = $derived.by(() => {
    const prev = router.stack[router.stack.length - 2];
    if (!prev || prev.name !== "tab") return "Back";
    if (prev.project) return `Back to ${store.displayNameFor(prev.project)}`;
    const name = { inbox: copy.inbox.title, projects: copy.projects.title, activity: copy.activity.title, settings: copy.settings.title }[prev.tab];
    return `Back to ${name}`;
  });
</script>

<header class="flex h-11 min-w-0 items-center gap-1 border-b border-edge-soft px-1">
  <button
    type="button"
    class="tap grid shrink-0 touch-manipulation place-items-center text-ink active:opacity-70"
    aria-label={backLabel}
    onclick={onback}
  >
    <BackIcon />
  </button>
  {#if dot}
    <span class="h-2 w-2 shrink-0 rounded-full {dot.cls}" role="img" aria-label={glyph?.label || dot.word} data-status-dot></span>
  {/if}
  <h1 class="min-w-0 flex-1 truncate text-headline">{title}</h1>
  {#if session}
    <button
      type="button"
      class="tap grid shrink-0 touch-manipulation place-items-center text-ink active:opacity-70"
      aria-label="Session menu"
      onclick={onmenu}
    >
      <Icon name="ellipsis" size={20} />
    </button>
  {/if}
</header>

<style>
  .status-pulse {
    animation: status-pulse 1.6s ease-in-out infinite;
  }
  @keyframes status-pulse {
    50% {
      opacity: 0.4;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .status-pulse {
      animation: none;
    }
  }
</style>
