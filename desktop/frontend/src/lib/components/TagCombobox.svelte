<script lang="ts" module>
  export type Tag = { value: string; label: string; mono?: boolean };
  export type Suggestion = { value: string; label: string; hint?: string };
</script>

<script lang="ts">
  // A tag input with suggestions (ARIA combobox + listbox): chips for what is
  // chosen, one text field that filters the suggestions, and free entry for
  // anything not suggested. Enter adds the highlighted suggestion or the typed
  // text; Backspace in an empty field removes the last chip; Escape closes the
  // list (and stops there, so a surrounding dialog stays open).
  import Button from "$lib/components/Button.svelte";
  import Icon from "$lib/components/Icon.svelte";

  let {
    label,
    tags,
    suggestions,
    placeholder = "",
    error = "",
    onAdd,
    onRemove,
  }: {
    label: string;
    tags: Tag[];
    suggestions: Suggestion[];
    placeholder?: string;
    error?: string;
    /** Returns false when the text was refused (the field keeps it). */
    onAdd: (text: string) => boolean;
    onRemove: (value: string) => void;
  } = $props();

  const id = $props.id();
  let query = $state("");
  let open = $state(false);
  let active = $state(-1);
  let input = $state<HTMLInputElement>();

  const shown = $derived.by(() => {
    const q = query.trim().toLowerCase();
    return suggestions.filter((s) => !q || s.label.toLowerCase().includes(q) || s.value.toLowerCase().includes(q)).slice(0, 8);
  });

  function add(text: string) {
    if (!text.trim()) return;
    if (onAdd(text.trim())) {
      query = "";
      active = -1;
    }
  }

  function onkeydown(e: KeyboardEvent) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      open = true;
      active = Math.min(active + 1, shown.length - 1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      active = Math.max(active - 1, -1);
    } else if (e.key === "Enter") {
      e.preventDefault();
      add(open && active >= 0 && shown[active] ? shown[active].value : query);
    } else if (e.key === "Escape") {
      if (open) {
        e.stopPropagation();
        open = false;
        active = -1;
      }
    } else if (e.key === "Backspace" && query === "" && tags.length > 0) {
      onRemove(tags[tags.length - 1].value);
    }
  }
</script>

<div class="relative grid gap-1">
  <div class="flex min-h-9 flex-wrap items-center gap-1.5 rounded border border-edge bg-canvas px-1.5 py-1 focus-within:border-accent">
    {#each tags as tag (tag.value)}
      <span class="inline-flex items-center gap-1 rounded bg-panel py-0.5 pl-2 pr-0.5 text-sm text-ink {tag.mono ? 'font-mono' : ''}">
        {tag.label}
        <Button size="xs" icon aria-label={`Remove ${tag.label}`} onclick={() => { onRemove(tag.value); input?.focus(); }}>
          <Icon name="x" size={12} />
        </Button>
      </span>
    {/each}
    <input
      bind:this={input}
      class="min-w-[8rem] flex-1 bg-transparent px-1 py-0.5 text-ink outline-none placeholder:text-placeholder"
      role="combobox"
      aria-label={label}
      aria-expanded={open && shown.length > 0}
      aria-controls={`${id}-list`}
      aria-autocomplete="list"
      aria-activedescendant={open && active >= 0 ? `${id}-opt-${active}` : undefined}
      aria-invalid={error ? "true" : undefined}
      spellcheck="false"
      autocomplete="off"
      {placeholder}
      bind:value={query}
      oninput={() => { open = true; active = -1; }}
      onfocus={() => (open = true)}
      onblur={() => setTimeout(() => (open = false), 120)}
      {onkeydown}
    />
  </div>
  {#if open && shown.length > 0}
    <ul id={`${id}-list`} role="listbox" aria-label={`${label} suggestions`}
      class="absolute left-0 right-0 top-full z-20 mt-1 max-h-64 overflow-auto rounded border border-edge bg-panel py-1 shadow-lg">
      {#each shown as s, i (s.value)}
        <li id={`${id}-opt-${i}`} role="option" aria-selected={i === active}
          class="flex cursor-pointer items-center justify-between gap-3 px-2.5 py-1.5 text-sm {i === active ? 'bg-edge text-ink' : 'text-ink'}"
          onmousedown={(e) => { e.preventDefault(); add(s.value); }}
          onmouseenter={() => (active = i)}>
          <span>{s.label}</span>
          {#if s.hint}<span class="text-faint">{s.hint}</span>{/if}
        </li>
      {/each}
    </ul>
  {/if}
  {#if error}<p role="alert" class="text-sm text-bad">{error}</p>{/if}
</div>
