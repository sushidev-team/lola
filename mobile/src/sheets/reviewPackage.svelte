<script lang="ts">
  import { listedFeedbackCount } from "@mobile/lib/feedbackcount";
  import { onMount } from "svelte";
  import { feedback } from "$lib/feedback.svelte";
  import { nav as deskNav } from "$lib/nav.svelte";
  import { reviewWorkspace } from "$lib/reviewworkspace.svelte";
  import SegmentBar from "@mobile/lib/components/SegmentBar.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { copy, fill } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";
  import { can } from "@mobile/lib/tiers.svelte";

  // The review package. The workspace is the shared desktop component, mounted
  // EMBEDDED: no modal of its own, its tabs and its "Mark reviewed" handed up
  // through `onstatus` so the sheet draws them in the phone's own vocabulary
  // (SegmentBar, Done trailing, Mark reviewed in the pinned footer). The desktop nav's
  // `pr-review` overlay and this sheet stay one state: the overlay closing
  // closes the sheet. The workspace gets the phone's `can`, so a control this phone
  // may not send is not drawn (Verify and Capture are never drawn on a phone). A
  // route may name the tab to open on (`&tab=visuals`). Queued comments are one tap
  // away: the footer's Feedback button opens the feedback sheet.

  type Status = {
    tabs: { id: string; label: string }[];
    canMark: boolean;
    reviewed: boolean;
    marking: boolean;
    mark: () => void;
  };

  const id = $derived(router.top.name === "session" ? router.top.id : "");
  let armed = false;
  // The route's tab. Not checked against the workspace's tabs: those arrive before the
  // material has loaded, and the workspace draws an empty state for a tab with nothing in it.
  let tab = $state(router.sheetTab || "material");
  const drafts = $derived(listedFeedbackCount(id));
  let status = $state<Status>({ tabs: [], canMark: false, reviewed: false, marking: false, mark: () => {} });

  onMount(() => {
    if (deskNav.overlay !== "pr-review") reviewWorkspace.open(id);
    armed = true;
  });
  $effect(() => {
    const open = deskNav.overlay === "pr-review";
    if (armed && !open) router.close();
  });

</script>

{#snippet footer()}
  <div class="flex items-center gap-2">
    {#if drafts > 0}
      <TouchButton variant="secondary" class="flex-1 justify-center!" onclick={() => router.open("feedback")}>
        {fill(copy.reviewPackage.feedback, { n: drafts })}
      </TouchButton>
    {/if}
    {#if status.canMark}
      <TouchButton variant="primary" class="flex-1 justify-center!" disabled={status.reviewed || status.marking} onclick={status.mark}>
        {status.reviewed ? copy.reviewPackage.reviewed : copy.reviewPackage.markReviewed}
      </TouchButton>
    {/if}
  </div>
{/snippet}

<Sheet
  label={copy.reviewPackage.title}
  title={copy.reviewPackage.title}
  footer={status.canMark || drafts > 0 ? footer : undefined}
  detent="large"
  onclose={() => router.close()}
>
  {#snippet navigation()}
    {#if status.tabs.length > 1}
      <div class="shrink-0 border-b border-edge-soft px-4 py-2">
        <SegmentBar segments={status.tabs} value={tab} onchange={(t) => (tab = t)} label="Review sections" />
      </div>
    {/if}
  {/snippet}
  <div data-phone-desk class="min-w-0 shrink-0 px-4 pb-4">
    {#await import("$lib/components/session/ReviewWorkspace.svelte") then m}
      {@const Workspace = m.default}
      <Workspace sessionId={reviewWorkspace.session || id} embedded {can} labels={{ verified: copy.reviewPackage.verified, notVerified: copy.reviewPackage.notVerified }} bind:tab onstatus={(s: Status) => (status = s)} />
    {/await}
  </div>
</Sheet>
