import { ago, base, event, permissionPrompt, session, talk, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// Fourteen sessions over every column: three need the person, two are broken,
// five work, two wait on review and two are done.
const sessions = [
  session({ id: "s1", issue: "NOR-414", title: "FinanzOnline: Account balance and due dates with reminders", agentState: "waiting_input", inputReason: "permission_prompt", delivery: "review_pending", pr: 352, minutes: 40, headline: "Awaiting commit confirmation." }),
  session({ id: "s2", issue: "NOR-411", title: "Which period should the balance chart cover?", agentState: "waiting_input", inputReason: "question", minutes: 22 }),
  session({ id: "s3", project: "okane", issue: "OKA-88", title: "Export the ledger as CSV", agentState: "waiting_input", inputReason: "dialog", minutes: 9 }),
  session({ id: "s6", issue: "NOR-329", title: "Build out the system-template library to meet pricing tier counts", agentState: "idle", delivery: "ci_failed", pr: 304, checks: "fail", minutes: 43, reacting: "ci_failed:1/2" }),
  session({ id: "s7", project: "okane", issue: "OKA-91", title: "Rename the ledger columns", agentState: "idle", delivery: "changes_requested", pr: 77, review: "changes_requested", minutes: 130 }),
  session({ id: "s8", issue: "NOR-402", title: "Extract the invoice presenter so the PDF and the mail share one shape", minutes: 12 }),
  session({ id: "s9", issue: "NOR-397", title: "Add a rate limit to the export endpoint", minutes: 64 }),
  session({ id: "s10", project: "okane", issue: "OKA-90", title: "Fix the rounding on split payments", delivery: "ci_pending", pr: 79, minutes: 31 }),
  session({ id: "s11", issue: "NOR-420", title: "Hatched period curve replaces the band rails", minutes: 5 }),
  session({ id: "s12", issue: "NOR-421", title: "Split the settings form into tabs", minutes: 2 }),
  session({ id: "s4", issue: "NOR-388", title: "Split the settings form into tabs, round two", agentState: "idle", delivery: "review_pending", pr: 341, minutes: 192 }),
  session({ id: "s5", issue: "NOR-390", title: "Migrate the reminder emails to the new template", agentState: "idle", delivery: "approved", pr: 345, checks: "pass", review: "approved", minutes: 75 }),
  session({ id: "s13", issue: "NOR-408", title: "Retire the legacy period picker", agentState: "dead", delivery: "merged", pr: 330, minutes: 120 }),
  session({ id: "s14", project: "okane", issue: "OKA-85", title: "Drop the unused currency table", agentState: "dead", delivery: "merged", pr: 71, minutes: 300 }),
];

const TRANSITIONS: Array<[string, string]> = [
  ["working", "needs_input"],
  ["working", "ci_failed"],
  ["review_pending", "approved"],
  ["approved", "merged"],
  ["ci_failed", "working"],
];

// Thirty activity rows, newest first.
const events = Array.from({ length: 30 }, (_, i) => {
  const s = sessions[i % sessions.length];
  const [from, to] = TRANSITIONS[i % TRANSITIONS.length];
  return event(`e${i}`, s.issue || s.id, from, to, 3 + i * 7, s.title);
});

export const fixture: Fixture = base("busy", {
  // Two sessions that left the roster: the Activity tab's "Recently ended".
  ended: [
    { id: "s13", project: "nori-app", title: "Retire the legacy period picker", issue: "NOR-408", endedAt: ago(120), reason: "PR #330 merged", prNumber: 330 },
    { id: "s14", project: "okane", title: "Drop the unused currency table", issue: "OKA-85", endedAt: ago(300), reason: "PR #71 merged", prNumber: 71 },
  ],
  routes: ["/inbox", "/inbox?filter=needs", "/inbox?filter=working", "/inbox?filter=review", "/inbox?filter=done", "/activity"],
  sessions,
  events,
  projects: withCounts(PROJECTS, sessions),
  // The permission session is watched, so its card shows the request and the one-tap Approve.
  talk: {
    s1: talk("s1", {
      prompt: permissionPrompt(["Bash command", "git commit -m \"Remove the chart from the period view\"", "Do you want to proceed?"]),
      feedSeq: 6,
    }),
  },
  prs: {
    repo: "sushidev-team/nori-app",
    ageSeconds: 40,
    stale: false,
    prs: [
      { number: 352, title: "FinanzOnline: account balance and due dates", author: "martin", branch: "lola/nor-414", isDraft: false, isFork: false, checks: "pass", review: "", url: "https://github.com/sushidev-team/nori-app/pull/352", status: "review_pending", alreadyOpen: true },
      { number: 358, title: "Bump vite to 8.3", author: "dependabot[bot]", branch: "dependabot/npm_and_yarn/vite-8.3", isDraft: false, isFork: false, checks: "fail", review: "", url: "https://github.com/sushidev-team/nori-app/pull/358", status: "ci_failed", alreadyOpen: false },
      { number: 357, title: "Draft: rework the reminder scheduler", author: "anna", branch: "anna/reminders", isDraft: true, isFork: false, checks: "pending", review: "", url: "https://github.com/sushidev-team/nori-app/pull/357", status: "ci_pending", alreadyOpen: false },
      { number: 349, title: "Fix the rounding on split payments", author: "ben", branch: "ben/rounding", isDraft: false, isFork: true, checks: "pass", review: "changes_requested", url: "https://github.com/sushidev-team/nori-app/pull/349", status: "changes_requested", alreadyOpen: false },
    ],
  },
  tickets: {
    team: "team-nori",
    teamName: "Nori",
    teamKey: "NOR",
    issues: [
      { identifier: "NOR-431", uuid: "u1", title: "Show the due date in the reminder email subject", branch: "nor-431", priority: 2, state: "Todo", stateType: "unstarted", assignee: "Martin", labels: ["lola", "email"], estimate: 2, updated: "2h", alreadyLive: false },
      { identifier: "NOR-428", uuid: "u2", title: "Balance chart flickers when switching the period", branch: "nor-428", priority: 1, state: "In Progress", stateType: "started", assignee: "Anna", labels: ["bug"], estimate: 3, updated: "5h", alreadyLive: false },
      { identifier: "NOR-420", uuid: "u3", title: "Hatched period curve replaces the band rails", branch: "nor-420", priority: 3, state: "In Progress", stateType: "started", labels: ["lola"], updated: "1d", alreadyLive: true },
      { identifier: "NOR-415", uuid: "u4", title: "Archive closed accounts after twelve months", branch: "nor-415", priority: 0, state: "Backlog", stateType: "backlog", updated: "Oct 4", alreadyLive: false },
    ],
  },
});
