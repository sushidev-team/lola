<script lang="ts">
  import type { Snippet } from "svelte";
  import Icon from "$lib/components/Icon.svelte";
  import type { IconName } from "$lib/icons";

  // One row of a ListGroup. A <button> when it does something (`onclick`), else a
  // <div>. Line 1 is the label, line 2 an optional detail; on the right a value, or
  // a check (which replaces the value), a chevron, or a `trailing` snippet (a Switch).
  //
  // Invariants:
  //  - 44pt minimum, 52pt with a leading icon; the separator inset follows the icon
  //    column (`--row-inset`), and app.css draws the hairline between rows.
  //  - A `checked` row draws its label in ink and its check in accent. `current`
  //    is the checked row that cannot be chosen: full colour (never dimmed),
  //    aria-current, not a button. `disabled` alone still dims.
  //  - `align="center"` is the action sheet's Cancel: centred label, semibold, 56pt.
  //  - Trailing accessories (value, check, `trailing`, chevron) sit 16px from the
  //    right edge; value and chevron share one trailing column, 12px apart.
  //  - Every class is a literal: Tailwind reads source text.
  let {
    label,
    detail,
    detailMono = false,
    align = "start",
    current = false,
    value,
    icon,
    chevron = false,
    checked = false,
    tone = "default",
    disabled = false,
    onclick: onclickProp,
    role,
    trailing,
    "aria-label": ariaLabel,
  }: {
    label: string;
    detail?: string;
    /** Draw the detail as code: mono, footnote, faint. */
    detailMono?: boolean;
    align?: "start" | "center";
    /** The chosen row that cannot be chosen again: full colour, not tappable. */
    current?: boolean;
    value?: string;
    icon?: IconName;
    chevron?: boolean;
    checked?: boolean;
    tone?: "default" | "accent" | "danger";
    disabled?: boolean;
    onclick?: () => void;
    role?: string;
    trailing?: Snippet;
    "aria-label"?: string;
  } = $props();

  const TONE = { default: "text-ink", accent: "text-accent", danger: "text-bad" } as const;
  const onclick = $derived(current ? undefined : onclickProp);
  const centered = $derived(align === "center");
  const isChecked = $derived(checked || current);
  const dim = $derived(disabled && !current);
  const base = $derived(
    `flex w-full items-center gap-3 px-4 ${centered ? "min-h-14 justify-center text-center" : `text-left ${icon ? "min-h-[52px]" : "min-h-11"}`} ${dim ? "opacity-40" : ""}`,
  );
  const labelClass = $derived(
    `${centered ? "text-body font-semibold" : "text-body"} ${isChecked && tone === "default" ? "text-ink" : TONE[tone]}`,
  );
</script>

{#snippet inner()}
  {#if icon}
    <span class="flex size-7 shrink-0 items-center justify-center {TONE[tone]}" aria-hidden="true">
      <Icon name={icon} size={22} />
    </span>
  {/if}
  <span class="flex min-w-0 {centered ? '' : 'flex-1'} flex-col py-2">
    <span class={labelClass}>{label}</span>
    {#if detail}
      <span class="text-footnote text-faint {detailMono ? 'font-mono' : ''}">{detail}</span>
    {/if}
  </span>
  {#if isChecked || value || trailing || chevron}
    <span class="flex shrink-0 items-center gap-3">
      {#if isChecked}
        <span class="shrink-0 text-accent" data-row-check aria-hidden="true"><Icon name="check" size={18} /></span>
      {:else if value}
        <span class="max-w-[50vw] shrink-0 truncate text-callout text-subtext tabular-nums">{value}</span>
      {/if}
      {#if trailing}
        {@render trailing()}
      {/if}
      {#if chevron}
        <span class="shrink-0 text-faint" data-row-chevron aria-hidden="true"><Icon name="chevron-right" size={14} /></span>
      {/if}
    </span>
  {/if}
{/snippet}

{#if onclick}
  <button
    type="button"
    class="{base} touch-manipulation enabled:active:bg-sel"
    style={icon ? "--row-inset: 52px" : undefined}
    data-list-row
    {role}
    aria-label={ariaLabel}
    aria-checked={role === "menuitemradio" || role === "radio" || role === "switch" ? checked : undefined}
    {disabled}
    {onclick}
  >
    {@render inner()}
  </button>
{:else}
  <div
    class={base}
    style={icon ? "--row-inset: 52px" : undefined}
    data-list-row
    {role}
    aria-label={ariaLabel}
    aria-disabled={disabled || undefined}
    aria-current={current ? "true" : undefined}
  >
    {@render inner()}
  </div>
{/if}
