import { base, session, talk, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// CI failed on two checks; the agent is idle and re-running them.
const sessions = [
  session({ id: "s6", issue: "NOR-329", title: "Build out the system-template library to meet pricing tier counts", agentState: "idle", delivery: "ci_failed", pr: 304, checks: "fail", minutes: 43, reacting: "ci_failed:1/2",
    board: [["Unit tests", "fail"], ["Parallel suite", "fail"], ["Lint", "pass"]], headline: "Re-running unit and parallel suites after a flaky Pest failure." }),
];

export const fixture: Fixture = base("ci-red", {
  routes: ["/inbox", "/session/s6/review"],
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
    },
  },
});
