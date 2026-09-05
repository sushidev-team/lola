<script lang="ts">
  // A LOOK-AT-IT HARNESS, and nothing else.
  //
  // It exists because the redesign's acceptance criterion is visual and the unit
  // tests deliberately are not: SessionCard.test.ts says in its own header that
  // it pins the card's JUDGEMENTS and leaves the geometry to the Figma frame, so
  // nothing in the suite would notice a card whose padding compiled to zero or a
  // section rule that lost its colour. jsdom has no layout and no stylesheet, so
  // it cannot notice either.
  //
  // It is dev-only: `mobile/preview.html` is not referenced by index.html, is not
  // an input to `vite build` (which builds index.html alone), and ships in no
  // bundle. It never touches the daemon — every session below is a literal — so
  // it can be opened with no Mac in reach, which is the other half of why it is
  // useful.
  //
  // NOT A SCREEN. It is a strip of the redesign's parts at their real sizes on a
  // 390-point ground. The screens themselves are reachable in the real app by a
  // development link (mobile/src/lib/devlink.ts), which is the supported way to
  // photograph one.

  import SessionCard from "./lib/components/SessionCard.svelte";
  import SessionRow from "./lib/components/SessionRow.svelte";
  import SectionHeader from "./lib/components/SectionHeader.svelte";
  import FilterRail from "./lib/components/FilterRail.svelte";
  import TabBar from "./lib/components/TabBar.svelte";
  import MetaPill from "./lib/components/MetaPill.svelte";
  import BackIcon from "./lib/icons/BackIcon.svelte";
  import BranchIcon from "./lib/icons/BranchIcon.svelte";
  import OverflowIcon from "./lib/icons/OverflowIcon.svelte";
  import FilterIcon from "./lib/icons/FilterIcon.svelte";
  import MacIcon from "./lib/icons/MacIcon.svelte";
  import AiGlyph from "./lib/icons/AiGlyph.svelte";
  import { applyFlavor } from "$lib/theme-runtime.svelte";
  // `flavorFor` is not among theme-runtime's re-exports; it comes from the pure
  // leaf, which is where the flavor table lives anyway.
  import { DEFAULT_THEME_ID, FLAVORS, flavorFor } from "$lib/catppuccin";
  import { applyMobileTokens } from "./lib/mobiletokens";
  import { store } from "$lib/store.svelte";
  import ProjectDetail from "./views/ProjectDetail.svelte";
  import { nav } from "./lib/nav.svelte";
  import type { ProjectInfo, SessionInfo } from "$lib/store.svelte";

  // `?theme=catppuccin-latte` so the derived tokens can be looked at in a LIGHT
  // flavor without a daemon. They are the whole reason this page exists twice:
  // `crust` is DARKER than `base` on the three dark flavors and LIGHTER on
  // latte, so a hard-coded tab bar would have been a black slab there and the
  // failure is invisible in the flavor the design was drawn in.
  // `applyFlavor` and not `appearance.set`: the setter also PERSISTS, which
  // means asking a daemon this page has none of. Painting is the whole job here.
  const wanted = new URLSearchParams(globalThis.location?.search ?? "").get("theme");
  const flavor = wanted ? flavorFor(wanted) : FLAVORS[DEFAULT_THEME_ID];
  applyFlavor(flavor);
  applyMobileTokens(flavor);

  let seq = 0;
  function s(over: Partial<SessionInfo>): SessionInfo {
    // A UNIQUE id per fixture. They all shared one, which every gallery
    // component tolerated (each is mounted alone) and a keyed {#each} over
    // sessions does not — ProjectDetail's live-session list keys by id, exactly
    // as it should, since real ids are unique by construction.
    seq += 1;
    return {
      id: `nori-app-${seq}`,
      project: "nori-app",
      issue: "NOR-414",
      title: "",
      status: "working",
      agentState: "working",
      delivery: "",
      interpretedState: "",
      headline: "",
      lastNotification: "",
      age: "12m",
      prNumber: 0,
      prUrl: "",
      checks: "",
      review: "",
      reacting: "",
      devActive: false,
      ...over,
    } as unknown as SessionInfo;
  }

  // The five sessions the Figma frame draws, verbatim.
  const needs = s({
    issue: "NOR-414",
    title: "FinanzOnline: Account balance and due dates with reminders",
    status: "needs_input",
    agentState: "waiting_input",
    lastNotification: "Finished formatting hooks and chart removal — awaiting commit confirmation.",
    age: "40m",
    prNumber: 352,
  });
  const broken = s({
    issue: "NOR-329",
    title: "Build out the system-template library to meet pricing tier counts",
    status: "ci_failed",
    lastNotification: "Re-running unit + parallel suites after a flaky Pest failure.",
    age: "43m",
    prNumber: 304,
    reacting: "ci_failed:1/2",
  });
  const rows = [
    s({
      issue: "NOR-402",
      title: "Extract the invoice presenter so the PDF and the mail share one shape",
      age: "12m",
    }),
    s({ issue: "NOR-397", title: "Add a rate limit to the export endpoint", age: "1h04m" }),
    s({
      issue: "NOR-388",
      title: "Split the settings form into tabs",
      status: "review_pending",
      agentState: "idle",
      age: "3h12m",
    }),
    s({
      issue: "NOR-408",
      title: "Hatched period curve replaces the band rails",
      status: "merged",
      agentState: "dead",
      age: "2h",
    }),
  ];

  let triage = $state("");
  const all = [needs, broken, ...rows];

  // The project screen reads the store singletons rather than props, so the
  // harness fills them. `?screen=project` swaps the whole page for it — the
  // strip above is a component gallery and this is a real screen.
  const showProject = new URLSearchParams(globalThis.location?.search ?? "").get("screen") === "project";
  store.projects = [
    {
      name: "nori-app",
      label: "Nori App",
      path: "/Volumes/Git/sushi/internal/nori/nori-app",
      repo: "sushidev-team/nori-app",
      defaultBranch: "main",
      agent: "claude",
      agentOk: true,
      agentErr: "",
      liveCounted: 2,
      needsYou: 1,
      ciRed: 1,
      pollsEnabled: 1,
      pollCount: 1,
      repoConfigured: true,
    } as unknown as ProjectInfo,
  ];
  store.sessions = all;
  nav.project = "nori-app";
</script>

{#if showProject}
  <div class="mx-auto h-dvh w-[390px] overflow-hidden bg-canvas text-ink">
    <ProjectDetail />
  </div>
{:else}
<div class="mx-auto flex h-dvh w-[390px] flex-col overflow-hidden bg-canvas text-ink">
  <header class="flex shrink-0 flex-col gap-0.5 px-5 pt-1.5 pb-3">
    <div class="flex h-11 items-center gap-1">
      <span class="text-2xl text-ink">Sessions</span>
      <span class="flex-1"></span>
      <span class="tap flex items-center justify-center rounded-[10px] text-subtext">
        <FilterIcon active />
      </span>
      <span class="tap flex items-center justify-center rounded-[10px] text-subtext">
        <MacIcon online />
      </span>
    </div>
    <div class="flex items-center gap-1.5">
      <span class="text-base font-medium text-orange">1 needs you</span>
      <span class="text-base text-faint">·</span>
      <span class="text-base font-medium text-faint">7 sessions</span>
      <span class="text-base text-faint">·</span>
      <span class="num text-sm font-medium text-faint">mars.local</span>
    </div>
  </header>

  <FilterRail bind:value={triage} sessions={all} />

  <div class="min-h-0 flex-1 overflow-y-auto">
    <SectionHeader title="Needs you" count={1} />
    <SessionCard session={needs} projectLabel="Nori App" onopen={() => {}} />
    <SectionHeader title="Fixing" count={1} />
    <SessionCard session={broken} projectLabel="Nori App" onopen={() => {}} />
    <SectionHeader title="Working" count={2} />
    <SessionRow session={rows[0]} projectLabel="Nori App" onopen={() => {}} />
    <SessionRow session={rows[1]} projectLabel="Okane" onopen={() => {}} />
    <SectionHeader title="In review" count={1} />
    <SessionRow session={rows[2]} projectLabel="Nori App" onopen={() => {}} />
    <SectionHeader title="Done today" count={1} />
    <SessionRow session={rows[3]} projectLabel="Nori App" onopen={() => {}} />

    <SectionHeader title="Detail chrome" />
    <!-- The identity row after the merge: back, key, spacer, PR badge, ONE
         button. The status is the word under the key, not a chip beside it. -->
    <div class="flex flex-col gap-0.5 px-3 py-2">
      <div class="flex items-center gap-2">
        <span class="tap flex items-center justify-center text-accent"><BackIcon /></span>
        <span class="num min-w-0 truncate text-base font-medium text-ink">NOR-414</span>
        <span class="flex-1"></span>
        <MetaPill tone="magenta">
          {#snippet leading()}<BranchIcon />{/snippet}
          #352
        </MetaPill>
        <span class="tap relative flex items-center justify-center text-subtext">
          <OverflowIcon />
          <span
            class="pointer-events-none absolute top-1.5 right-1.5 h-2 w-2 rounded-full bg-warn ring-2 ring-canvas"
          ></span>
        </span>
      </div>
      <div class="flex min-w-0 items-center gap-1.5 text-sm">
        <span class="shrink-0 text-orange">needs you</span>
        <span class="shrink-0 text-edge">·</span>
        <span class="min-w-0 truncate text-faint">
          FinanzOnline: Account balance and due dates with reminders
        </span>
      </div>
    </div>
    <div class="px-3 pt-2.5 pb-2">
      <div class="flex flex-col gap-2 rounded-[10px] border border-edge-soft bg-panel px-3 py-2.5">
        <div class="flex items-start gap-2">
          <span class="mt-px shrink-0 text-accent"><AiGlyph /></span>
          <span class="text-body text-subtext">
            Removed the chart from the accounting period view; waiting on a go-ahead to commit.
          </span>
        </div>
        <div class="flex items-center gap-1.5">
          <MetaPill tone="good">✓ CI pass</MetaPill>
          <MetaPill tone="grey" ground="grey">Permission prompt</MetaPill>
          <span class="flex-1"></span>
          <span class="num text-sm font-medium text-faint">2m ago</span>
        </div>
      </div>
    </div>
    <div class="h-6"></div>
  </div>

  <TabBar />
</div>
{/if}
