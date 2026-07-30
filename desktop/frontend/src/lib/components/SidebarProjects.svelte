<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { displayName } from "$lib/slug";
  import NavRow from "./NavRow.svelte";

  // The project switcher, moved out of the old Rail panel onto the shared
  // NavRow. Reads the store directly (leaf component) — see Sidebar.svelte.

  function pollDot(name: string): { glyph: string; cls: string; faint: boolean } {
    const ps = (store.status?.polls ?? []).find((p) => p.name === name);
    if (!ps) return { glyph: "·", cls: "text-faint", faint: true };
    if (ps.lastError) return { glyph: "●", cls: "text-bad", faint: false };
    if (ps.enabled) return { glyph: "●", cls: "text-good", faint: false };
    return { glyph: "○", cls: "text-faint", faint: true };
  }
</script>

<nav class="px-2 pt-3.5" aria-label="Projects">
  <div class="flex items-center px-2 pb-1">
    <h2 class="label text-faint">Projects</h2>
    <button
      class="ml-auto rounded px-1 text-sm text-faint opacity-0 transition-opacity group-hover/side:opacity-100 focus-visible:opacity-100 hover:text-accent-ink"
      title="add a project"
      aria-label="Add project"
      onclick={() => nav.openOverlay("project", "")}>+</button
    >
  </div>

  {#if store.projects.length === 0}
    <button
      class="w-full rounded-md border border-dashed border-edge px-2 py-3 text-center text-sm text-faint hover:border-accent hover:text-accent-ink"
      onclick={() => nav.openOverlay("project", "")}>no projects — add one</button
    >
  {:else}
    <!-- Capped so a long project list can never squeeze Activity out of the
         column; the list scrolls inside itself instead. -->
    <ul class="max-h-[38vh] overflow-auto">
      {#each store.projects as p (p.name)}
        {@const d = pollDot(p.name)}
        {@const active = nav.scoped && nav.project === p.name}
        <li>
          <NavRow
            label={displayName(p)}
            glyph={d.glyph}
            glyphCls={d.cls}
            dim={d.faint}
            {active}
            title="scope the cockpit to this project"
            onclick={() => nav.goCockpit(p.name)}
          >
            {#snippet badges()}
              <!-- Bare glyph counts on purpose: the triage row named "Needs You"
                   counts needs_input ONLY, while ProjectInfo.needsYou is the
                   wider attention set. Two different numbers must never appear
                   under one name, so this one never spells itself out. -->
              <!-- The glyph is aria-hidden and the meaning is spelled out in an
                   sr-only sibling: `title` on a <span> is a mouse-only tooltip
                   that no screen reader announces, so "3!" was reaching AT as the
                   bare string "3!". -->
              {#if p.needsYou > 0}<span class="num text-sm text-orange" title="{p.needsYou} need you"
                  ><span aria-hidden="true">{p.needsYou}!</span
                  ><span class="sr-only">{p.needsYou} need you</span></span
                >{/if}
              {#if p.ciRed > 0}<span class="num text-sm text-bad" title="{p.ciRed} failing CI"
                  ><span aria-hidden="true">{p.ciRed}✕</span
                  ><span class="sr-only">{p.ciRed} failing CI</span></span
                >{/if}
            {/snippet}
            {#snippet actions()}
              <button
                class="rounded px-1 text-faint hover:text-accent-ink"
                title="project settings"
                aria-label="{displayName(p)} settings"
                onclick={() => nav.openOverlay("project", p.name)}
              >
                <svg
                  viewBox="0 0 24 24"
                  class="h-3.5 w-3.5"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="1.9"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  aria-hidden="true"
                >
                  <circle cx="12" cy="12" r="3" />
                  <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
                </svg>
              </button>
              <button
                class="rounded px-1 text-faint hover:text-accent-ink"
                title="open project hub"
                aria-label="{displayName(p)} hub"
                onclick={() => nav.goDetail(p.name)}>›</button
              >
            {/snippet}
          </NavRow>
        </li>
      {/each}
    </ul>
  {/if}
</nav>
