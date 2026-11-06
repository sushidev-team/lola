<script lang="ts">
  // The one home for "needs you", above the composer. One card at a time, oldest first, and the
  // order never changes because something new arrived.
  //
  // It never takes focus by appearing, so a keystroke meant for the composer or a terminal cannot
  // land on a card. Only ⌘J, ⌘⇧A, `A`, `t`, Tab from an empty composer or a click gives one focus.
  // A short line goes to a status region, because a card nobody can hear is a card nobody knows of.
  //
  // Every kind keeps its own rules. A password card stays its own component and is never mixed
  // with an agent's prompt, whose words a process chose.
  import { onDestroy, untrack } from "svelte";
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { talk } from "$lib/talk.svelte";
  import { agentName } from "$lib/ssh";
  import { tierOneCount, trayAnnouncement, trayCards } from "$lib/composer";
  import { widthOf } from "./measure";
  import Button from "../Button.svelte";
  import DevClashBanner from "../DevClashBanner.svelte";
  import SSHApproval from "../ssh/SSHApproval.svelte";
  import SSHConnCard from "../ssh/SSHConnCard.svelte";
  import SSHElement from "../ssh/SSHElement.svelte";
  import SSHGoCard from "../ssh/SSHGoCard.svelte";
  import SSHPromptCard from "../ssh/SSHPromptCard.svelte";
  import ModalCard from "./ModalCard.svelte";
  import PermissionCard from "./PermissionCard.svelte";
  import QuestionCard from "./QuestionCard.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const agent = $derived(agentName(session?.agent));
  const cards = $derived(
    trayCards({
      connections: view.connections,
      items: view.items,
      activeConn: view.activeConn,
      answered: view.answeredPrompts,
      agentPrompt: talk.state(sessionId)?.prompt ?? null,
    }),
  );
  const first = $derived(tierOneCount(cards));
  // ‹ › stands on a card until it goes. A card that went leaves the tray on the oldest.
  const at = $derived(Math.max(cards.findIndex((c) => c.id === view.trayCard), 0));
  const card = $derived(cards[at]);
  const counted = $derived(card?.tier === 1 ? first : cards.length - first);
  const position = $derived(card?.tier === 1 ? at + 1 : at - first + 1);

  function step(dir: 1 | -1) {
    const from = card?.tier === 1 ? 0 : first;
    const to = from + ((position - 1 + dir + counted) % counted);
    view.trayCard = cards[to]?.id ?? "";
  }

  let root = $state<HTMLDivElement | null>(null);

  // A request can come before the card it is about: a jump lands here on a session nobody was
  // polling, whose prompt is one round trip away. So it waits for this session's next talk and
  // timeline answers, and only that long. A card that showed up minutes later would otherwise
  // take the keyboard by appearing, under someone typing, and it never may. With no card by then
  // the keyboard goes to the composer rather than to a wrapper where letters fire bare keys.
  const CARD_WAIT_MS = 2000;
  let waitingFor: { n: number; talk: number; timeline: number } | null = null;
  let giveUp: ReturnType<typeof setTimeout> | undefined;
  onDestroy(() => clearTimeout(giveUp));

  function settleOnComposer(n: number) {
    clearTimeout(giveUp);
    waitingFor = null;
    if (view.seen.dock === n) return;
    view.seen.dock = n;
    untrack(() => view.composerFocus++);
  }

  $effect(() => {
    const n = view.dockFocus;
    const present = !!card;
    const answers = { talk: talk.answerCount(sessionId), timeline: view.fetches };
    if (n === view.seen.dock || !root) return;
    if (present) {
      clearTimeout(giveUp);
      waitingFor = null;
      view.seen.dock = n;
      // The CARD takes focus, not this wrapper: its keys live on it. An answered card unmounts and
      // focus goes with it, so a held key cannot walk the queue.
      (root.querySelector<HTMLElement>("[data-card][tabindex]") ?? root).focus();
      return;
    }
    if (waitingFor?.n !== n) {
      waitingFor = { n, ...answers };
      clearTimeout(giveUp);
      giveUp = setTimeout(() => settleOnComposer(n), CARD_WAIT_MS);
      untrack(() => {
        void talk.refresh(sessionId);
        void view.fetch();
      });
      return;
    }
    if (answers.talk > waitingFor.talk && answers.timeline > waitingFor.timeline) settleOnComposer(n);
  });

  // Gone once its connection has its next question, or a sign-in has landed.
  const promptNote = $derived.by(() => {
    const n = view.promptNote;
    if (!n) return "";
    const c = view.connections.find((x) => x.id === n.conn);
    if (c?.prompt && !view.answeredPrompts.has(c.prompt.id)) return "";
    if (n.signIn && c?.state !== "connecting" && c?.state !== "authenticating") return "";
    return n.text;
  });

  // Esc from any card goes back to the composer. A field on a card handles its own first.
  function onKey(e: KeyboardEvent) {
    if (e.key !== "Escape" || !card) return;
    e.preventDefault();
    e.stopPropagation();
    view.composerFocus++;
  }

  let width = $state(0);
  // The width of the person's column, where cards live, and that tops out near 624.
  const narrow = $derived(width > 0 && width < 480);
</script>

{#snippet pager()}
  {#if counted > 1}
    <span class="flex items-center gap-0.5 text-sm text-faint" data-tray-pager>
      <span class="num whitespace-nowrap">{position} of {counted}</span>
      <Button size="xs" icon class="h-5! w-5!" aria-label="Older card" onclick={() => step(-1)}>‹</Button>
      <Button size="xs" icon class="h-5! w-5!" aria-label="Newer card" onclick={() => step(1)}>›</Button>
    </span>
  {/if}
{/snippet}

<div class="sr-only" role="status" data-tray-status>{trayAnnouncement(cards, agent)}</div>

<div class="cv-band cv-dock min-h-0">
  <div class="cv-dock-rail" aria-hidden="true"><div class="cv-rail-line"></div></div>
  <div class="grid min-h-0 grid-rows-[minmax(0,1fr)] {card || promptNote || session?.devClash ? 'pt-2' : ''}" {@attach widthOf((w) => (width = w))} data-tray>
  <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions, a11y_no_static_element_interactions -->
  <div bind:this={root} tabindex="-1" class="grid min-h-0 gap-2 outline-none {narrow ? 'max-h-[60cqh]' : 'max-h-[45cqh]'}" onkeydown={onKey}>
    {#if card}
      {#key card.id}
        {#if card.kind === "approval"}
          <SSHApproval {sessionId} conn={card.conn} block={card.block} {pager} />
        {:else if card.kind === "go"}
          <SSHGoCard {sessionId} conn={card.conn} {pager} />
        {:else if card.kind === "permission"}
          <PermissionCard {sessionId} prompt={card.prompt} {narrow} {pager} />
        {:else if card.kind === "question"}
          <QuestionCard {sessionId} prompt={card.prompt} {narrow} {pager} />
        {:else if card.kind === "modal"}
          <ModalCard {sessionId} prompt={card.prompt} {pager} />
        {:else if card.kind === "prompt"}
          <div class="min-h-0 overflow-y-auto"><SSHPromptCard {sessionId} conn={card.conn} prompt={card.prompt} {pager} /></div>
        {:else if card.kind === "auth"}
          <SSHConnCard {sessionId} conn={card.conn} kind="auth" {pager} />
        {:else if card.kind === "reconnect"}
          <div class="min-h-0 overflow-y-auto"><SSHConnCard {sessionId} conn={card.conn} kind="reconnect" running={card.running} {pager} /></div>
        {:else if card.kind === "failed"}
          <div class="min-h-0 overflow-y-auto"><SSHConnCard {sessionId} conn={card.conn} kind="failed" {pager} /></div>
        {:else if card.kind === "element"}
          <!-- Neutral: a question the agent posted is its own claim that it wants something. -->
          <section class="grid min-h-0 overflow-y-auto rounded-[10px] border border-edge bg-panel px-3 py-2" data-card={card.element.kind} tabindex="-1" data-tray-claim>
            {#if counted > 1}<div class="justify-self-end">{@render pager()}</div>{/if}
            <SSHElement {sessionId} element={card.element} />
          </section>
        {/if}
      {/key}
    {:else if session?.devClash}
      <DevClashBanner {session} tray />
    {/if}
    {#if promptNote}<p class="text-sm text-faint" role="status" data-prompt-note>{promptNote}</p>{/if}
  </div>
  </div>
</div>
