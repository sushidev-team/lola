<script lang="ts">
  import { store } from "$lib/store.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { copy, fill } from "@mobile/lib/copy";
  import { haptic } from "@mobile/lib/haptics";
  import { router } from "@mobile/lib/router.svelte";

  // The plan-approval gate from the phone (spec §5.3): the agent's submitted
  // plan, an approve and a request-changes. The plan is the agent's own text —
  // preformatted, never markup. The verdict is cmd=planDecide (control tier);
  // the daemon types it into the agent at its next resting prompt.

  const id = $derived(router.top.name === "session" ? router.top.id : "");
  const plan = $derived(store.sessionById(id)?.plan);
  const submitted = $derived(plan?.gate === "submitted");
  let comment = $state("");
  let busy = $state(false);

  async function decide(approve: boolean): Promise<void> {
    if (busy || !id || !submitted || (!approve && !comment.trim())) return;
    busy = true;
    const ok = await store.decidePlan(id, approve, comment.trim());
    busy = false;
    if (ok) {
      haptic("success");
      router.close();
    }
  }
</script>

{#snippet footer()}
  <div class="flex flex-col gap-2">
    <TouchButton variant="primary" prominent loading={busy} disabled={!submitted} onclick={() => decide(true)}>
      {copy.plan.approve}
    </TouchButton>
    <TouchButton variant="secondary" loading={busy} disabled={!submitted || !comment.trim()} onclick={() => decide(false)}>
      {copy.plan.requestChanges}
    </TouchButton>
  </div>
{/snippet}

<Sheet
  label={copy.plan.title}
  title={copy.plan.title}
  detent="large"
  cancel="Cancel"
  done={false}
  dirty={comment.trim() !== ""}
  {footer}
  onclose={() => router.close()}
>
  <div class="flex min-w-0 flex-col gap-3 px-4 pb-2">
    {#if submitted}
      {#if (plan?.round ?? 0) > 1}<p class="text-subhead text-faint">{fill(copy.plan.round, { n: plan?.round ?? 0 })}</p>{/if}
      <pre class="selectable max-h-[45vh] overflow-y-auto rounded border border-edge/60 px-3 py-2 font-mono text-subhead whitespace-pre-wrap">{plan?.text}</pre>
      <ListGroup>
        <textarea
          class="min-h-20 text-body text-ink outline-none placeholder:text-placeholder"
          aria-label={copy.plan.feedbackLabel}
          placeholder={copy.plan.placeholder}
          bind:value={comment}
          disabled={busy}
        ></textarea>
      </ListGroup>
    {:else}
      <p class="text-subhead text-faint" role="status">{copy.plan.none}</p>
    {/if}
  </div>
</Sheet>
