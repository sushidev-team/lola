<script lang="ts" module>
  /**
   * Where the flavor is remembered on this device.
   *
   * IT MIRRORS A PRIVATE CONSTANT — `THEME_CACHE_KEY` in
   * desktop/frontend/src/lib/theme-runtime.svelte.ts — because that module does
   * not export it and this project does not edit desktop/. The mirror is
   * deliberate and its failure mode is stated here so nobody has to work it out
   * from a symptom: if the desktop ever renames that key, this app forgets the
   * chosen flavor on the next launch and comes up on the compiled default.
   * Nothing breaks, no data is lost, and the picker still works — which is why
   * a mirror is an acceptable price for not forking the runtime.
   *
   * WHY WRITE IT AT ALL, rather than calling `appearance.commit()`:
   *
   *   * `commit` writes `[ui].theme` in config.toml ON THE MAC. This app is
   *     read-only about the daemon — PLAN.md's rule — and repainting the
   *     desktop app and the TUI from a phone is exactly the kind of remote
   *     write that rule exists to prevent.
   *   * It could not work anyway: the shim leaves `ConfigService.SetTheme`
   *     absent ON PURPOSE ("the phone's theme is the phone's"), so `commit`
   *     throws before it ever reaches the cache.
   *
   * `appearance.init()` reads this same key synchronously on boot, which is what
   * makes the choice survive a relaunch with no flash of the previous flavor.
   */
  const THEME_CACHE_KEY = "lola.theme";
</script>

<script lang="ts">
  import { appearance, FLAVORS, THEME_IDS, type ThemeId } from "$lib/theme-runtime.svelte";
  import SectionHeader from "@mobile/lib/components/SectionHeader.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { connection } from "@mobile/lib/connection.svelte";
  import { NAME_MAX } from "@mobile/lib/daemonname";
  import { nav } from "@mobile/lib/nav.svelte";

  // The two things a phone actually settles: which Mac it is attached to, and
  // what colour it is.
  //
  // EVERYTHING ELSE IN lola's SETTINGS IS ABSENT ON PURPOSE. The desktop's
  // settings form edits config.toml — polls, labels, review providers, the
  // Linear key — and none of that is reachable from here: the shim answers
  // `unsupported` for every config write, and the forms depend on a native
  // folder picker. That is a platform fact, not a gap to be filled in later
  // with a phone-shaped config editor.
  //
  // THIS IS NOW THE ONLY PLACE A MAC IS MANAGED, and the duplication that used
  // to be flagged here is gone rather than resolved by a shared component. The
  // sessions header carried a second door onto the same four things — the
  // connected-to line, disconnect, forget and the nickname — in a sheet that had
  // to be kept in step with this screen by hand. The button and the sheet were
  // both removed and the nickname moved here, which is the shape the tab bar
  // made available: a Settings TAB is a place, and a place does not need a
  // shortcut from another screen's header.

  /**
   * The nickname, drafted locally and committed on submit.
   *
   * Seeded from the stored override and NOT from `connection.label`, which falls
   * back to the daemon's own name: seeding from the label would put the
   * hostname in the field as if a person had typed it, and the next Return would
   * store it as an override that can no longer follow a rename on the Mac.
   */
  let nameDraft = $state(connection.renamed ? connection.label : "");

  function commitName(e: SubmitEvent) {
    e.preventDefault();
    connection.rename(nameDraft);
    nameDraft = connection.renamed ? connection.label : "";
  }

  /**
   * Leave the Mac.
   *
   * DISCONNECTING AND FORGETTING ARE DIFFERENT. `disconnect()` sets a flag that
   * lives as long as the process; the stored key does not, because it is in the
   * Keychain and iOS reclaims a backgrounded app freely — so an explicit
   * disconnect followed by a relaunch used to go straight back to an
   * authenticated session list. Forgetting is what makes leaving durable.
   */
  async function leave(forget: boolean): Promise<void> {
    if (forget) await connection.forget();
    await connection.disconnect();
    nav.toConnect();
  }

  /**
   * Apply a flavor: paint it now, remember it for the next launch.
   *
   * Driving `appearance.id` + `paint()` is the same pair `appearance.init()`
   * uses, and it is the whole update — Tailwind v4 compiles every utility to a
   * `var()` against a custom property on :root, so rewriting the properties
   * re-resolves the entire sheet with no component involved. App.svelte's
   * effect on `appearance.flavor` repaints the seven phone-only tokens (the tab
   * bar's ground among them) off the same object, so the chrome moves with the
   * screens rather than a frame behind them.
   */
  function pick(id: ThemeId): void {
    appearance.id = id;
    appearance.paint();
    try {
      globalThis.localStorage?.setItem(THEME_CACHE_KEY, id);
    } catch {
      /* storage disabled or partitioned — a theme is not worth failing over,
         and the flavor still applies for the life of this launch */
    }
  }
</script>

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <!-- Same header shape as every other screen in the redesign; see
       Activity.svelte for why the top inset is spelled out rather than taken
       from `pt-safe-t`. -->
  <header
    class="flex shrink-0 flex-col gap-0.5 px-5 pb-3"
    style="padding-top: calc(var(--lola-top-inset, env(safe-area-inset-top, 0px)) + 6px)"
  >
    <h1 class="flex h-11 items-center text-2xl text-ink">Settings</h1>
    <!-- `text-body`, not the `text-base` the Sessions header uses. That one is a
         facts line — three counts and a host name — and reads as data at the row
         size. This is a HINT, which the brief's scale puts one step down. -->
    <span class="truncate text-body text-subtext">This phone only — nothing here changes the Mac</span>
  </header>

  <!-- No bottom safe-area spacer: the tab bar pays that inset itself. -->
  <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain pb-4">
    <!-- The list's own heading, with the count omitted. It used to be two
         inline copies of this geometry, on the argument that a count is
         meaningless for a settings group — true, and the fix is an optional
         count rather than a second `px-5 pt-3.5 pb-[5px]` that can drift from
         the first. -->
    <SectionHeader title="Connection" />

    <div class="flex flex-col gap-1 px-5 py-2">
      <span class="text-base text-ink">Connected to {connection.label}</span>
      <!-- The ADDRESS as well as the name, because they answer different
           questions: the name says which machine, the address says over which
           network — and discovery means the same Mac answers on a different one
           at home and at the office. `num` is tabular figures so the row does
           not reflow when a re-dial lands on a neighbouring address. -->
      <span class="num text-sm text-faint">{connection.host}:{connection.port}</span>
      <span class="copy text-sm text-faint">
        {#if connection.hasStoredKey}
          This Mac stays remembered and the app reconnects on its own next time. Forgetting it
          removes the access key from this phone's Keychain as well, and pairing has to be done
          again.
        {:else}
          Nothing is stored for this Mac, so the next launch starts at the pairing screen.
        {/if}
      </span>
    </div>

    <!-- THE NICKNAME, and this screen is the only thing that writes one. A
         hostname is frequently neither chosen nor readable — "Martins-MacBook-
         Pro" is a fine machine name and a poor label — so the daemon's own name
         is the default and this overrides it.

         A FORM, so the keyboard's Return commits it: a bare input is one a phone
         can only leave by dismissing the keyboard, and a rename needing a
         separate Save button read as two decisions rather than one.

         EMPTYING IT IS THE UNDO, not a way to have no name — the field falls
         back to what the daemon reports, which is why the placeholder shows that
         name rather than a hint. -->
    <form class="flex flex-col gap-1 px-5 pb-2" onsubmit={commitName}>
      <label class="flex flex-col gap-1">
        <span class="text-sm text-faint">Name for this Mac</span>
        <input
          class="w-full rounded-md border border-edge bg-panel px-3 py-2 text-ink"
          type="text"
          autocapitalize="words"
          autocorrect="off"
          spellcheck="false"
          maxlength={NAME_MAX}
          placeholder={connection.label}
          bind:value={nameDraft}
        />
      </label>
    </form>

    <div class="flex flex-col gap-2 px-5 pt-1 pb-3">
      <!-- THE BUTTON NAMES WHAT IT LEAVES. "Disconnect" alone was the original
           bug in the sessions header: a control whose subject the user has to
           infer is ambiguous wherever it is drawn. -->
      <TouchButton wide variant="secondary" onclick={() => void leave(false)}>
        Disconnect from {connection.label}
      </TouchButton>

      {#if connection.hasStoredKey}
        <!-- `text-bad!` is not decoration and the `!` is not optional. The
             shared Button's `danger` variant is `text-faint` at REST and only
             turns red on hover — which on a phone never happens, so the
             destructive choice would render in exactly the same ink as
             "Disconnect" above it. A plain `text-bad` has the same specificity
             as the variant's own and the winner would be decided by Tailwind's
             order in the compiled sheet. -->
        <TouchButton wide variant="danger" class="text-bad!" onclick={() => void leave(true)}>
          Forget this Mac
        </TouchButton>
      {/if}
    </div>

    <SectionHeader title="Appearance" />

    <!-- THE LIST IS THE Go LIST. `THEME_IDS` matches internal/config's UIThemes
         exactly — same order, same spelling — so a flavor added there appears
         here with no edit, and one this app invented would be rejected by
         `Validate` on the Mac. Nothing is enumerated locally. -->
    {#each THEME_IDS as id (id)}
      {@const flavor = FLAVORS[id]}
      {@const active = appearance.id === id}
      <button
        type="button"
        class="tap-row flex w-full touch-manipulation items-center gap-3 border-b border-edge-soft px-5 py-[11px] text-left active:bg-sel"
        aria-pressed={active}
        onclick={() => pick(id)}
      >
        <!-- THE SWATCH IS DRAWN IN ITS OWN FLAVOR'S COLOURS, which is the one
             place in this app an inline style carries a raw hex. It has to: the
             whole point is showing a flavor that is NOT the one the token set
             is currently painted with, and every `bg-*` utility resolves to the
             live one. The values come from the flavor data, never from user
             input or daemon text, so there is nothing here to escape. It mirrors
             the desktop's own hand-rolled swatches, which CLAUDE.md lists as a
             deliberate exception to the Button ladder for exactly this reason.

             `sky` is what the token mapping resolves --color-accent from; base
             is the canvas and surface1 the edge, so the chip is a miniature of
             the screen it will produce. -->
        <span
          class="size-5 shrink-0 rounded-md border"
          style="background:{flavor.base};border-color:{flavor.surface1}"
          aria-hidden="true"
        >
          <span
            class="m-[5px] block size-2.5 rounded-full"
            style="background:{flavor.sky}"
          ></span>
        </span>
        <span class="min-w-0 flex-1 truncate text-base font-medium text-ink">{flavor.label}</span>
        <!-- A tick rather than a dot, and only on the chosen row: `aria-pressed`
             already carries the state for a screen reader, so this is the
             sighted half of the same fact and is hidden from the tree. -->
        {#if active}
          <svg
            viewBox="0 0 24 24"
            class="size-5 shrink-0 text-accent"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M4 12.5 9.5 18 20 7" />
          </svg>
        {/if}
      </button>
    {/each}

    <p class="copy px-5 pt-3 text-sm text-faint">
      The flavor is stored on this phone. The Mac keeps its own in config.toml, and this app never
      writes it.
    </p>
  </div>
</div>
