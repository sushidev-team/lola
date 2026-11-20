<script lang="ts">
  import { store } from "$lib/store.svelte";
  import type { SessionInfo } from "@bindings/internal/protocol";
  import Button from "./Button.svelte";

  // The plan-approval gate ([[project]].require_plan). The agent may not edit
  // files until a human approves its plan, so a SUBMITTED plan is the one thing
  // holding this session up — it gets a banner above the terminal, in the app's
  // "you are needed" orange, with the plan itself and both answers.
  //
  // The plan is the agent's own text: rendered as preformatted text, never as
  // markup. The decision goes to the daemon (cmd=planDecide), which types the
  // verdict into the agent at its next resting prompt and echoes it into the
  // Linear agent session when there is one — the same decision Linear's
  // Approve / Request changes reply makes.
  let { session }: { session: SessionInfo } = $props();

  const plan = $derived(session.plan);
  let comment = $state("");
  let busy = $state(false);
  let open = $state(true);

  async function decide(approve: boolean) {
    if (busy) return;
    busy = true;
    try {
      if (await store.decidePlan(session.id, approve, comment.trim())) comment = "";
    } finally {
      busy = false;
    }
  }
</script>

{#if plan && plan.gate === "submitted"}
  <div
    class="flex max-h-[45%] shrink-0 flex-col gap-2 border-b border-orange/40 bg-orange/10 px-3 py-2.5 text-ink"
    role="region"
    aria-label="Plan awaiting approval"
  >
    <div class="flex items-center gap-2">
      <span class="font-medium text-orange">Plan awaiting approval</span>
      {#if (plan.round ?? 0) > 1}<span class="num text-sm text-faint">round {plan.round}</span>{/if}
      <Button size="xs" class="ml-auto" onclick={() => (open = !open)}>{open ? "Hide plan" : "Show plan"}</Button>
    </div>
    {#if open}
      <pre
        class="selectable min-h-0 overflow-y-auto rounded border border-edge/60 bg-canvas px-2.5 py-2 font-mono text-sm whitespace-pre-wrap">{plan.text}</pre>
    {/if}
    <div class="flex items-end gap-2">
      <textarea
        class="min-w-0 flex-1 resize-y rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none placeholder:text-placeholder focus:border-accent"
        rows="1"
        aria-label="Feedback for the agent"
        placeholder="What should change? (required to request changes)"
        bind:value={comment}
      ></textarea>
      <Button variant="secondary" size="sm" loading={busy} disabled={!comment.trim()} onclick={() => decide(false)}>
        Request changes
      </Button>
      <Button variant="primary" size="sm" loading={busy} onclick={() => decide(true)}>Approve plan</Button>
    </div>
  </div>
{:else if plan && plan.gate === "planning"}
  <div class="flex shrink-0 items-center gap-3 border-b border-edge/60 px-3 py-2 text-sm text-faint" role="status">
    <span class="min-w-0 flex-1">
      Planning — file edits stay blocked until a plan is approved.
      {#if plan.feedback}<span class="selectable block truncate" title={plan.feedback}>Last feedback: {plan.feedback}</span>{/if}
    </span>
    <Button
      size="xs"
      loading={busy}
      title="unlock coding without waiting for a plan"
      onclick={() => decide(true)}>Skip plan</Button
    >
  </div>
{/if}
