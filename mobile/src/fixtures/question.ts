import { base, questionPrompt, session, shortStory, talk, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// An AskUserQuestion prompt with four choices.
const s2 = session({
  id: "s2",
  issue: "NOR-411",
  title: "Account balance chart",
  agentState: "waiting_input",
  inputReason: "question",
  minutes: 22,
  headline: "Which period should the balance chart cover?",
});
const sessions = [s2];

// Claude's own free-text row is "Type something."; the card's toTerminal rule renders it as "Other…".
const prompt = questionPrompt();
prompt.choices = (prompt.choices ?? []).map((c) => (c.key === "4" ? { ...c, label: "Type something." } : c));

export const fixture: Fixture = base("question", {
  routes: ["/session/s2/conversation"],
  sessions,
  projects: withCounts(PROJECTS, sessions),
  talk: { s2: talk("s2", { prompt, feedSeq: 6 }) },
  feeds: { s2: shortStory("I can draw the chart over several periods. Which one do you want?") },
});
