import { base, longStory, session, talk, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// Three hundred conversation items: forty tool rows or more, one subagent run.
const sessions = [
  {
    ...session({ id: "s3", issue: "NOR-402", title: "Extract the invoice presenter so the PDF and the mail share one shape", agentState: "working", minutes: 190 }),
    devActive: true,
    devUrls: ["http://localhost:5173/", "http://localhost:8787/api/health"],
    devForwards: [
      { url: "http://192.168.20.3:5173/", from: "127.0.0.1:5173", freed: false },
      { url: "http://192.168.20.3:8787/api/health", from: "127.0.0.1:8787", freed: false },
    ],
  },
];
const items = longStory(300);

export const fixture: Fixture = base("long-story", {
  routes: ["/session/s3/conversation"],
  sessions,
  projects: withCounts(PROJECTS, sessions),
  talk: { s3: talk("s3", { feedSeq: items.length }) },
  feeds: { s3: items },
  // Two notes typed while the agent works: held until it is at its prompt.
  timelines: {
    s3: {
      connections: [],
      items: [
        { id: "q1", kind: "human", order: 1, seq: 1, human: { text: "Also keep the mail footer unchanged.", delivered: false } },
        { id: "q2", kind: "human", order: 2, seq: 2, human: { text: "Run the PDF snapshot tests when you are done.", delivered: false } },
      ] as never,
    },
  },
  checkpoints: {
    s3: {
      session: "s3",
      restorable: true,
      checkpoints: [
        { seq: 3, sha: "a1b2c3d", head: "9f8e7d6", label: "Before: tighten the accounting period view", created: new Date(Date.now() - 12 * 60000).toISOString() },
        { seq: 2, sha: "b2c3d4e", head: "9f8e7d6", label: "Before: split the presenter", created: new Date(Date.now() - 55 * 60000).toISOString() },
        { seq: 1, sha: "c3d4e5f", head: "8e7d6c5", label: "Session start", created: new Date(Date.now() - 190 * 60000).toISOString() },
      ],
    },
  },
});
