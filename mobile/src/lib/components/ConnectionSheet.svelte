<script lang="ts">
  import Sheet from "./Sheet.svelte";
  import TouchButton from "./TouchButton.svelte";
  import { connection } from "@mobile/lib/connection.svelte";
  import { NAME_MAX } from "@mobile/lib/daemonname";

  /**
   * The settings menu behind the header's gear.
   *
   * IT IS ABOUT THE CONNECTION, NOT THE DAEMON, and that distinction is the
   * whole reason this sheet exists. Two bare controls used to sit in the header
   * — Refresh and Disconnect — and neither named its subject, so both read as
   * daemon controls. They are not: this app can never stop the daemon (PLAN.md
   * — "a phone that stops the daemon severs the only link it has back"), and
   * Refresh only re-asked a list that already polls.
   *
   * DISCONNECTING AND FORGETTING ARE DIFFERENT, which is why both are here.
   * `disconnect()` sets a flag that lives as long as the process. The stored key
   * does not: it is in the Keychain, and iOS reclaims a backgrounded app freely
   * — so an explicit disconnect followed by a relaunch went straight back to an
   * authenticated session list. The user's decision to leave was the one thing
   * that was NOT durable, while the credential was.
   */

  let { onleave, onclose }: { onleave: (forget: boolean) => void; onclose: () => void } = $props();

  /**
   * The rename, drafted locally and committed on submit.
   *
   * NAMING THE MAC IS THE POINT OF THIS SHEET NOW. Discovery means the same
   * machine answers on a different address at home and at the office, so an
   * address names a network rather than the thing somebody left work running
   * on. The daemon reports its own hostname and that is the default; this row
   * exists because a hostname is frequently neither chosen nor readable —
   * "Martins-MacBook-Pro" is a fine machine name and a poor label, and somebody
   * with two Macs wants "work" and "home".
   *
   * EMPTYING IT IS THE UNDO, not a way to have no name: the field falls back to
   * the daemon's own name, which is why the placeholder shows that name rather
   * than being a hint about typing.
   */
  let draft = $state(connection.renamed ? connection.label : "");

  function commit(e: Event): void {
    e.preventDefault();
    connection.rename(draft);
    draft = connection.renamed ? connection.label : "";
  }
</script>

<!-- The backdrop keeps Sheet's default "Close" rather than being named "Cancel"
     too: two controls with the same accessible name inside one dialog is a real
     ambiguity for anyone navigating by name, and this sheet already has a Cancel
     button of its own. -->
<Sheet label="Connection settings" {onclose}>
  <div class="flex flex-col gap-1">
    <span class="text-ink">Connected to {connection.label}</span>
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

  <!-- A form, so the keyboard's Return commits it: a bare input in a sheet is
       one a phone can only leave by dismissing, and a rename that needs a
       separate Save button read as two decisions rather than one. -->
  <form class="flex flex-col gap-1" onsubmit={commit}>
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
        bind:value={draft} />
    </label>
    <span class="copy text-xs text-faint">
      {#if connection.renamed}
        Shown wherever this app names the Mac. Clear it to go back to the name the daemon reports.
      {:else}
        Shown wherever this app names the Mac. Left empty, it uses the name the daemon reports for
        itself.
      {/if}
    </span>
  </form>

  <!-- THE BUTTON NAMES WHAT IT LEAVES. "Disconnect" alone was the original bug
       in a header, and it is no better in a menu — a control whose subject the
       user has to infer is ambiguous wherever it is drawn. -->
  <TouchButton wide variant="secondary" onclick={() => onleave(false)}>
    Disconnect from {connection.label}
  </TouchButton>

  {#if connection.hasStoredKey}
    <!-- `text-bad!` is not decoration and the `!` is not optional. The shared
         Button's `danger` variant is `text-faint` at REST and only turns red on
         hover — which on a phone never happens, so the destructive choice
         rendered in exactly the same ink as "Disconnect" above it. The trailing
         `!` is CLAUDE.md's documented rule: a plain `text-bad` has the same
         specificity as the variant's `text-faint` and the winner would be
         decided by Tailwind's order in the compiled sheet. -->
    <TouchButton wide variant="danger" class="text-bad!" onclick={() => onleave(true)}>
      Forget this Mac
    </TouchButton>
  {/if}

  <TouchButton wide onclick={onclose}>Cancel</TouchButton>
</Sheet>
