<script lang="ts">
  import { ConfigService } from "@bindings/desktop";
  import Button from "./Button.svelte";
  import Icon from "./Icon.svelte";

  let { value = $bindable(), label, kind = "folder", id, placeholder = "", disabled = false, onChange, onPick, onblur }: {
    value?: string;
    label: string;
    kind?: "folder" | "file";
    id?: string;
    placeholder?: string;
    disabled?: boolean;
    onChange?: (value: string) => void;
    onPick?: (value: string) => void;
    onblur?: () => void;
  } = $props();
  let busy = $state(false);
  let error = $state("");
  const errorId = $props.id();

  function change(next: string) {
    value = next;
    error = "";
    onChange?.(next);
  }

  async function browse() {
    if (disabled || busy) return;
    busy = true;
    error = "";
    const before = value;
    try {
      const start = (before ?? "").trim();
      const chosen = await (kind === "folder" ? ConfigService.PickFolder(start) : ConfigService.PickFile(start));
      // A late dialog result must not replace a newer value supplied by a parent.
      if (chosen && value === before) {
        change(chosen);
        onPick?.(chosen);
      }
    } catch (err) {
      error = String(err);
    } finally {
      busy = false;
    }
  }
</script>

<div class="grid min-w-0 gap-1">
  <div class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-2">
    <input {id} aria-label={label} aria-describedby={error ? errorId : undefined}
      class="w-full min-w-0 rounded border border-edge bg-canvas px-2 py-1.5 font-mono text-ink outline-none focus:border-accent placeholder:text-placeholder disabled:opacity-40"
      {placeholder} {value} disabled={disabled || busy} {onblur}
      oninput={(e) => change(e.currentTarget.value)} />
    <Button variant="secondary" class="h-auto! self-stretch" disabled={disabled} loading={busy} aria-label={`Browse for ${label.toLowerCase()}`} onclick={browse}>
      <Icon name="folder" size={14} />Browse…
    </Button>
  </div>
  {#if error}<p id={errorId} role="alert" class="text-sm text-bad">{error}</p>{/if}
</div>
