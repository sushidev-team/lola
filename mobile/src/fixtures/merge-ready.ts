import { base, session, talk, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// Approved, green and mergeable: the merge sheet's happy path.
const sessions = [
  session({ id: "s5", issue: "NOR-390", title: "Migrate the reminder emails to the new template", agentState: "idle", delivery: "approved", pr: 345, checks: "pass", review: "approved", minutes: 75 }),
];

export const fixture: Fixture = base("merge-ready", {
  routes: ["/session/s5/review?sheet=merge"],
  sessions,
  projects: withCounts(PROJECTS, sessions),
  talk: { s5: talk("s5") },
  readiness: {
    s5: {
      repo: "sushidev-team/nori-app",
      number: 345,
      url: "https://github.com/sushidev-team/nori-app/pull/345",
      state: "OPEN",
      draft: false,
      mergeable: "MERGEABLE",
      mergeState: "CLEAN",
      review: "approved",
      checks: "pass",
      head: "5c1d9e07b2",
      base: "main",
      threads: [],
      mergeMethod: "squash",
      mergeBlocked: "",
    },
  },
});
