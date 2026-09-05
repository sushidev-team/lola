import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/svelte";
import { store } from "$lib/store.svelte";
import { nav } from "@mobile/lib/nav.svelte";
import type { ProjectInfo, SessionInfo } from "$lib/store.svelte";

// The project detail screen: the facts, the actions a phone is allowed to take,
// and the sessions of the project underneath them.
//
// THE DAEMON IS MOCKED AND THE STORE IS NOT. `store`, `nav` and `connection` are
// module singletons with `$state` fields, so a fixture is plain assignment —
// which is what makes these tests catch a renamed protocol field instead of
// agreeing with a mock about one. The three daemon calls this screen makes go
// through `@mobile/wailsshim`, the mobile-only barrel, and only those are
// faked: a component test has no socket, and pointing them at a real transport
// would be testing the bridge.

// `vi.hoisted` because `vi.mock`'s factory is lifted above every import in the
// file, so a plain `const` declared here would not exist yet when it runs. This
// is the documented way to share a fixture with one.
const daemon = vi.hoisted(() => ({
  Enable: vi.fn(async (_poll: string) => undefined),
  Disable: vi.fn(async (_poll: string) => undefined),
  PollOnce: vi.fn(async (_poll: string, _dryRun: boolean) => ({
    poll: "nori-app",
    dryRun: false,
    matches: [] as unknown[],
  })),
}));

vi.mock("@mobile/wailsshim", () => ({ DaemonService: daemon }));

// Imported AFTER the mock is declared, which vitest hoists above it anyway —
// spelled in this order so the dependency reads the way it resolves.
import ProjectDetail from "./ProjectDetail.svelte";

function p(over: Partial<ProjectInfo> = {}): ProjectInfo {
  return {
    name: "nori-app",
    label: "Nori",
    group: "",
    path: "/Volumes/Git/nori",
    repo: "sushidev-team/nori",
    defaultBranch: "main",
    agent: "claude",
    agentBin: "claude",
    agentOk: true,
    agentErr: "",
    pathOk: true,
    repoConfigured: true,
    pollCount: 1,
    pollsEnabled: 1,
    lastRun: "",
    sessions: 4,
    liveCounted: 2,
    needsYou: 1,
    ciRed: 0,
    openPrs: 1,
    ...over,
  } as unknown as ProjectInfo;
}

function s(over: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id: "nori-app-nor-401",
    project: "nori-app",
    issue: "NOR-401",
    title: "Email ingest",
    status: "working",
    agentState: "",
    delivery: "",
    interpretedState: "",
    age: "2h",
    prNumber: 0,
    ...over,
  } as unknown as SessionInfo;
}

/** The action list's rows, by their label. The descriptions ride in the same
 *  accessible name, so the label is matched at the start of it. */
function actionNames(): string[] {
  const group = screen.getByRole("group", { name: "Project actions" });
  return [...group.querySelectorAll("button")].map(
    (b) => b.querySelector("span > span")?.textContent?.trim() ?? "",
  );
}

function action(label: string): HTMLButtonElement {
  const group = screen.getByRole("group", { name: "Project actions" });
  return within(group).getByRole("button", { name: new RegExp(`^${label}`) }) as HTMLButtonElement;
}

beforeEach(() => {
  vi.clearAllMocks();
  store.projects = [p()];
  store.sessions = [];
  store.alive = true;
  store.connected = true;
  store.flash = null;
  // The screen refreshes after every daemon action. Stubbed so a test never
  // reaches the shim's real service namespaces through the store.
  store.refresh = vi.fn(async () => {});
  nav.project = "nori-app";
  nav.pick = "";
  nav.tab = "projects";
  nav.query = "";
  nav.triage = "";
});

describe("ProjectDetail header", () => {
  it("renders the DISPLAY name, and names the identity underneath it", () => {
    // CLAUDE.md's "a project has two names" invariant: `Name` is identity and
    // `Label` is display, and a UI renders the display one. The identity still
    // appears — as the subtitle — because it is what the Sessions action below
    // filters the list by.
    render(ProjectDetail);
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("Nori");
    expect(screen.getByText("nori-app")).toBeTruthy();
  });

  it("falls back to the repo for a project with no label", () => {
    // With no label the title IS the identity, so repeating it below would say
    // nothing.
    store.projects = [p({ label: "" })];
    const { container } = render(ProjectDetail);
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("nori-app");
    // Scoped to the header: the repo is also a row of the facts card below, and
    // what is being checked here is which string the SUBTITLE fell back to.
    expect(container.querySelector("header > span")?.textContent).toBe("sushidev-team/nori");
  });

  it("goes back to the project list", async () => {
    render(ProjectDetail);
    await fireEvent.click(screen.getByRole("button", { name: "Back to projects" }));
    expect(nav.project).toBe("");
  });
});

describe("ProjectDetail facts", () => {
  it("reads every fact off the ProjectInfo record", () => {
    store.projects = [
      p({
        path: "/Volumes/Git/nori",
        repo: "sushidev-team/nori",
        agent: "codex",
        defaultBranch: "develop",
        liveCounted: 2,
        needsYou: 1,
        ciRed: 3,
      }),
    ];
    render(ProjectDetail);

    expect(screen.getByText("/Volumes/Git/nori")).toBeTruthy();
    expect(screen.getByText("sushidev-team/nori")).toBeTruthy();
    expect(screen.getByText("codex")).toBeTruthy();
    expect(screen.getByText("develop")).toBeTruthy();
    expect(screen.getByText("● Polling")).toBeTruthy();
    expect(screen.getByText("✓ Agent ready")).toBeTruthy();
    expect(screen.getByText("2 live")).toBeTruthy();
    expect(screen.getByText("1 need you")).toBeTruthy();
    expect(screen.getByText("3 ci-red")).toBeTruthy();
  });

  it("omits the two attention counts at zero", () => {
    // "0 need you" is a sentence that draws the eye to say nothing.
    store.projects = [p({ needsYou: 0, ciRed: 0 })];
    render(ProjectDetail);
    expect(screen.queryByText(/need you/)).toBeNull();
    expect(screen.queryByText(/ci-red/)).toBeNull();
  });

  it("refuses to report a live count from a daemon that is not running", () => {
    store.alive = false;
    render(ProjectDetail);
    expect(screen.getByText("— live")).toBeTruthy();
    expect(screen.queryByText("2 live")).toBeNull();
  });

  it("says the agent is not ready, names why, and disables the launch actions", () => {
    store.projects = [p({ agentOk: false, agentErr: "claude not found on PATH" })];
    render(ProjectDetail);

    expect(screen.getByText("✗ Agent not ready")).toBeTruthy();
    expect(screen.getByText(/claude not found on PATH/)).toBeTruthy();
    // The desktop prints the same sentence and leaves its rows enabled; this
    // screen means it, because a sentence arguing against its own buttons is
    // worse than either behaviour on its own.
    expect(screen.getByText(/launch actions are disabled/)).toBeTruthy();
    expect(action("Open a PR").disabled).toBe(true);
    expect(action("Start a ticket").disabled).toBe(true);
    expect(action("New worktree").disabled).toBe(true);
    // Not a launch: filtering a list needs no agent.
    expect(action("Sessions").disabled).toBe(false);
  });

  it("tells a project removed on the Mac apart from a push that has not landed", () => {
    store.projects = [];
    store.connected = false;
    const { unmount } = render(ProjectDetail);
    expect(screen.getByText("Connecting…")).toBeTruthy();
    unmount();

    store.connected = true;
    render(ProjectDetail);
    expect(screen.getByText("Project not found")).toBeTruthy();
  });
});

describe("ProjectDetail actions", () => {
  it("draws exactly the six a phone may take, in order", () => {
    render(ProjectDetail);
    expect(actionNames()).toEqual([
      "Open a PR",
      "Start a ticket",
      "New worktree",
      "Sessions",
      "Poll now",
      "Stop polling",
    ]);
  });

  it("draws no config-writing action at all, not even disabled", () => {
    // Every ConfigService write answers `unsupported` through the shim, so the
    // desktop's "Polls" and "Edit project" rows are absent rather than dead: a
    // control that never works teaches people not to look at the controls.
    render(ProjectDetail);
    expect(screen.queryByText("Polls")).toBeNull();
    expect(screen.queryByText("Edit project")).toBeNull();
    expect(screen.queryByText(/Remove project/i)).toBeNull();
  });

  it("disables the PR row without a repo, and says why", () => {
    store.projects = [p({ repoConfigured: false })];
    render(ProjectDetail);

    const row = action("Open a PR");
    expect(row.disabled).toBe(true);
    expect(row.textContent).toContain("set a GitHub repo to list PRs");
    // The ticket picker needs no repo — Linear is the source there.
    expect(action("Start a ticket").disabled).toBe(false);
  });

  it("opens the two pickers through nav, never inline", async () => {
    render(ProjectDetail);
    await fireEvent.click(action("Open a PR"));
    expect(nav.pick).toBe("prs");

    nav.pick = "";
    await fireEvent.click(action("Start a ticket"));
    expect(nav.pick).toBe("tickets");
  });

  it("filters the sessions list by the project's IDENTITY and goes there", async () => {
    nav.triage = "In Review";
    render(ProjectDetail);
    await fireEvent.click(action("Sessions"));

    // The name, not the label: it is what every session of this project carries
    // in its `project` field, so the filter can over-match and never under-match.
    expect(nav.query).toBe("nori-app");
    expect(nav.triage).toBe("");
    expect(nav.tab).toBe("sessions");
  });

  it("polls once and reports what the tick found", async () => {
    daemon.PollOnce.mockResolvedValueOnce({
      poll: "nori-app",
      dryRun: false,
      matches: [{}, {}],
    } as never);
    render(ProjectDetail);
    await fireEvent.click(action("Poll now"));

    // dryRun false: this is a real tick, which is why the row's description
    // says it spawns.
    expect(daemon.PollOnce).toHaveBeenCalledWith("nori-app", false);
    expect(await screen.findByText("Polled once: 2 matches")).toBeTruthy();
  });

  it("shows the daemon's own sentence when it refuses a poll", async () => {
    daemon.PollOnce.mockRejectedValueOnce(new Error("poll nori-app is already running"));
    render(ProjectDetail);
    await fireEvent.click(action("Poll now"));

    // The refusal is the only half a person can act on, and `store.flash` — the
    // shared store's channel for it — is not drawn anywhere in this app.
    expect(await screen.findByText("poll nori-app is already running")).toBeTruthy();
  });

  it("toggles polling by the project's name, which IS the poll's name", async () => {
    render(ProjectDetail);
    await fireEvent.click(action("Stop polling"));
    // Awaited through the confirmation rather than the click: the row is
    // disabled while the move is in flight, so a second click before the reply
    // lands would find a dead button — which is the point of the busy flag.
    expect(await screen.findByText("Polling stopped")).toBeTruthy();
    expect(daemon.Disable).toHaveBeenCalledWith("nori-app");

    // The label follows the daemon's state, which reaches this screen as a new
    // ProjectInfo on the next push.
    store.projects = [p({ pollsEnabled: 0 })];
    await fireEvent.click(await screen.findByText("Start polling"));
    expect(await screen.findByText("Polling started")).toBeTruthy();
    expect(daemon.Enable).toHaveBeenCalledWith("nori-app");
  });

  it("disables both poll rows for a project with no Linear filter", () => {
    store.projects = [p({ pollCount: 0, pollsEnabled: 0 })];
    render(ProjectDetail);

    const once = action("Poll now");
    expect(once.disabled).toBe(true);
    expect(once.textContent).toContain("no Linear filter configured");
    expect(action("Start polling").disabled).toBe(true);
  });
});

describe("ProjectDetail new worktree", () => {
  async function openForm() {
    render(ProjectDetail);
    await fireEvent.click(action("New worktree"));
  }

  it("stays closed until the row is tapped", () => {
    render(ProjectDetail);
    expect(screen.queryByLabelText("Branch name")).toBeNull();
  });

  it("spawns through the store with the typed branch and the chosen agent", async () => {
    const openManual = vi.fn(async () => ({}) as never);
    store.openManual = openManual;
    await openForm();

    await fireEvent.input(screen.getByLabelText("Branch name"), {
      target: { value: "  feature/ingest  " },
    });
    await fireEvent.click(screen.getByRole("button", { name: "Start" }));

    // Trimmed, and the agent kind only when it overrides the project's default.
    expect(openManual).toHaveBeenCalledWith({
      project: "nori-app",
      branch: "feature/ingest",
      agent: true,
      agentKind: undefined,
    });
    // Success opens the sessions list, filtered to this project.
    expect(nav.query).toBe("nori-app");
    expect(nav.tab).toBe("sessions");
  });

  it("sends agent:false for a shell worktree", async () => {
    const openManual = vi.fn(async () => ({}) as never);
    store.openManual = openManual;
    await openForm();

    await fireEvent.click(screen.getByRole("button", { name: "Shell" }));
    await fireEvent.input(screen.getByLabelText("Branch name"), {
      target: { value: "spike" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "Start" }));

    expect(openManual).toHaveBeenCalledWith(
      expect.objectContaining({ agent: false, agentKind: undefined }),
    );
    // A shell has no agent to pick, so the picker is not drawn.
    expect(screen.queryByLabelText("Coding agent")).toBeNull();
  });

  it("KEEPS THE FORM AND THE TYPED BRANCH when the spawn fails", async () => {
    // `openManual` resolves to undefined on failure — the store swallows the
    // error into a flash — and the commonest failure here is a branch name that
    // already exists, i.e. one edit away from working. Clearing the field would
    // make every retry a re-type.
    store.openManual = vi.fn(async () => {
      store.flash = { text: "branch feature/ingest already exists", kind: "bad" };
      return undefined;
    });
    await openForm();

    const field = screen.getByLabelText("Branch name") as HTMLInputElement;
    await fireEvent.input(field, { target: { value: "feature/ingest" } });
    await fireEvent.click(screen.getByRole("button", { name: "Start" }));

    expect(screen.getByLabelText("Branch name")).toBeTruthy();
    expect((screen.getByLabelText("Branch name") as HTMLInputElement).value).toBe(
      "feature/ingest",
    );
    // And the daemon's sentence is on screen, because nothing else draws it.
    expect(await screen.findByText("branch feature/ingest already exists")).toBeTruthy();
    // Nothing navigated away from the half-finished form.
    expect(nav.tab).toBe("projects");
  });

  it("refuses to start on an empty branch", async () => {
    const openManual = vi.fn(async () => ({}) as never);
    store.openManual = openManual;
    await openForm();

    expect((screen.getByRole("button", { name: "Start" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
    expect(openManual).not.toHaveBeenCalled();
  });
});

describe("ProjectDetail sessions", () => {
  it("lists this project's sessions and opens a terminal on tap", async () => {
    store.sessions = [s({ id: "a", title: "Email ingest", tmuxName: "nori-app-nor-401" })];
    render(ProjectDetail);

    await fireEvent.click(screen.getByRole("button", { name: /Email ingest/ }));
    expect(nav.screen).toBe("terminal");
    expect(nav.paneSession).toBe("a");
    // paneNameFor: the tmux session name when one correlates, else the id.
    expect(nav.pane).toBe("nori-app-nor-401");
  });

  it("caps the list and hands the rest to the filtered sessions screen", async () => {
    store.sessions = Array.from({ length: 9 }, (_, i) =>
      s({ id: `s${i}`, issue: `NOR-${400 + i}`, title: `Task ${i}` }),
    );
    render(ProjectDetail);

    const more = screen.getByRole("button", { name: "Show 3 more" });
    await fireEvent.click(more);
    expect(nav.query).toBe("nori-app");
    expect(nav.tab).toBe("sessions");
  });

  it("says a project with no sessions is empty rather than drawing nothing", () => {
    render(ProjectDetail);
    expect(screen.getByText("No sessions in this project yet.")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Sessions 0" })).toBeTruthy();
  });
});
