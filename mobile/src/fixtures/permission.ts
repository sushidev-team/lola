import { base, permissionPrompt, session, shortStory, talk, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// A session blocked on a permission prompt with the three choices Claude offers.
const s1 = session({
  id: "s1",
  issue: "NOR-414",
  title: "FinanzOnline: Account balance and due dates with reminders",
  agentState: "waiting_input",
  inputReason: "permission_prompt",
  delivery: "review_pending",
  pr: 352,
  minutes: 40,
  headline: "Finished formatting hooks and chart removal; awaiting commit confirmation.",
});
const sessions = [s1];

export const fixture: Fixture = base("permission", {
  routes: ["/inbox", "/session/s1/conversation"],
  sessions,
  projects: withCounts(PROJECTS, sessions),
  talk: {
    s1: talk("s1", {
      prompt: permissionPrompt(["Bash command", "git commit -m \"Remove the chart from the period view\"", "Do you want to proceed?"]),
      feedSeq: 6,
    }),
  },
  feeds: { s1: shortStory("The change is staged. I need your go-ahead to commit it.") },
});
