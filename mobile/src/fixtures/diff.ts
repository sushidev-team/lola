import { ago, base, diff, session, talk, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// Forty files and two thousand patch lines, with three comments not yet sent.
// It is also the first of two best-of attempts and has three checkpoints, so the
// bestOf and checkpoints sheets show content.
const sessions = [
  { ...session({ id: "s4", issue: "NOR-388", title: "Split the settings form into tabs", agentState: "idle", delivery: "review_pending", pr: 341, minutes: 192 }), bestOf: { group: "g-nor-388", attempt: 1, of: 2 } },
];
const data = diff("s4", 40, 2000);
const first = data.files?.[0]?.path ?? "";
const second = data.files?.[3]?.path ?? "";

const draft = (key: string, path: string, line: number, body: string) => ({
  key,
  path,
  side: "new",
  line,
  endLine: line,
  quote: "const total = balance(period);",
  body,
});

export const fixture: Fixture = base("diff", {
  routes: ["/session/s4/changes", "/session/s4/review?sheet=feedback", "/session/s4/review", "/session/s4/review?sheet=checkpoints", "/session/s4/review?sheet=bestOf"],
  sessions,
  projects: withCounts(PROJECTS, sessions),
  talk: { s4: talk("s4") },
  diffs: { s4: data },
  checkpoints: {
    s4: {
      session: "s4",
      restorable: true,
      checkpoints: [
        { seq: 1, sha: "1a2b3c4d", head: "9f3c2ab", label: "Split the form into tabs", created: ago(180) },
        { seq: 2, sha: "2b3c4d5e", head: "9f3c2ab", label: "Move validation per tab", created: ago(120) },
        { seq: 3, sha: "3c4d5e6f", head: "9f3c2ab", label: "Fix tab focus order", created: ago(40) },
      ],
    },
  },
  storage: {
    "lola.diff.drafts": JSON.stringify({
      s4: {
        drafts: [
          draft("d1", first, 4, "This should read from the period, not the clock."),
          draft("d2", first, 9, "Name this after what it returns."),
          draft("d3", second, 6, "Add a test for the empty month."),
        ],
        note: "",
      },
    }),
  },
});
