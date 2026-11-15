import { base, session, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// Connected over the relay with two tiers missing: Settings, the connection
// sheet and a session menu with its disabled rows.
const sessions = [
  session({ id: "s1", issue: "NOR-414", title: "FinanzOnline: Account balance and due dates with reminders", agentState: "idle", minutes: 40 }),
];

export const fixture: Fixture = {
  ...base("settings", { sessions, projects: withCounts(PROJECTS, sessions) }),
  routes: ["/settings?sheet=connection", "/session/s1/conversation?sheet=sessionMenu"],
  tiers: ["read", "answer", "control"],
  path: { kind: "relay", interface: "cellular" },
};
