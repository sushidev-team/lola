import { ago, base, session, talk, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// CI failed on two checks (one required); the agent is idle and re-running them. The
// readiness answer carries the per-check list the checks sheet groups.
const sessions = [
  session({ id: "s6", issue: "NOR-329", title: "Build out the system-template library to meet pricing tier counts", agentState: "idle", delivery: "ci_failed", pr: 304, checks: "fail", minutes: 43, reacting: "ci_failed:1/2",
    board: [["Unit tests", "fail"], ["Parallel suite", "fail"], ["Lint", "pass"]], headline: "Re-running unit and parallel suites after a flaky Pest failure." }),
];

export const fixture: Fixture = base("ci-red", {
  routes: ["/inbox", "/session/s6/review", "/session/s6/review?sheet=checks"],
  sessions,
  projects: withCounts(PROJECTS, sessions),
  talk: { s6: talk("s6") },
  readiness: {
    s6: {
      repo: "sushidev-team/nori-app",
      number: 304,
      url: "https://github.com/sushidev-team/nori-app/pull/304",
      state: "OPEN",
      draft: false,
      mergeable: "MERGEABLE",
      mergeState: "UNSTABLE",
      review: "",
      checks: "fail",
      head: "a4f02c9d11",
      base: "main",
      threads: [],
      mergeMethod: "squash",
      mergeBlocked: "2 checks failing: Unit tests, Parallel suite",
      checkList: [
        { name: "Unit tests", workflow: "CI", state: "FAILURE", bucket: "fail", required: true, url: "https://github.com/sushidev-team/nori-app/actions/runs/101", startedAt: ago(9), completedAt: ago(5) },
        { name: "Parallel suite", workflow: "CI", state: "FAILURE", bucket: "fail", url: "https://github.com/sushidev-team/nori-app/actions/runs/102", startedAt: ago(9), completedAt: ago(4) },
        { name: "Browser tests", workflow: "CI", state: "IN_PROGRESS", bucket: "pending", url: "https://github.com/sushidev-team/nori-app/actions/runs/103", startedAt: ago(2) },
        { name: "Lint", workflow: "CI", state: "SUCCESS", bucket: "pass", required: true, url: "https://github.com/sushidev-team/nori-app/actions/runs/104", startedAt: ago(9), completedAt: ago(8) },
        { name: "Typecheck", workflow: "CI", state: "SUCCESS", bucket: "pass", startedAt: ago(9), completedAt: ago(7) },
        { name: "Deploy preview", state: "SKIPPED", bucket: "skipped" },
        { name: "lola/review/codex-session", state: "SUCCESS", bucket: "pass", gate: true },
      ],
    },
  },
});
