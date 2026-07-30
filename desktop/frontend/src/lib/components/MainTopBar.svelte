<script lang="ts">
  import { store, scopedSessions } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { triaged } from "$lib/filters";

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

  const lenses: { id: "list" | "kanban" | "grid"; icon: string; label: string }[] = [
    { id: "list", icon: "≡", label: "list" },
    { id: "kanban", icon: "▤", label: "board" },
    { id: "grid", icon: "▦", label: "terminals" },
  ];
</script>

<header
  class="drag flex h-11 items-center gap-2 border-b border-edge bg-canvas pr-3 {nav.sidebarOpen
    ? 'pl-3'
    : 'pl-[82px]'}"
>
  {#if !nav.sidebarOpen}
    <!-- Placed AFTER the traffic-light padding, never inside it. -->
    <button
      class="rounded p-1 text-faint hover:bg-sel hover:text-ink"
      title="show sidebar (b)"
      aria-label="Show sidebar"
      onclick={() => nav.toggleSidebar()}>»</button
    >
  {/if}

  {#if nav.view !== "cockpit"}
    <button
      class="rounded p-1 text-faint hover:bg-sel hover:text-ink"
      title="back to sessions (Esc)"
      aria-label="Back to sessions"
      onclick={() => nav.goCockpit(nav.scoped ? nav.project : "")}>←</button
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
      <button
        class="flex items-center gap-1.5 rounded px-1.5 py-1 {store.alive ? 'text-warn' : 'text-bad'} hover:bg-sel"
        title="{daemonHealth} · open doctor (d)"
        onclick={() => nav.openOverlay("doctor")}
      >
        <span aria-hidden="true">{store.alive ? "▲" : "○"}</span>
        <span>{store.alive ? "degraded" : "daemon down"}</span>
      </button>
    {/if}
    {#if nav.view === "cockpit"}
      <span class="flex items-center gap-0.5 rounded border border-edge p-0.5">
        {#each lenses as l (l.id)}
          <button
            class="rounded px-1.5 py-[1px] text-sm"
            class:bg-accent={nav.lens === l.id}
            class:text-on-accent={nav.lens === l.id}
            class:text-faint={nav.lens !== l.id}
            title={l.label}
            aria-label="{l.label} lens"
            aria-pressed={nav.lens === l.id}
            onclick={() => nav.setLens(l.id)}>{l.icon}</button
          >
        {/each}
      </span>
    {:else if nav.view === "home"}
      <button
        class="rounded border border-edge px-2 py-1 text-faint hover:border-accent hover:text-accent-ink"
        title="add a project"
        onclick={() => nav.openOverlay("project", "")}>+ Add project</button
      >
    {/if}
  </span>
</header>
