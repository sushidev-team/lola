<script lang="ts">
  import { store, scopedSessions } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { triaged } from "$lib/filters";
  import Button from "./Button.svelte";

  // The main column's 44px context header. It replaces the old full-width vitals
  // bar: it starts at the SIDEBAR's right edge, not the window's, so together
  // with the sidebar's own 44px brand row the entire top 44px of the window is
  // one continuous drag band in both sidebar states.
  //
  // Reads `store` and `nav` directly (leaf component) — it is never handed a
  // title or a count by App. See Sidebar.svelte for why.

  type Crumb = { text: string; onclick?: () => void };

  const projectName = $derived(store.displayNameFor(nav.project));

  const crumbs = $derived.by<Crumb[]>(() => {
    switch (nav.view) {
      case "home":
        return [{ text: "Projects" }];
      case "detail":
        return [{ text: "Projects", onclick: () => nav.goHome() }, { text: projectName }];
      case "prpicker":
        return [{ text: projectName, onclick: () => nav.goDetail(nav.project) }, { text: "Open a PR" }];
      case "ticketpicker":
        return [
          { text: projectName, onclick: () => nav.goDetail(nav.project) },
          { text: "Start a ticket" },
        ];
      default: {
        const c: Crumb[] = nav.scoped
          ? [{ text: "All", onclick: () => nav.goCockpit("") }, { text: projectName }]
          : [{ text: "Sessions" }];
        // The filter is a crumb, and clicking it is how you drop it — the same
        // affordance as Escape, for people who never learn Escape.
        if (nav.triage) c.push({ text: nav.triage, onclick: () => nav.setTriage("") });
        return c;
      }
    }
  });

  // The number of rows actually visible below — the count that used to sit in
  // the sessions panel header.
  const count = $derived(triaged(scopedSessions(store.sessions, nav.scoped, nav.project), nav.triage).length);

  // Mirrors <SidebarStatus>'s health read so the collapsed-sidebar alarm below
  // fires on the same condition the sidebar chip does.
  const daemonOk = $derived(
    store.alive && !!store.status && store.status.runtimeOk && store.status.linearOk,
  );
  const daemonHealth = $derived(
    store.status
      ? `runtime ${store.status.runtimeOk ? "✓" : "✗"} · linear ${store.status.linearOk ? "✓" : "✗"}`
      : "daemon health unknown",
  );

  // The lens picker draws its three glyphs as SVG rather than as the box-drawing
  // characters it used to spend (≡ ▤ ▦). Those are text: the font sets their
  // weight, width and baseline, so the three sat at different sizes, none of them
  // optically centred, in buttons that were pill-shaped around them. Drawn on the
  // same 24-unit grid with the same stroke as the sidebar's gear, they finally
  // read as one set — and they can then sit in genuinely square buttons.
  const lenses: { id: "list" | "kanban" | "grid"; label: string }[] = [
    { id: "list", label: "List" },
    { id: "kanban", label: "Board" },
    { id: "grid", label: "Terminals" },
  ];
</script>

{#snippet lensIcon(id: "list" | "kanban" | "grid")}
  <svg
    viewBox="0 0 24 24"
    class="h-4 w-4"
    fill="none"
    stroke="currentColor"
    stroke-width="1.9"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    {#if id === "list"}
      <!-- Rows with a leading marker: a plain three-line glyph reads as a hamburger menu. -->
      <line x1="9" y1="6" x2="20" y2="6" />
      <line x1="9" y1="12" x2="20" y2="12" />
      <line x1="9" y1="18" x2="20" y2="18" />
      <line x1="4" y1="6" x2="4.01" y2="6" />
      <line x1="4" y1="12" x2="4.01" y2="12" />
      <line x1="4" y1="18" x2="4.01" y2="18" />
    {:else if id === "kanban"}
      <!-- A framed board with two stacks of unequal height. Drawn as bars inside
           a frame rather than as three bare outlined columns: at 16px an outlined
           rect is nearly all stroke, and the three of them read as loose boxes
           rather than as one board. -->
      <rect x="3" y="3" width="18" height="18" rx="2.5" />
      <line x1="8.5" y1="7.5" x2="8.5" y2="16.5" />
      <line x1="15.5" y1="7.5" x2="15.5" y2="12.5" />
    {:else}
      <rect x="3.5" y="3.5" width="7.5" height="7.5" rx="1.5" />
      <rect x="13" y="3.5" width="7.5" height="7.5" rx="1.5" />
      <rect x="3.5" y="13" width="7.5" height="7.5" rx="1.5" />
      <rect x="13" y="13" width="7.5" height="7.5" rx="1.5" />
    {/if}
  </svg>
{/snippet}

<header
  class="drag flex h-11 items-center gap-2 border-b border-edge bg-canvas pr-3 {nav.sidebarOpen
    ? 'pl-3'
    : 'pl-[82px]'}"
>
  {#if !nav.sidebarOpen}
    <!-- Placed AFTER the traffic-light padding, never inside it. -->
    <Button icon title="show sidebar (b)" aria-label="Show sidebar" onclick={() => nav.toggleSidebar()}>»</Button>
  {/if}

  {#if nav.view !== "cockpit"}
    <Button
      icon
      title="back to sessions (Esc)"
      aria-label="Back to sessions"
      onclick={() => nav.goCockpit(nav.scoped ? nav.project : "")}>←</Button
    >
  {/if}

  <nav class="flex min-w-0 items-baseline gap-1.5" aria-label="Breadcrumb">
    {#each crumbs as c, i (c.text + i)}
      {#if i > 0}<span class="text-lg text-faint" aria-hidden="true">▸</span>{/if}
      {#if c.onclick}
        <button class="truncate text-lg text-faint hover:text-ink" onclick={c.onclick}>{c.text}</button>
      {:else}
        <span class="truncate text-lg" class:text-faint={i < crumbs.length - 1}>{c.text}</span>
      {/if}
    {/each}
    {#if nav.view === "cockpit"}
      <span class="num shrink-0 text-sm text-faint">{count}</span>
    {/if}
  </nav>

  <span class="ml-auto flex shrink-0 items-center gap-2">
    <!-- Daemon alarm, ONLY while the sidebar is collapsed. <SidebarStatus> is the
         permanent home for liveness, but it lives inside the collapsible <aside>,
         and `sidebarOpen` persists to localStorage — so with the sidebar hidden a
         dead daemon had no indicator anywhere: the store keeps serving its last
         snapshot, and SessionsEmpty's "daemon isn't running" hero only renders
         when the list is empty. Silent while everything is fine, so the bar stays
         quiet; opens the doctor, same as the sidebar chip. -->
    {#if !nav.sidebarOpen && store.connected && !daemonOk}
      <!-- Trailing `!` on the colour: it has to beat the variant's own text-faint,
           and equal-specificity utilities are resolved by Tailwind's sheet order,
           not by the class attribute. -->
      <Button
        class={store.alive ? "text-warn!" : "text-bad!"}
        title="{daemonHealth} · open doctor (d)"
        onclick={() => nav.openOverlay("doctor")}
      >
        <span aria-hidden="true">{store.alive ? "▲" : "○"}</span>
        <span>{store.alive ? "Degraded" : "Daemon down"}</span>
      </Button>
    {/if}
    {#if nav.view === "cockpit"}
      <!-- Square buttons in a hairline rail: a segmented control of icons, so the
           three cells must be the same shape whatever is drawn in them. `icon` at
           `xs` gives that (24px) — the old `size="xs"` text buttons were pills
           whose width followed their glyph. The icon inside is 16px, i.e. the cell
           is mostly icon and only 4px of gutter: a picker in the title bar should
           spend its space on the marks, not around them. -->
      <span class="flex items-center gap-0.5 rounded-lg border border-edge p-0.5">
        {#each lenses as l (l.id)}
          <Button
            size="xs"
            icon
            selected={nav.lens === l.id}
            title={l.label}
            aria-label="{l.label} lens"
            onclick={() => nav.setLens(l.id)}
          >
            {@render lensIcon(l.id)}
          </Button>
        {/each}
      </span>
    {:else if nav.view === "home"}
      <Button variant="secondary" title="add a project" onclick={() => nav.openOverlay("project", "")}>
        <span aria-hidden="true">+</span> Add project
      </Button>
    {/if}
  </span>
</header>
