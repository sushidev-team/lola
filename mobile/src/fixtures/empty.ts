import { base, session, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// A paired Mac where nothing needs you: two agents work and one PR waits on
// review, so the compact "Nothing needs you" row leads the list.
const sessions = [
  session({ id: "e1", issue: "NOR-402", title: "Extract the invoice presenter", minutes: 12 }),
  session({ id: "e2", issue: "NOR-397", title: "Add a rate limit to the export endpoint", minutes: 64 }),
  session({ id: "e3", issue: "NOR-388", title: "Split the settings form into tabs", agentState: "idle", delivery: "review_pending", pr: 341, minutes: 192 }),
];

export const fixture: Fixture = base("empty", {
  routes: ["/inbox", "/projects"],
  sessions,
  projects: withCounts(PROJECTS, sessions),
});
