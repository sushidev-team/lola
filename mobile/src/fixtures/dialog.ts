import { ago, base, session, shortStory, talk, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// A session whose pane shows a menu that only takes keys: the needs-you dialog card with its one
// way out, Open terminal. Same session as busy's s3.
const sessions = [
  session({ id: "s3", project: "okane", issue: "OKA-88", title: "Export the ledger as CSV", agentState: "waiting_input", inputReason: "dialog", minutes: 9 }),
];

export const fixture: Fixture = base("dialog", {
  routes: ["/session/s3/conversation"],
  sessions,
  projects: withCounts(PROJECTS, sessions),
  talk: {
    s3: talk("s3", {
      feedSeq: 6,
      prompt: {
        kind: "modal",
        hash: "modal-0001",
        lines: ["Select model", "", "  1. Default (recommended)", "> 2. Opus", "  3. Sonnet", "  4. Haiku", "", "Enter to confirm, Esc to exit"],
        choices: [],
        since: ago(2),
      },
    }),
  },
  feeds: { s3: shortStory("I opened the model menu to switch before the export.") },
});
