// The small roster the two failed-link fixtures show as their last snapshot.

import { base as start, session, withCounts, PROJECTS } from "./build";
import type { Fixture, FixtureName } from "./types";

const sessions = [
  session({ id: "s1", issue: "NOR-414", title: "FinanzOnline: Account balance and due dates with reminders", agentState: "waiting_input", inputReason: "permission_prompt", delivery: "review_pending", pr: 352, minutes: 40, headline: "Awaiting commit confirmation." }),
  session({ id: "s6", issue: "NOR-329", title: "Build out the system-template library to meet pricing tier counts", agentState: "idle", delivery: "ci_failed", pr: 304, checks: "fail", minutes: 43 }),
  session({ id: "s8", issue: "NOR-402", title: "Extract the invoice presenter so the PDF and the mail share one shape", minutes: 12 }),
  session({ id: "s4", issue: "NOR-388", title: "Split the settings form into tabs", agentState: "idle", delivery: "review_pending", pr: 341, minutes: 192 }),
];

export function snapshot(name: FixtureName): Fixture {
  return start(name, { sessions, projects: withCounts(PROJECTS, sessions) });
}
