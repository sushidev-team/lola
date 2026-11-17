import { base, longStory, session, talk, withCounts, PROJECTS } from "./build";
import type { Fixture } from "./types";

// The composer holds a draft with one pasted block and one image; the keyboard
// is up (`kb=336`) and the attach sheet is open (route `?sheet=attach`). Without the sheet the
// field holds a six-line draft with two queued messages above it, for the keyboard-up layout:
// `preview.html?route=/session/s3/conversation&fixture=composer&kb=336`.
const sessions = [
  session({ id: "s3", issue: "NOR-402", title: "Extract the invoice presenter so the PDF and the mail share one shape", agentState: "working", minutes: 190 }),
];
const items = longStory(40);

const PASTED = Array.from({ length: 12 }, (_, i) => `  at presenter.render (src/invoice/presenter.ts:${40 + i}:11)`).join("\n");

export const fixture: Fixture = base("composer", {
  routes: ["/session/s3/conversation?sheet=attach"],
  sessions,
  projects: withCounts(PROJECTS, sessions),
  talk: { s3: talk("s3", { feedSeq: items.length }) },
  feeds: { s3: items },
  async seed() {
    const { ssh } = await import("$lib/ssh.svelte");
    const { attachmentToken } = await import("$lib/composerpaste");
    const view = ssh.view("s3");
    view.pastes.set(1, { kind: "text", value: PASTED });
    // A pasted block, a ready image, one still preparing and one that failed: every tile state.
    view.pastes.set(2, { kind: "image", value: "/tmp/lola/attachments/period-view.png", thumb: "data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='56' height='56'><rect width='56' height='56' fill='%23556'/></svg>" });
    view.pastes.set(3, { kind: "image", value: "", loading: true });
    view.pastes.set(4, { kind: "image", value: "", error: "That image is too large." });
    if (!new URLSearchParams(globalThis.location?.search ?? "").get("route")?.includes("sheet=attach")) {
      view.pastes.clear();
      const at = new Date().toISOString();
      view.items = [
        ...view.items,
        { id: "q1", kind: "human", order: 9001, seq: 9001, at, human: { origin: "say", text: "Also keep the mail template in sync.", delivered: false } },
        { id: "q2", kind: "human", order: 9002, seq: 9002, at, human: { origin: "say", text: "And add a test for the empty invoice.", delivered: false } },
      ];
      view.agentDraft = ["Please do this in order:", "1. extract the presenter", "2. move the PDF layout onto it", "3. move the mail layout onto it", "4. delete the old helpers", "5. run the whole suite"].join("\n");
      return;
    }
    view.agentDraft = `The render fails with ${attachmentToken(1)} and the layout looks like ${attachmentToken(2)} and ${attachmentToken(3)} and ${attachmentToken(4)}. Can you fix both?`;
  },
});
