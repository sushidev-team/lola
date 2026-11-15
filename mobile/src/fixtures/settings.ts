import { ago, base, session, withCounts, PROJECTS, STATUS } from "./build";
import type { Fixture } from "./types";

// Connected over the relay with two tiers missing: Settings, the connection
// sheet and a session menu with its disabled rows.
const sessions = [
  session({ id: "s1", issue: "NOR-414", title: "FinanzOnline: Account balance and due dates with reminders", agentState: "idle", minutes: 40 }),
];

export const fixture: Fixture = {
  ...base("settings", { sessions, projects: withCounts(PROJECTS, sessions) }),
  routes: ["/settings?sheet=connection", "/session/s1/conversation?sheet=sessionMenu"],
  // Today's tokens against a budget and a subscription window, for the usage glance.
  status: {
    ...STATUS,
    usage: {
      day: "2026-11-15",
      tokens: 46_700_000,
      weighted: 38,
      todayUsd: 0,
      budgetTokens: 100,
      quotas: [
        {
          agent: "claude",
          at: ago(1),
          windows: [
            { label: "5h", usedPercent: 42, resetsAt: ago(-130) },
            { label: "7d", usedPercent: 18, resetsAt: ago(-60 * 24 * 4) },
          ],
        },
      ],
    },
  },
  tiers: ["read", "answer", "control"],
  path: { kind: "relay", interface: "cellular" },
};
