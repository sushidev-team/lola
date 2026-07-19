<script lang="ts">
  import { pillClasses, statusLabel, pillKind } from "$lib/theme";
  let { status, dim = false }: { status: string; dim?: boolean } = $props();
  const solid = $derived(pillKind(status) === "urgent" || pillKind(status) === "broken");
</script>

<span
  class="inline-flex items-center whitespace-nowrap rounded px-1.5 py-[1px] text-[11px] leading-tight {pillClasses(
    status,
  )}"
  class:opacity-70={dim && !solid}
>
  {statusLabel(status)}
</span>
