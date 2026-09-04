<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { buildRows } from "$lib/sidebarlayout";
  import { displayName } from "$lib/slug";
  import type { ProjectInfo } from "@bindings/internal/protocol";
  import SectionHeader from "@mobile/lib/components/SectionHeader.svelte";
  import { nav } from "@mobile/lib/nav.svelte";

  // The configured projects, in the arrangement the Mac shows them in.
  //
  // READ-ONLY, and it must stay that way. A project row on the desktop is an
  // editor — add, remove, drag into a folder, open the form — and every one of
  // those writes config.toml, which a phone cannot do (`ConfigService` is
  // `unsupported` in the shim, deliberately) and should not want to: PLAN.md's
  // rule is that this app never grows a control that could stop the daemon, and
  // removing a project is the quiet version of exactly that. Nothing on this
  // screen writes anything; a row opens the project's DETAIL, which is where
  // the facts and the handful of actions a phone is allowed to take live.
  //
  // THE ARRANGEMENT IS BORROWED, NOT REBUILT. `buildRows` is the desktop's own
  // pure layout function — ungrouped projects in `[[project]]` order with each
  // `[[group]]` spliced in at its own `position` — and it is the reason a folder
  // can be empty and still appear. Re-deriving the order here would be a second
  // reading of the same three config facts, and the one that drifts is always
  // the copy nobody has a test for. It is deliberately Svelte-free and
  // store-free, so it compiles here unchanged.
  const rows = $derived(buildRows(store.projects, store.groups));

  /**
   * Drill into a project.
   *
   * IT USED TO FILTER THE SESSIONS LIST AND JUMP TABS, and that behaviour has
   * not disappeared — it is the detail screen's own "Sessions" action, which
   * still writes the free-text query and switches tabs. What changed is that a
   * row is no longer the only thing this tab can do with a project, so spending
   * the tap on the narrowest of its uses would make the rest unreachable: the
   * facts (path, repo, agent health, the counts), the two pickers, the poll
   * controls. A phone row does ONE thing, so the one thing it does is open the
   * place where all of them are.
   *
   * THE NAME, NOT THE LABEL, is what goes in — and here it is identity rather
   * than a search term. `Name` is what this repository keys by everywhere: the
   * worktree path segment, the tmux prefix, the value every session carries in
   * its `project` field. `nav.project` holds that name and the detail resolves
   * it against the current push, so a project removed on the Mac between two
   * pushes renders its own "not found" instead of a stale object nothing can
   * refresh.
   */
  function open(p: ProjectInfo): void {
    nav.toProject(p.name);
  }
</script>

{#snippet projectRow(p: ProjectInfo)}
  <!-- The redesign's compact row: title over one meta line, a hairline under.
       `tap-row` rather than `tap` — a list row is already full width, so only
       the height needs the 44pt floor. -->
  <button
    type="button"
    class="tap-row flex w-full touch-manipulation flex-col gap-[3px] border-b border-edge-soft px-5 py-[11px] text-left active:bg-sel"
    onclick={() => open(p)}
  >
    <!-- NEVER A BARE `name`. A project has two names — `Name` is identity
         (paths, tmux, every protocol field) and `Label` is display — and
         CLAUDE.md's rule is that a UI renders the display one.
         `store.displayNameFor` falls back to the id for a project the daemon
         has not described yet, so a row always says something. -->
    <span class="truncate text-base font-medium text-ink">{store.displayNameFor(p.name)}</span>
    <span class="flex items-center gap-1.5 text-sm text-faint">
      {#if p.pollsEnabled > 0}
        <!-- The design gives a compact row a 6px dot "only while the agent is
             live". A project has no agent; the nearest thing it has to being
             live is a poll actually running, so that is what the dot means
             here. `good` because a poll that is on is the healthy state — a
             stopped poll is a choice, not a fault, so its absence is drawn as
             the absence of the dot rather than as a red one. -->
        <span class="size-1.5 shrink-0 rounded-full bg-good" aria-hidden="true"></span>
        <span>Polling</span>
      {:else}
        <span>Not polling</span>
      {/if}
      <span aria-hidden="true">·</span>
      <!-- `liveCounted`, the daemon's own "occupying a concurrency slot" count,
           rather than `sessions`: the total includes merged and dead ones, and
           a project reading "4 sessions" when all four are finished says the
           opposite of what a glance is asking. -->
      <span>{p.liveCounted} live</span>
    </span>
  </button>
{/snippet}

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <!-- Same header shape as every other screen in the redesign; see
       Activity.svelte for why the top inset is spelled out rather than taken
       from `pt-safe-t`. -->
  <header
    class="flex shrink-0 flex-col gap-0.5 px-5 pb-3"
    style="padding-top: calc(var(--lola-top-inset, env(safe-area-inset-top, 0px)) + 6px)"
  >
    <h1 class="flex h-11 items-center text-2xl text-ink">Projects</h1>
    <!-- `text-body`, not the `text-base` the Sessions header uses. That one is a
         facts line — three counts and a host name — and reads as data at the row
         size. This is a HINT, which the brief's scale puts one step down. -->
    <span class="truncate text-body text-subtext">Tap one for its facts and actions</span>
  </header>

  <!-- No bottom safe-area spacer: the tab bar pays that inset itself. -->
  <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain">
    {#each rows as row (row.kind === "group" ? `g:${row.group.name}` : `p:${row.project.name}`)}
      {#if row.kind === "group"}
        <!-- A folder is a SECTION here, not a row that can be opened. On the
             desktop it is a drop target and a collapse toggle; neither gesture
             exists on this screen, and a folder holds no facts of its own — it
             is arrangement only, which is exactly what a heading is for.
             SectionHeader gives it the count for free, and a count is the one
             thing an empty folder needs in order to read as empty rather than
             as broken. -->
        <SectionHeader title={displayName(row.group)} count={row.projects.length} />
        {#each row.projects as p (p.name)}
          {@render projectRow(p)}
        {/each}
      {:else}
        {@render projectRow(row.project)}
      {/if}
    {:else}
      <!-- Two genuinely different empty states, same reasoning as the sessions
           list: a push that has not arrived yet must not look like a Mac with
           nothing configured on it. And unlike the desktop there is no "add a
           project" action to offer, because that writes config.toml. -->
      <div class="flex flex-col items-center gap-2 px-8 py-12 text-center">
        {#if !store.connected}
          <span class="text-faint">Connecting…</span>
        {:else}
          <span class="text-lg text-ink">No projects</span>
          <span class="copy text-sm text-faint">
            Projects are configured on the Mac, in config.toml. This app only reads them.
          </span>
        {/if}
      </div>
    {/each}
  </div>
</div>
