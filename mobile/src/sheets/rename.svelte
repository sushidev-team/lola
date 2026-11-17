<script lang="ts">
  import { untrack } from "svelte";
  import Icon from "$lib/components/Icon.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import { store } from "$lib/store.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import { copy } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";

  // Renames the display title only; the branch and directory never move (spec §5.3).

  const id = $derived(router.top.name === "session" ? router.top.id : "");
  const original = untrack(() => store.sessionById(id)?.title ?? "");
  let name = $state(original);
  let busy = $state(false);

  let field = $state<HTMLInputElement>();
  const dirty = $derived(name !== original);

  // Opens ready to retype: focused with the whole name selected and the caret at its start.
  $effect(() => {
    const el = field;
    if (!el) return;
    el.focus();
    // Everything selected, caret at the start: a long name opens showing its beginning.
    el.setSelectionRange(0, el.value.length, "backward");
    el.scrollLeft = 0;
  });

  const clean = $derived(name.trim());
  const canSave = $derived(!busy && clean !== "" && clean !== original.trim());

  async function save(): Promise<void> {
    if (!canSave || !id) return;
    busy = true;
    const ok = await store.renameSession(id, clean);
    busy = false;
    if (ok) router.close();
  }
</script>

<Sheet
  label={copy.rename.title}
  title={copy.rename.title}
  action={{ label: copy.rename.save, onclick: () => void save(), disabled: !canSave }}
  {dirty}
  onclose={() => router.close()}
>
  <form class="pb-2" onsubmit={(e) => { e.preventDefault(); void save(); }}>
    <ListGroup>
      <div class="flex items-center" data-list-row>
        <input
          bind:this={field}
          class="min-w-0 flex-1 text-body text-ink outline-none placeholder:text-placeholder"
          aria-label={copy.rename.title}
          placeholder={copy.rename.placeholder}
          bind:value={name}
          maxlength="100"
          disabled={busy}
          autocapitalize="sentences"
        />
        {#if name}
          <button
            type="button"
            class="grid size-11 shrink-0 touch-manipulation place-items-center text-faint active:opacity-60"
            aria-label="Clear"
            onclick={() => {
              name = "";
              field?.focus();
            }}
          >
            <Icon name="circle-x" size={16} />
          </button>
        {/if}
      </div>
    </ListGroup>
  </form>
</Sheet>
