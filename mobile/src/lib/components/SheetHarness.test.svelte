<script lang="ts">
  import Sheet from "./Sheet.svelte";
  import type { Detent } from "@mobile/lib/mobiletokens";

  let {
    onclose,
    detent,
    label = "Test sheet",
    title,
    done,
    action,
    cancel,
    dirty,
    withFooter = false,
  }: {
    onclose: () => void;
    detent?: Detent;
    label?: string | undefined;
    title?: string;
    done?: string | false;
    action?: { label: string; onclick: () => void; disabled?: boolean };
    cancel?: string;
    dirty?: boolean;
    withFooter?: boolean;
  } = $props();
</script>

{#snippet foot()}<button type="button">Pick</button>{/snippet}

<Sheet {label} {detent} {title} {done} {action} {cancel} {dirty} footer={withFooter ? foot : undefined} {onclose}>
  <p>Body</p>
</Sheet>
