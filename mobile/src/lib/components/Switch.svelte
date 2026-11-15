<script lang="ts">
  // The iOS switch: a 51 x 31 track (accent when on; edge with a 1px inset edge ring when off, so
  // it reads on a grouped surface) and a 27px white thumb in both states.
  // The look is ViewSettings' switch, at the platform size; it is one control, so it carries the
  // name (`label`) and the state (`aria-checked`) and nothing inside it is announced again.
  let {
    checked,
    onchange,
    label,
    disabled = false,
  }: {
    checked: boolean;
    onchange: (v: boolean) => void;
    label: string;
    disabled?: boolean;
  } = $props();
</script>

<button
  type="button"
  role="switch"
  aria-checked={checked}
  aria-label={label}
  {disabled}
  class="switch relative block h-[31px] w-[51px] shrink-0 touch-manipulation rounded-full transition-colors disabled:opacity-40 {checked
    ? 'bg-accent'
    : 'bg-edge'}"
  style={checked ? undefined : "box-shadow: inset 0 0 0 1px var(--color-edge)"}
  onclick={() => onchange(!checked)}
>
  <span
    class="thumb absolute top-0.5 left-0.5 block size-[27px] rounded-full bg-white shadow-sm transition-transform {checked
      ? 'translate-x-5'
      : 'translate-x-0'}"
    aria-hidden="true"
  ></span>
</button>

<style>
  /* A 44pt hit area around the 31pt track, without changing its size. */
  .switch::after {
    content: "";
    position: absolute;
    inset: -7px -4px;
  }
  @media (prefers-reduced-motion: reduce) {
    .switch,
    .thumb {
      transition: none;
    }
  }
</style>
