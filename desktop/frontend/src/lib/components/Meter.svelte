<script lang="ts">
  // A thin proportional bar, like the TUI triage meters: "NN label ━━━━".
  let {
    label,
    value,
    total,
    color = "var(--color-info)",
    strong = false,
  }: { label: string; value: number; total: number; color?: string; strong?: boolean } = $props();
  const frac = $derived(total > 0 ? Math.min(1, value / total) : 0);
</script>

<div class="flex items-center gap-2 text-xs">
  <span class="w-4 tabular-nums" style="color:{color}">{value}</span>
  <!-- `strong` (used for "need you") tints the LABEL in the bar's colour and
       bumps its weight; ordinary meters keep a faint, uniform label. -->
  <span class="w-16 shrink-0 whitespace-nowrap {strong ? 'font-medium' : 'text-faint'}" style={strong ? `color:${color}` : ""}
    >{label}</span
  >
  <div class="relative h-[6px] flex-1 overflow-hidden rounded-full bg-edge/50">
    <div class="h-full rounded-full transition-[width] duration-300" style="width:{frac * 100}%;background:{color}"></div>
  </div>
</div>
