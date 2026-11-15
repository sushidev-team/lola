import { base, ago, session, talk, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// Two review packages and three screenshots on a green, approved PR.
const sessions = [
  session({ id: "s5", issue: "NOR-390", title: "Migrate the reminder emails to the new template", agentState: "idle", delivery: "approved", pr: 345, checks: "pass", review: "approved", minutes: 75 }),
];

const body = `## Summary
Reminder emails use the new template, in light and dark.

## Changes
- **Reminder emails use the new layout**
  New: Customers see the due date in the header.
  Includes:
  - Light and dark colour schemes
  - Long customer names wrap instead of clipping
- **Old template removed**
  New: One template serves every reminder.

## Verification
- npm test passed (212 tests)

## Risks
- **Customers with very long names could see a clipped header**
  Likelihood: low; Impact: medium; Outcome: The name is cut off in some mail clients.
  Assess: Send a preview to a Gmail and an Outlook inbox.
  Basis: Only tested in the in-app preview.

## Agent confidence
Score: 82/100
Basis: Tests pass; real mail clients were not checked.
`;

const screen = (title: string, caption: string) => ({ title, caption, after: "after.png", viewport: "390x844" });

const pkg = (id: string, title: string, screens: ReturnType<typeof screen>[]) => ({
  version: 1,
  id,
  session: "s5",
  repo: "sushidev-team/nori-app",
  pr: 345,
  title,
  body,
  base: "main",
  head: "5c1d9e07b2",
  screens,
  createdAt: ago(30),
  artifacts: [],
});

export const fixture: Fixture = base("review-package", {
  routes: ["/session/s5/review?sheet=reviewPackage"],
  sessions,
  projects: withCounts(PROJECTS, sessions),
  talk: { s5: talk("s5") },
  packages: {
    s5: {
      reviewTracking: true,
      packages: [
        pkg("pkg-1", "Reminder email, light and dark", [screen("Light", "Header and due date"), screen("Dark", "Same email in dark mode")]),
        pkg("pkg-2", "Reminder email, long name", [screen("Long name", "A customer name that wraps")]),
      ],
      reviewed: {},
    },
  },
});
