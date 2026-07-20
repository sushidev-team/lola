<script lang="ts">
  import { pillClasses, statusLabel } from "$lib/theme";
  let { status }: { status: string } = $props();
  // No `dim` opacity. Group opacity composites the pill — fill AND label as one
  // layer — over whatever is behind it, which pulls the two toward a common
  // colour and quietly undoes the AA the pill's measured tokens guarantee. The
  // grid's de-emphasis already comes from the muted tints the non-attention
  // statuses carry (urgent/broken stay solid and loud); fading the whole pill
  // only cost legibility on top of that.
</script>

<span
  class="inline-flex items-center whitespace-nowrap rounded px-1.5 py-[1px] text-[11px] leading-tight {pillClasses(
    status,
  )}"
>
  {statusLabel(status)}
</span>
