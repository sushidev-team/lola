<script lang="ts">
  import { onMount } from "svelte";
  import { nav as deskNav } from "$lib/nav.svelte";
  import { reviewWorkspace } from "$lib/reviewworkspace.svelte";
  import SegmentBar from "@mobile/lib/components/SegmentBar.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { copy } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";

  // The review package. The workspace is the shared desktop component, mounted
  // EMBEDDED: no modal of its own, its tabs and its "Mark reviewed" handed up
  // through `onstatus` so the sheet draws them in the phone's own vocabulary
  // (SegmentBar, Done trailing, Mark reviewed in the pinned footer). The desktop nav's
  // `pr-review` overlay and this sheet stay one state: the overlay closing
  // closes the sheet.

  type Status = {
    tabs: { id: string; label: string }[];
    canMark: boolean;
    reviewed: boolean;
    marking: boolean;
    mark: () => void;
  };

  const id = $derived(router.top.name === "session" ? router.top.id : "");
  let armed = false;
  let tab = $state("material");
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
  {#if status.canMark}
    <TouchButton variant="primary" prominent disabled={status.reviewed || status.marking} onclick={status.mark}>
      {status.reviewed ? "Reviewed" : copy.reviewPackage.markReviewed}
    </TouchButton>
  {/if}
{/snippet}

<Sheet
  label={copy.reviewPackage.title}
  title={copy.reviewPackage.title}
  footer={status.canMark ? footer : undefined}
  detent="large"
  onclose={() => router.close()}
>
  <div data-phone-desk class="min-w-0 px-4">
    {#if status.tabs.length > 1}
      <div class="pb-3">
        <SegmentBar segments={status.tabs} value={tab} onchange={(t) => (tab = t)} label="Review sections" />
      </div>
    {/if}
    {#await import("$lib/components/session/ReviewWorkspace.svelte") then m}
      {@const Workspace = m.default}
      <Workspace sessionId={reviewWorkspace.session || id} embedded labels={{ verified: copy.reviewPackage.verified, notVerified: copy.reviewPackage.notVerified }} bind:tab onstatus={(s: Status) => (status = s)} />
    {/await}
  </div>
</Sheet>
