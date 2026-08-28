<script lang="ts">
  import {
    BAR_ROW_PRIMARY,
    BAR_ROW_SECONDARY,
    DEFAULT_MODES,
    barKeyBytes,
    type BarKey,
    type KeyModifiers,
    type TerminalModes,
  } from "@mobile/lib/keybytes";
  import AccessoryKey from "./AccessoryKey.svelte";

  // The keyboard accessory bar: the feature that decides whether this app is
  // usable at all.
  //
  // An iOS soft keyboard has no Escape, no Tab, no Ctrl, no arrows and no way to
  // express Shift+Enter. Every one of those is on the critical path for
  // answering a parked agent — Escape dismisses the modal the daemon's pane
  // classifier reports as ActivityBlocked, Shift-Tab cycles Claude Code's
  // permission modes, the arrows drive the AskUserQuestion picker, and Ctrl-C is
  // the reason a person reaches for the phone in the first place. Without this
  // bar the terminal screen is a viewer.
  //
  // The layout is Termux's, which is what every serious mobile terminal
  // converges on and is the one whose mechanics are readable in source. Row one
  // is always up; row two collapses, because two permanent rows over a soft
  // keyboard leave almost no pane visible on a 390-point screen.
  //
  // MODIFIERS LATCH. Tap ctrl, then c: the next ordinary keypress consumes the
  // modifier and clears it. A modifier you must hold is unusable one-handed on
  // glass, and holding two is impossible. The latch is consumed by ANY key that
  // produces bytes, including one from the soft keyboard — which is why the
  // parent hands its own typing through `consumeLatch`.
  //
  // The bar never encodes anything itself: every byte comes from
  // keybytes.barKeyBytes, which is the one table, and this component's only job
  // is deciding which key was pressed and what the latch was at that moment.

  let {
    /** The terminal's live modes. The arrows' encoding depends on DECCKM. */
    modes = DEFAULT_MODES,
    /** Send bytes to the pane. */
    onsend,
    /**
     * Non-empty while the app wants a deliberate confirmation before the first
     * keystroke of a burst — the mid-turn friction described in PLAN.md. This
     * component only asks; the parent decides.
     */
    needsConfirm = false,
    onconfirm,
  }: {
    modes?: TerminalModes;
    onsend: (bytes: string) => void;
    needsConfirm?: boolean;
    onconfirm?: () => void;
  } = $props();

  let expanded = $state(false);
  let ctrl = $state(false);
  let alt = $state(false);

  const mods = $derived<KeyModifiers>({ ctrl, alt });

  function clearLatch() {
    ctrl = false;
    alt = false;
  }

  /**
   * The latch as it stands, consumed. Exported so the parent can apply it to
   * text the SOFT keyboard produced: a latch that only worked for bar keys would
   * make "ctrl then a letter" — the single most common chord — impossible, since
   * the letters are on the system keyboard.
   */
  export function consumeLatch(): KeyModifiers {
    const m = { ctrl, alt };
    clearLatch();
    return m;
  }

  /** Whether a modifier is currently latched, for the parent's own encoding. */
  export function latched(): KeyModifiers {
    return { ctrl, alt };
  }

  /**
   * Fire one key. Returns false when the press was REFUSED, which is what stops
   * AccessoryKey arming an auto-repeat: holding the down arrow through the
   * mid-turn banner would otherwise re-ask for confirmation every 80ms.
   */
  function press(key: BarKey): boolean {
    if (key.kind === "latch") {
      // A latch toggles and sends nothing. Tapping it twice cancels, which is
      // the only way to back out of a mis-tap without sending a key.
      if (key.value === "ctrl") ctrl = !ctrl;
      else alt = !alt;
      return true;
    }

    // The mid-turn guard, and it is FRICTION rather than a gate — see the
    // terminal screen for the full statement. The interrupts are exempt because
    // interrupting is the legitimate mid-turn action, and putting a
    // confirmation in front of Ctrl-C would recreate exactly the uselessness the
    // exemption exists to avoid.
    //
    // The latch is NOT consumed here: the press produced no bytes, so clearing
    // ctrl would make the user latch it again after answering the confirmation,
    // with nothing on screen saying why.
    if (needsConfirm && !key.interrupt) {
      onconfirm?.();
      return false;
    }

    const bytes = barKeyBytes(key, modes, mods);
    clearLatch();
    if (bytes !== "") onsend(bytes);
    return true;
  }
</script>

<!-- The bar sits above the soft keyboard and pays back the home-indicator inset
     itself. env() inline rather than a Tailwind token: this component must not
     depend on the shell's CSS defining a spacing scale it happens not to. -->
<div
  class="shrink-0 border-t border-edge bg-panel"
  style="padding-bottom: env(safe-area-inset-bottom, 0px)"
>
  {#if expanded}
    <!-- Row two scrolls horizontally: thirteen keys do not fit at 390 points and
         shrinking them below a fingertip is worse than a scroll. -->
    <div class="flex gap-1 overflow-x-auto px-2 pt-2 pb-1 [scrollbar-width:none]">
      {#each BAR_ROW_SECONDARY as key (key.id ?? key.value)}
        <AccessoryKey
          label={key.label}
          aria={key.aria}
          repeats={key.repeats}
          wide={key.label.length > 2}
          latched={key.kind === "latch" && (key.value === "ctrl" ? ctrl : alt)}
          onfire={() => press(key)}
        />
      {/each}
    </div>
  {/if}

  <div class="flex items-center gap-1 px-2 pt-1 pb-2">
    {#each BAR_ROW_PRIMARY as key (key.id)}
      <AccessoryKey
        label={key.label}
        aria={key.aria}
        repeats={key.repeats}
        wide={key.label.length > 2}
        onfire={() => press(key)}
      />
    {/each}
    <!-- The disclosure is last so the keys start at the screen edge, where a
         thumb reaches them. It is not an AccessoryKey: it sends no bytes. -->
    <button
      type="button"
      aria-label={expanded ? "Hide the second key row" : "Show the second key row"}
      aria-expanded={expanded}
      class="ml-auto flex h-10 min-w-10 shrink-0 touch-manipulation items-center justify-center
             rounded-md border border-edge/60 bg-panel text-base text-faint select-none"
      onpointerdown={(e) => {
        e.preventDefault();
        expanded = !expanded;
      }}
    >
      {expanded ? "▾" : "▴"}
    </button>
  </div>
</div>
