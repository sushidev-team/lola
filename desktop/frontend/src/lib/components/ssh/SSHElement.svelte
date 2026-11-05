<script lang="ts">
  // Everything an agent publishes: bare text, in its voice. Kinds differ by a quiet
  // lead word and by small structure, never by a box. Nothing here may draw a plate
  // or a solid marker, and a green tick is a daemon's word, so nothing here is green.
  //
  // Every string in an element is a CLAIM the daemon has only sanitized. It renders
  // as text nodes through SSHText. An evidence plate is the one recorded thing in
  // here, and a proposal's dashed box says "not on the record yet".
  import { tick } from "svelte";
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import {
    agentName,
    hostName,
    hypothesisView,
    severityView,
    stepView,
    type SSHElement,
    isAnswer,
  } from "$lib/ssh";
  import { agoPhrase } from "$lib/time";
  import Button from "../Button.svelte";
  import SSHText from "./SSHText.svelte";
  import SSHRich from "./SSHRich.svelte";
  import SSHWidgetRow from "./SSHWidgetRow.svelte";
  import SSHEvidence from "./SSHEvidence.svelte";
  import SSHApproval from "./SSHApproval.svelte";

  let { sessionId, element }: { sessionId: string; element: SSHElement } = $props();

  const session = $derived(store.sessionById(sessionId));
  const agent = $derived(agentName(session?.agent));
  const view = $derived(ssh.view(sessionId));
  const conn = $derived(view.connections.find((c) => c.id === element.conn) ?? view.active());
  const host = $derived(conn ? hostName(conn) : "");

  // Body is folded behind "More" unless the kind reads best with it open.
  const openByDefault = $derived(["summary", "narration", "recommendation"].includes(element.kind));
  let more = $state(false);
  const hasBody = $derived(!!element.body);
  const showBody = $derived(hasBody && (openByDefault || more));

  const known = $derived(
    ["summary", "narration", "hypothesis", "finding", "plan", "metric", "evidence", "recommendation", "proposal", "question", "widget"].includes(element.kind),
  );

  // --- proposal ---
  let checking = $state(false);
  let checkEl = $state<HTMLDivElement | null>(null);
  let rootEl = $state<HTMLDivElement | null>(null);
  let declining = $state(false);
  let declineText = $state("");
  // The revision the human read. Anything else means the words moved under them.
  let drawn = $state(0);
  let moved = $state(false);
  $effect(() => {
    const r = element.revision;
    if (checking && r !== drawn) {
      checking = false;
      moved = true;
    }
  });
  function startCheck() {
    moved = false;
    drawn = element.revision;
    checking = true;
    // The human just asked for this card, so it comes to them: its confirm and its
    // keys are useless below the fold or without focus.
    void tick().then(() => {
      const card = checkEl?.querySelector<HTMLElement>("[data-card]");
      card?.scrollIntoView?.({ block: "nearest" });
      card?.focus();
    });
  }
  function cancelCheck() {
    checking = false;
    void tick().then(() => rootEl?.querySelector<HTMLElement>("[data-proposal-run]")?.focus());
  }
  const movedText = $derived(`${agent} changed this proposal. Read it again.`);
  async function runProposal() {
    checking = false;
    await ssh.proposal(sessionId, element, "run", "", () => (moved = true));
  }
  async function decline() {
    const reason = declineText.trim();
    declining = false;
    declineText = "";
    await ssh.proposal(sessionId, element, "decline", reason, () => (moved = true));
  }

  // --- question ---
  let answer = $state("");
  let questionMoved = $state(false);
  async function sendAnswer(text: string) {
    const t = text.trim();
    if (!t) return;
    answer = "";
    questionMoved = false;
    await ssh.answer(sessionId, element, t, () => (questionMoved = true));
  }

  function copy(text: string) {
    void navigator.clipboard?.writeText(text);
  }
</script>

{#snippet lead(word: string, cls = "text-faint")}
  <span class="mr-1.5 text-sm {cls}">{word}</span>
{/snippet}

{#snippet updated()}
  {#if element.revision > 1}
    <span
      class="ml-1.5 text-sm whitespace-nowrap text-faint"
      title="{agent} changed this {element.revision} times. Lola keeps only the latest version."
      >updated {agoPhrase(element.updatedAt)}</span
    >
  {/if}
{/snippet}

{#snippet bodyOf()}
  {#if showBody}
    <div class="copy mt-1 text-ink/85"><SSHText {sessionId} text={element.body} /></div>
  {:else if hasBody}
    <Button size="xs" class="mt-0.5 -ml-1.5" aria-expanded={more} onclick={() => (more = true)}>
      <span aria-hidden="true">▸</span> More
    </Button>
  {/if}
{/snippet}

{#snippet plates()}
  {#if element.evidence?.length}
    <div class="mt-1 flex flex-wrap items-center gap-1">
      {#each element.evidence as ev (ev.block)}
        <SSHEvidence {sessionId} block={ev.block} verified={ev.verified} status={ev.status ?? ""} exitCode={ev.exitCode} />
      {/each}
    </div>
  {/if}
{/snippet}

<span class="sr-only">{agent} says</span>
<div bind:this={rootEl} data-claim={element.kind} class="selectable min-w-0">
  {#if !known}
    <div class="text-ink"><SSHText {sessionId} text={element.title} /></div>
    {#if element.body}<div class="copy mt-1 text-ink/85"><SSHText {sessionId} text={element.body} /></div>{/if}
  {:else if element.kind === "summary"}
    <div class="text-ink"><SSHRich {sessionId} text={element.title} />{@render updated()}</div>
    {@render bodyOf()}
    {@render plates()}
  {:else if element.kind === "widget"}
    <SSHWidgetRow {sessionId} {element} />
  {:else if element.kind === "narration"}
    <!-- A status line stays quiet. An answer, the thing someone was waiting to read, does not. -->
    {@const answer = isAnswer(element.title) || !!element.body}
    {#if answer}
      <div class="w-fit max-w-[80%] rounded-lg rounded-bl-sm border border-magenta/30 bg-magenta/8 px-3 py-2.5 text-ink" data-answer>
        <div class="mb-1 text-sm font-medium text-magenta">{agent}</div>
        <SSHRich {sessionId} text={element.title} />{@render updated()}
        {#if element.body}<div class="mt-1.5"><SSHRich {sessionId} text={element.body} /></div>{/if}
      </div>
    {:else}
      <div class="text-ink/75"><SSHRich {sessionId} text={element.title} />{@render updated()}</div>
    {/if}
    {@render plates()}
  {:else if element.kind === "hypothesis"}
    {@const h = hypothesisView(element.state)}
    <div class="copy">
      {@render lead("Theory")}<span class={h.titleCls}><SSHText {sessionId} text={element.title} /></span>
      <span class="ml-1.5 text-sm whitespace-nowrap {h.wordCls}">{h.word}</span>{@render updated()}
    </div>
    {@render bodyOf()}
    {@render plates()}
  {:else if element.kind === "finding"}
    {@const v = severityView(element.severity)}
    <div class="copy">
      {@render lead(v.word, v.text)}<span class="text-ink"><SSHText {sessionId} text={element.title} /></span>{@render updated()}
    </div>
    {@render bodyOf()}
    {@render plates()}
  {:else if element.kind === "plan"}
    <div class="copy">{@render lead("Plan")}<span class="text-ink"><SSHText {sessionId} text={element.title} /></span>{@render updated()}</div>
    <ol class="mt-1.5 grid gap-1">
      {#each element.steps ?? [] as step, i (i)}
        {@const s = stepView(step.state)}
        <li class="grid grid-cols-[14px_minmax(0,1fr)] items-baseline gap-1.5">
          <span class="translate-y-px justify-self-center">
            <span class="inline-block h-2 w-2 rounded-full border {s.ring}" aria-hidden="true"></span>
          </span>
          <span class="{s.text} copy"><SSHText {sessionId} text={step.text} />{s.suffix}</span>
        </li>
      {/each}
    </ol>
    {@render bodyOf()}
    {@render plates()}
  {:else if element.kind === "metric"}
    <div class="copy">{@render lead("Read")}<span class="text-ink"><SSHText {sessionId} text={element.title} /></span>{@render updated()}</div>
    <dl
      class="mt-1 grid w-fit grid-cols-[auto_auto] gap-x-4 gap-y-0.5"
      title="{agent} copied these from its commands. Check the linked output to confirm."
    >
      {#each element.metrics ?? [] as m, i (i)}
        <dt class="text-faint">{m.name}</dt>
        <dd class="num text-ink">{m.value}{#if m.unit}<span class="ml-1 text-faint">{m.unit}</span>{/if}</dd>
      {/each}
    </dl>
    {@render bodyOf()}
    {@render plates()}
  {:else if element.kind === "evidence"}
    <div class="copy text-ink"><SSHText {sessionId} text={element.title} />{@render updated()}</div>
    {@render bodyOf()}
    {@render plates()}
  {:else if element.kind === "recommendation"}
    <div class="copy">{@render lead("Suggests")}<span class="text-ink"><SSHText {sessionId} text={element.title} /></span>{@render updated()}</div>
    {@render bodyOf()}
    {@render plates()}
  {:else if element.kind === "proposal"}
    {@const decided = element.state === "accepted" || element.state === "declined"}
    <div class="copy">
      {@render lead("Proposed")}<span class="text-ink"><SSHText {sessionId} text={element.title} /></span>{@render updated()}
    </div>
    <!-- Dashed: not on the record yet. Solid plates are the record. -->
    <div
      data-proposal-command
      class="mt-1.5 rounded border border-dashed border-edge px-2.5 py-1.5 font-mono text-sm whitespace-pre-wrap break-all {element.state === 'declined'
        ? 'text-faint line-through decoration-edge'
        : 'text-ink'}"
    >
      <span class="mr-[1ch] text-faint" aria-hidden="true">$</span>{element.command}{#if element.cwd}<div class="text-faint no-underline">in {element.cwd}</div>{/if}
    </div>
    {@render bodyOf()}
    {@render plates()}
    {#if moved}<p class="mt-1.5 text-sm text-warn" role="alert">{movedText}</p>{/if}
    {#if element.state === "accepted"}
      <div class="mt-1.5 flex items-center gap-2 text-sm text-faint">
        You ran this
        {#if element.outcome?.block}<SSHEvidence {sessionId} block={element.outcome.block} verified={true} />{/if}
      </div>
    {:else if element.state === "declined"}
      <div class="mt-1.5 text-sm text-faint">You declined this</div>
      {#if element.outcome?.text}
        <div class="mt-1 rounded-md bg-sel/50 px-2.5 py-1.5">
          <span class="text-sm text-accent-ink">You</span>
          <span class="copy ml-1.5 text-ink">{element.outcome.text}</span>
        </div>
      {/if}
    {:else if checking && conn}
      <div class="mt-2" bind:this={checkEl}>
        <SSHApproval
          {sessionId}
          {conn}
          proposal={{ command: element.command ?? "", cwd: element.cwd ?? "", revision: element.revision }}
          onConfirm={runProposal}
          onCancel={cancelCheck}
        />
      </div>
    {:else if declining}
      <div class="mt-1.5 grid gap-1.5">
        <input
          class="w-full rounded border border-edge bg-canvas px-2 py-1 text-ink outline-none placeholder:text-placeholder focus:border-accent"
          placeholder="Tell {agent} why (optional)"
          aria-label="Reason for declining"
          bind:value={declineText}
          onkeydown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              void decline();
            } else if (e.key === "Escape") {
              e.preventDefault();
              e.stopPropagation();
              declining = false;
            }
          }}
        />
        <div class="flex gap-1.5">
          <Button size="xs" onclick={() => (declining = false)}>Back</Button>
          <Button variant="danger" size="xs" onclick={decline}>Decline</Button>
        </div>
      </div>
    {:else if !decided}
      <div class="mt-1.5 flex flex-wrap items-center gap-1.5">
        <Button variant="secondary" size="xs" disabled={!conn} data-proposal-run onclick={startCheck}>Run on {host}…</Button>
        <Button variant="danger" size="xs" onclick={() => (declining = true)}>Decline…</Button>
        <Button size="xs" onclick={() => copy(element.command ?? "")}>Copy</Button>
      </div>
    {/if}
  {:else if element.kind === "question"}
    <div class="copy">{@render lead("Asks")}<span class="text-ink"><SSHText {sessionId} text={element.title} /></span>{@render updated()}</div>
    {@render bodyOf()}
    {@render plates()}
    {#if element.state === "answered"}
      <div class="mt-1.5 rounded-md bg-sel/50 px-2.5 py-1.5">
        <span class="text-sm text-accent-ink">You</span>
        <span class="copy ml-1.5 text-ink">{element.outcome?.text}</span>
      </div>
    {:else}
      {#if questionMoved}<p class="mt-1.5 text-sm text-warn" role="alert">{agent} changed this question. Read it again.</p>{/if}
      <div class="mt-1.5 flex flex-wrap items-center gap-1.5">
        {#each element.choices ?? [] as c (c)}
          <Button variant="secondary" size="sm" onclick={() => sendAnswer(c)}>{c}</Button>
        {/each}
        <input
          class="min-w-48 flex-1 rounded border border-edge bg-canvas px-2 py-1 text-ink outline-none placeholder:text-placeholder focus:border-accent"
          placeholder="Answer in your own words…"
          aria-label="Answer in your own words"
          bind:value={answer}
          onkeydown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              void sendAnswer(answer);
            }
          }}
        />
      </div>
    {/if}
  {/if}
</div>
