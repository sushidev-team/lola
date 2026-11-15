import { base, session, talk, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// The agent's pane as a resync of an 80 by 24 screen.
const sessions = [
  session({ id: "s1", issue: "NOR-414", title: "FinanzOnline: Account balance and due dates with reminders", agentState: "idle", minutes: 40 }),
];

const ESC = "\u001b";
const dim = (s: string) => `${ESC}[2m${s}${ESC}[0m`;
const bold = (s: string) => `${ESC}[1m${s}${ESC}[0m`;
const green = (s: string) => `${ESC}[32m${s}${ESC}[0m`;
const rule = "─".repeat(78);

const lines = [
  `${bold("●")} I removed the chart and kept the due dates.`,
  "",
  `  ${green("✓")} src/Period.svelte`,
  `  ${green("✓")} src/lib/due.ts`,
  "",
  `${bold("●")} Bash(npm test -- --run)`,
  `  ${dim("⎿")} ${green("Test Files  12 passed (12)")}`,
  `  ${dim("⎿")} ${green("     Tests  96 passed (96)")}`,
  "",
  `${bold("●")} All green. Want me to commit this?`,
  "",
  dim(rule),
  `> `,
  dim(rule),
  dim("  ? for shortcuts"),
];

export const fixture: Fixture = base("terminal", {
  routes: ["/session/s1/terminal"],
  sessions,
  projects: withCounts(PROJECTS, sessions),
  talk: { s1: talk("s1") },
  panes: {
    s1: [
      { name: "lola-s1", kind: "agent", label: "agent" },
      { name: "lola-s1-shell-1", kind: "shell", label: "shell 1", index: 1 },
    ],
  },
  screens: {
    "lola-s1": { cols: 80, rows: 24, lines, cursorX: 2, cursorY: 12 },
    "lola-s1-shell-1": { cols: 80, rows: 24, lines: ["$ "], cursorX: 2, cursorY: 0 },
  },
});
