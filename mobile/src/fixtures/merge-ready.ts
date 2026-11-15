import { ago, base, session, talk, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// Approved, green and mergeable: the merge sheet's happy path.
// It is second in the merge queue and shares a file with another session. An agent
// review ran on this PR and left a tally, which the Review segment's Agent review row shows.
const sessions = [
  {
    ...session({ id: "s5", issue: "NOR-390", title: "Migrate the reminder emails to the new template", agentState: "idle", delivery: "approved", pr: 345, checks: "pass", review: "approved", minutes: 75 }),
    mergeQueue: { position: 2, step: "waiting" },
    overlaps: [{ session: "s9", issue: "NOR-397", files: ["app/Mail/Reminder.php", "resources/views/mail/reminder.blade.php"] }],
    reviewTallies: [{ kind: "codex-session", major: 1, minor: 2, at: ago(20) }],
  },
];

export const fixture: Fixture = base("merge-ready", {
  routes: ["/session/s5/review?sheet=merge", "/session/s5/review"],
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
      checkList: [
        { name: "Unit tests", workflow: "CI", state: "SUCCESS", bucket: "pass", required: true, startedAt: ago(40), completedAt: ago(36) },
        { name: "Lint", workflow: "CI", state: "SUCCESS", bucket: "pass", startedAt: ago(40), completedAt: ago(39) },
      ],
    },
  },
});
