<script lang="ts">
  import { untrack } from "svelte";
  import Sheet from "./Sheet.svelte";
  import ListGroup from "./ListGroup.svelte";
  import ListRow from "./ListRow.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import { copy } from "@mobile/lib/copy";
  import { PANE_LABEL_MAX, normalizePaneLabel } from "@mobile/lib/prefs";

  // One tab's menu: rename it on this phone, or close it on the Mac.
  //
  // WHY A SHEET AND NOT A POPOVER ANCHORED TO THE TAB. Sheet.svelte's opening
  // paragraph is the reason -- a phone modal rises from the bottom edge because
  // that is the half a thumb reaches -- and the tab strip adds one of its own: it
  // scrolls sideways, so a menu pinned to a tab moves out from under the finger
  // that opened it the moment anything nudges the strip. It is also the app's
  // fourth Sheet rather than its first bespoke menu, which is what keeps Escape,
  // the dismissible backdrop and the Dynamic Type height cap in one file.
  //
  // THE TWO ACTIONS ARE NOT THE SAME KIND OF THING and the sheet says so. A
  // rename is a nickname stored on this device (see prefs.ts): the tmux name is
  // the pane's identity, the daemon anchors on it, and nothing typed here ever
  // reaches a wire field. A close ends a real process tree on somebody's Mac.
  // So the rename section leads, the close sits under a rule at the bottom in
  // the danger variant, and the sheet states which machine each one touches --
  // the whole confusion this app can cause is a control that looks local and is
  // not.
  //
  // CLOSE IS ONE STEP, NOT TWO. The sheet already IS the deliberate second
  // gesture: it took a half-second hold to get here, and nothing else in it
  // fires by accident. A nested confirm over a control the daemon can refuse
  // anyway would be the third dialog in a gesture that started as a tap.
  //
  // AND CLOSE IS ABSENT, NEVER DISABLED, FOR THE AGENT PANE. `handlePaneClose`
  // refuses it outright because that pane is the session, so a button there
  // could only ever produce a refusal -- see wire/panes.ts. Its place is taken
  // by the sentence naming the command that DOES end a session, so the answer to
  // "then how do I stop this" is on the screen that raised the question.

  let {
    /** The tmux name: this pane's identity, and what the wire calls it. */
    name,
    /**
     * The daemon's own label for it -- "shell 2", "dev 1", "review".
     *
     * NOT NAMED `derived`, which is what it was called first. A local binding of
     * that name shadows the `$derived` rune: every `$derived(...)` below
     * compiled to a store subscription on the prop instead, so `shown`, `clean`
     * and `unchanged` were all quietly dead and the Save button never enabled.
     * The compiler does say so (store_rune_conflict), in a warning that is easy
     * to read past.
     */
    defaultName,
    /** The nickname stored on this device, or "" when there is none. */
    label,
    /**
     * Whether a close may be offered. FALSE for the agent pane, where the
     * control is omitted rather than disabled.
     */
    canClose,
    /** A close is in flight. */
    closing = false,
    /** Store a nickname. "" forgets it. */
    onrename,
    /** Close this pane on the Mac. */
    onclosepane,
    /** Dismiss without doing anything. */
    ondismiss,
  }: {
    name: string;
    defaultName: string;
    label: string;
    canClose: boolean;
    closing?: boolean;
    onrename: (label: string) => void;
    onclosepane: () => void;
    ondismiss: () => void;
  } = $props();

  // Seeded ONCE, at mount. The sheet is mounted only while it is open and for
  // one pane at a time, so there is no case where this has to re-seed -- and a
  // `$derived` here would throw away half a typed word the moment a reload of
  // the inventory reassigned the labels map.
  // `untrack` because `label` is a prop and the compiler is right to ask: a bare
  // read here captures only the initial value, which is exactly what is wanted,
  // and saying so explicitly is the difference between a decision and a bug.
  let draft = $state(untrack(() => label));

  /** The name the tab is showing right now. */
  const shown = $derived(label || defaultName || name);

  /** What this rename would actually store, after trimming and clipping. */
  const clean = $derived(normalizePaneLabel(draft));

  /** Nothing to save when the field already says what is stored. */
  const unchanged = $derived(clean === label);

  /**
   * The id tying the field to its label.
   *
   * A constant, for the same reason the terminal screen's panel id is one: only
   * one sheet is open at a time, so a generated id buys nothing and a stable one
   * is readable in a snapshot.
   */
  const NAME_FIELD = "pane-menu-name";

  function save(): void {
    if (unchanged) return;
    onrename(clean);
  }

  function reset(): void {
    draft = "";
    onrename("");
  }
</script>

<Sheet
  label="Options for {shown}"
  title="Rename"
  dismissLabel="Close the pane menu"
  action={{ label: copy.rename.save, onclick: save, disabled: unchanged }}
  onclose={ondismiss}
>
  <!-- One inset group: a borderless 44pt field with its own clear button. The
       tmux name is the group's footnote -- the identity every command sends, so a
       renamed tab can still be told apart. -->
  <div class="pt-1">
  <ListGroup>
    <div class="flex min-h-11 items-center pl-4" data-list-row>
      <label class="sr-only" for={NAME_FIELD}>Name for this pane on this phone</label>
      <input
        id={NAME_FIELD}
        class="min-h-11 min-w-0 flex-1 bg-transparent text-body text-ink outline-none placeholder:text-faint"
        type="text"
        autocapitalize="none"
        autocorrect="off"
        spellcheck="false"
        maxlength={PANE_LABEL_MAX}
        placeholder={defaultName || name}
        bind:value={draft}
        onkeydown={(e: KeyboardEvent) => {
          // Enter saves, which is what a one-field form owes a hardware keyboard.
          // Escape is Sheet's and is deliberately not touched here.
          if (e.key !== "Enter") return;
          e.preventDefault();
          save();
        }}
      />
      {#if draft !== ""}
        <button
          type="button"
          class="flex size-11 shrink-0 touch-manipulation items-center justify-center text-faint active:opacity-60"
          aria-label="Clear name"
          onclick={() => (draft = "")}
        >
          <Icon name="x" size={16} />
        </button>
      {/if}
    </div>
  </ListGroup>
  </div>

  {#if label !== ""}
    <div class="pt-4">
      <ListGroup>
        <ListRow label="Use the default name" onclick={reset} />
      </ListGroup>
    </div>
  {/if}

  {#if canClose}
    <!-- Close is one step: holding a tab to get here was the deliberate gesture. -->
    <div class="pt-4">
      <ListGroup>
        <ListRow icon="trash-2" tone="danger" label="Close this pane" disabled={closing} onclick={onclosepane} />
      </ListGroup>
    </div>
  {/if}
</Sheet>
