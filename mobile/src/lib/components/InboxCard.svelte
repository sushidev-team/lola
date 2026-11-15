<script lang="ts" module>
  import { SvelteSet } from "svelte/reactivity";

  /** Address presses the daemon took, by `<session>#<pr>#<state since>`: one per changes_requested spell. */
  const addressed = new SvelteSet<string>();
</script>

<script lang="ts">
  import { slide } from "svelte/transition";
  import { DaemonService } from "@bindings/desktop";
  import { store } from "$lib/store.svelte";
  import { talk } from "$lib/talk.svelte";
  import { copy } from "@mobile/lib/copy";
  import { isFrameLine } from "$lib/composer";
  import { haptic } from "@mobile/lib/haptics";
  import {
    ageLabel,
    APPROVE_SETTLE_MS,
    metaParts,
    rowAriaLabel,
    approveChoice,
    largeType,
    type CardAction,
    type RowModel,
  } from "@mobile/lib/inbox.svelte";
  import { reducedMotion } from "@mobile/lib/mobiletokens";
  import { router } from "@mobile/lib/router.svelte";
  import { can } from "@mobile/lib/tiers.svelte";
  import StatusDot from "./StatusDot.svelte";

  // A "Needs you" card: the request and exactly one action (spec §5.2).
  //
  // Invariants:
  //  - Approve is offered only while `approveChoice` finds the lone "Yes" in the
  //    prompt of the SAME TalkStateData whose lines are drawn, and the press
  //    carries that state's hash. A new hash disables it for APPROVE_SETTLE_MS.
  //  - The card never types into a pane itself; the daemon re-reads the pane
  //    against the hash.
  //  - The permission well shows the command and nothing else: frame lines and the
  //    "Bash command" label are dropped (ChoiceCard's phoneLines rule). Text the well
  //    clamps stays expandable, so Approve is never pressed on text the person cannot read.
  //  - Address is pressed once per changes_requested spell: a press the daemon
  //    took (delivered or queued) leaves it "Requested", so no second copy of
  //    the instruction is queued.
  //  - The leave transition plays only in an unwindowed list (`animate`), where
  //    a removal is a real resolution and not a row scrolling out of the window.

  let {
    model,
    projectLabel,
    watch = false,
    animate = false,
  }: { model: RowModel; projectLabel: string; watch?: boolean; animate?: boolean } = $props();

  const reduced = reducedMotion();

  $effect(() => largeType.watch());

  $effect(() => {
    if (!watch) return;
    return talk.poll(model.id);
  });

  const talkState = $derived(watch ? talk.state(model.id) : null);
  const prompt = $derived(talkState?.prompt ?? null);
  const yes = $derived(model.waiting && can("agentChoose") ? approveChoice(prompt) : null);
  const action = $derived<CardAction | null>(yes ? "approve" : model.action);

  // "Bash command" labels the code under it and the frame lines are the pane's chrome.
  const lines = $derived.by(() => {
    const all = prompt?.kind === "permission" ? (prompt.lines ?? []) : [];
    const labelAt = all.findIndex((l) => /command$/i.test(l.trim()));
    const out = all.filter((l, i) => i !== labelAt && !isFrameLine(l));
    while (out.length && !out[0].trim()) out.shift();
    while (out.length && !out[out.length - 1].trim()) out.pop();
    return out;
  });
  const wellText = $derived(lines.join("\n"));
  let expanded = $state(false);
  let well = $state<HTMLElement | null>(null);
  let clamped = $state(false);
  $effect(() => {
    // Re-measure when the text, the clamp (large type) or the width changes.
    wellText;
    largeType.value;
    const el = well;
    if (!el || expanded) return;
    const measure = (): void => {
      clamped = el.scrollHeight > el.clientHeight;
    };
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  });
  // The first non-blank line names a command ("Bash command"): the same rule as PermissionCard.
  const asksToRun = $derived(/command/i.test((prompt?.lines ?? []).find((l) => l.trim()) ?? ""));

  // A derived string, so a pushed prompt with the same hash re-runs nothing.
  const hash = $derived(prompt?.hash ?? "");
  let settled = $state(true);
  $effect(() => {
    if (hash === "") return;
    settled = false;
    const t = setTimeout(() => (settled = true), APPROVE_SETTLE_MS);
    return () => clearTimeout(t);
  });

  let busy = $state(false);
  let notice = $state("");
  /** A queued answer is news, not a refusal. */
  let noticeQueued = $state(false);

  const LABEL: Record<CardAction, string> = copy.inbox.actions;
  // A menu or dialog is chosen from; everything else asked is answered.
  const answerLabel = $derived(
    model.session.inputReason === "dialog" || model.session.inputReason === "modal" ? copy.inbox.actions.choose : copy.inbox.actions.answer,
  );
  const kind = $derived(asksToRun && model.state === copy.inbox.kinds.permission ? copy.inbox.kinds.command : model.state);
  const COMMAND: Record<CardAction, string> = {
    approve: "agentChoose",
    answer: "",
    resolve: "resolveConflict",
    checks: "",
    address: "addressPRFeedback",
    merge: "mergePR",
  };
  const allowed = $derived(action !== null && (COMMAND[action] === "" || can(COMMAND[action])));
  const addressKey = $derived(`${model.id}#${model.session.prNumber}#${model.since}`);
  const requested = $derived(action === "address" && addressed.has(addressKey));
  const disabled = $derived(!allowed || busy || requested || (action === "approve" && !settled));

  const age = $derived(ageLabel(model.since, model.ageFallback));
  // "Project · NOR-411 · 22m": the age is the last part and never dropped.
  const parts = $derived(metaParts(model, projectLabel, age));
  const spoken = $derived(rowAriaLabel(model, projectLabel));
  // Permission, question and menu-open are "your turn" (warn); a regression is bad.
  const kindTone = $derived(model.chip === "broken" ? "text-bad" : model.chip === "urgent" ? "text-warn" : model.stateClass);

  function open(seg: "conversation" | "review"): void {
    router.push({ name: "session", id: model.id, seg });
  }

  /** The card's own tap: checks lands on review, everything else on the conversation. */
  const openCard = (): void => open(action === "checks" ? "review" : "conversation");

  async function run(): Promise<void> {
    if (!action || disabled) return;
    notice = "";
    noticeQueued = false;
    if (action === "answer") return open("conversation");
    if (action === "checks") return open("review");
    if (action === "merge") {
      router.push({ name: "session", id: model.id, seg: "review" });
      router.open("merge");
      return;
    }
    busy = true;
    try {
      if (action === "approve" && yes && prompt) {
        haptic("decision");
        const d = await talk.choose(model.id, prompt.hash, yes.key);
        if (!d.sent) notice = d.why ?? "";
        else store.setFlash(`${copy.activity.words.approved} \u00B7 ${model.session.issue || model.title}`, "good");
      } else if (action === "resolve") {
        if ((await store.resolveConflict(model.id)) === undefined) notice = store.flash?.text ?? "";
      } else if (action === "address") {
        const key = addressKey;
        const d = await DaemonService.AddressPRFeedback(model.id, model.session.prNumber);
        if (d?.agentGone) notice = copy.notices.agentGone.body;
        else {
          addressed.add(key);
          if (!d?.delivered) {
            notice = d?.why ?? "";
            noticeQueued = true;
          }
        }
      }
    } catch (err) {
      notice = err instanceof Error ? err.message : String(err);
    } finally {
      busy = false;
    }
  }

  function collapse(node: Element) {
    return slide(node, { duration: animate && !reduced ? 240 : 0 });
  }
</script>

<!-- One tap target: a button covers the card and opens the conversation. The content sits above it
     with pointer events off, so only the action button (and "Show all") take their own taps.
     The covering button carries the whole card as one VoiceOver label; the drawn text is hidden from it. -->
<div class="px-4 py-1.5" out:collapse|global>
  <div class="relative flex flex-col gap-1.5 rounded-card bg-panel px-4 py-3">
    <button
      type="button"
      class="absolute inset-0 touch-manipulation rounded-card active:bg-sel"
      aria-label={spoken}
      onclick={openCard}
    ></button>

    <div class="pointer-events-none relative flex flex-col gap-1">
      <div class="flex items-center gap-1.5 text-subhead {kindTone}" aria-hidden="true">
        <StatusDot tone={kindTone} />
        <span class="min-w-0 truncate">{kind}</span>
      </div>

      <span
        class="min-w-0 text-left text-body font-semibold text-ink {largeType.value ? 'line-clamp-3' : 'line-clamp-2'}"
        aria-hidden="true">{model.title}</span
      >

      {#if lines.length > 0}
        <div class="flex flex-col items-start gap-1">
          <p
            bind:this={well}
            class="w-full rounded-[10px] bg-crust px-3 py-2.5 font-mono text-[13px] leading-[18px] break-words whitespace-pre-wrap text-ink {expanded
              ? ''
              : largeType.value
                ? 'line-clamp-3'
                : 'line-clamp-2'}"
            data-prompt>{wellText}</p
          >
          {#if clamped && !expanded}
            <button
              type="button"
              class="pointer-events-auto text-subhead text-accent"
              onclick={(e) => {
                e.stopPropagation();
                expanded = true;
              }}
            >
              {copy.inbox.showAll}
            </button>
          {/if}
        </div>
      {/if}

      {#if notice}
        {#if noticeQueued}
          <p class="line-clamp-2 text-footnote text-faint" role="status">{notice}</p>
        {:else}
          <p class="line-clamp-2 text-footnote text-bad" role="alert">{notice}</p>
        {/if}
      {:else if action !== null && !allowed}
        <p class="text-footnote text-faint">{copy.notices.noTier.body}</p>
      {/if}

      <div class="flex flex-wrap items-center gap-x-2 gap-y-3">
        <span class="min-w-0 flex-1 text-footnote text-faint {largeType.value ? 'basis-full' : ''}" data-meta aria-hidden="true">
          {#each parts as part, i (i)}{#if i > 0}{" \u00B7 "}{/if}{#if age && i === parts.length - 1}<span class="num whitespace-nowrap">{part}</span>{:else}{part}{/if}{/each}
        </span>
        {#if action !== null}
          <button
            type="button"
            class="pointer-events-auto relative inline-flex shrink-0 touch-manipulation items-center justify-center rounded-full text-callout font-semibold whitespace-nowrap after:absolute after:-inset-[6px] after:content-[''] disabled:opacity-40 {action ===
            'approve'
              ? 'bg-primary-fill text-on-primary'
              : 'bg-sel text-ink'} {largeType.value ? 'h-11 w-full' : 'h-8 px-[14px]'}"
            {disabled}
            aria-busy={busy}
            onclick={(e: MouseEvent) => {
              e.stopPropagation();
              void run();
            }}
          >
            {requested ? copy.inbox.actions.requested : action === "answer" ? answerLabel : LABEL[action]}
          </button>
        {/if}
      </div>
    </div>
  </div>
</div>
