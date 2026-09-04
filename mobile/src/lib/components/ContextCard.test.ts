import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/svelte";
import { inputReasonLabel } from "$lib/theme";
import ContextCard from "./ContextCard.svelte";
import type { SessionInfo } from "$lib/store.svelte";

// What the card DECIDES, which is: whether to exist at all, which facts earn a
// chip, and that the agent's untrusted prose reaches the DOM as text.
//
// The most valuable assertions here are the NEGATIVE ones, and they are the ones
// a screenshot can never make. This card sits directly above the terminal — the
// subject of the whole screen — so a version that renders an empty bordered box
// for the commonest session in the app costs forty points of the one thing the
// screen is for, and it looks perfectly fine in a mock built from a session that
// has something to say.

function s(over: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id: "nori-app-nor-414",
    project: "nori-app",
    issue: "NOR-414",
    title: "FinanzOnline: Account balance and due dates",
    status: "working",
    agentState: "working",
    delivery: "",
    interpretedState: "",
    headline: "",
    headlineAgo: "",
    lastNotification: "",
    inputReason: "",
    checks: "",
    age: "40m",
    prNumber: 0,
    reacting: "",
    devActive: false,
    ...over,
  } as unknown as SessionInfo;
}

function mount(over: Partial<SessionInfo> | undefined = {}) {
  return render(ContextCard, { props: { session: over === undefined ? undefined : s(over) } });
}

describe("ContextCard", () => {
  it("renders nothing at all for a session with nothing to say", () => {
    // Not an empty card, not a placeholder. This is the commonest state in the
    // app — no PR, no interpreter judgement, no notification — and a bordered
    // box holding none of those is worse than the space it takes.
    const { container } = mount();
    expect(container.textContent!.trim()).toBe("");
    expect(container.querySelector(".bg-panel")).toBeNull();
  });

  it("renders nothing while the list has not caught up with the pane", () => {
    // The screen can be attached to a pane whose session record has not arrived
    // (a development link opens one directly). Undefined is the same empty case
    // as a session with no facts, and must not be a crash or a skeleton.
    const { container } = mount(undefined);
    expect(container.textContent!.trim()).toBe("");
  });

  it("draws the agent's own sentence, as text", () => {
    // UNTRUSTED: it is derived from pane text, which an issue description or a
    // dependency's README can write into. It reaches the DOM as a text node and
    // nowhere else — no HTML, nothing a link is built from (brief rule 6).
    const hostile = '<img src=x onerror="alert(1)"> waiting on a go-ahead';
    const { container } = mount({ lastNotification: hostile });
    expect(screen.getByText(hostile)).toBeTruthy();
    expect(container.querySelector("img")).toBeNull();
  });

  it("prefers the interpreter's headline over the agent's notification", () => {
    // The same source and the same precedence the list uses. A detail screen
    // that paraphrases the row the user just tapped is one people learn to
    // distrust.
    mount({ headline: "Removing the chart", lastNotification: "Awaiting confirmation" });
    expect(screen.getByText(/Removing the chart/)).toBeTruthy();
    expect(screen.queryByText(/Awaiting confirmation/)).toBeNull();
  });

  it("stamps only the interpreter's sentence, never the session's own age", () => {
    // `headlineAgo` is the freshness of the judgement. `age` is how long the
    // session has existed, and printing it in this slot would read as "this is
    // what the agent said 40 minutes ago" about a session that has merely been
    // running that long.
    const withHeadline = mount({ headline: "Removing the chart", headlineAgo: "2m" });
    expect(withHeadline.container.textContent).toContain("2m ago");
    withHeadline.unmount();

    const notificationOnly = mount({ lastNotification: "Awaiting confirmation", age: "40m" });
    expect(notificationOnly.container.textContent).not.toContain("40m");
    expect(notificationOnly.container.textContent).not.toContain("ago");
  });

  it("turns a checks rollup into one chip, and says nothing when there is none", () => {
    for (const [checks, text] of [
      ["pass", "CI pass"],
      ["fail", "CI fail"],
      ["pending", "CI running"],
    ] as const) {
      const { unmount } = mount({ checks, prNumber: 352 });
      expect(screen.getByText(new RegExp(text))).toBeTruthy();
      unmount();
    }
    // A PR with no checks configured is not a fact about this session, and "no
    // checks" reads as the app having failed to find them.
    const { container } = mount({ checks: "none", prNumber: 352 });
    expect(container.textContent).not.toContain("CI");
  });

  it("names WHY the agent is blocked, using the shared vocabulary", () => {
    // The one fact that turns a status into an instruction: "needs you" says a
    // person is required, this says what pressing into the pane will ask.
    // Every reason the shared table names, so a fixture cannot quietly test a
    // key the daemon never sends. (An earlier draft asked for "permission"; the
    // real key is "permission_prompt", and the guard below is what caught it.)
    for (const reason of ["question", "permission_prompt", "dialog", "quota_limited"]) {
      const label = inputReasonLabel(reason);
      expect(label).not.toBe("");
      const { unmount } = mount({ agentState: "waiting_input", inputReason: reason });
      expect(screen.getByText(label)).toBeTruthy();
      unmount();
    }
  });

  it("says nothing for an input reason the shared table does not name", () => {
    // `inputReasonLabel` answers "" outside the answerable reasons, including
    // the historical `idle_notification` a pre-split snapshot can still carry —
    // so such a record shows no chip rather than an explanation that stopped
    // being true.
    const { container } = mount({ agentState: "waiting_input", inputReason: "idle_notification" });
    expect(container.textContent!.trim()).toBe("");
  });

  it("marks the session holding its project's dev servers", () => {
    // Only one session per project may run them, so this is a fact about a
    // shared resource — and the toggle behind the overflow button is otherwise
    // the only thing on the screen that says which state it is in.
    mount({ devActive: true });
    expect(screen.getByText(/dev running/)).toBeTruthy();
  });

  it("makes none of its facts a control", () => {
    // The corresponding actions live behind the header's overflow button, where
    // they can be labelled and confirmed. A chip that is sometimes tappable is a
    // worse affordance than one that never is.
    const { container } = mount({
      checks: "pass",
      prNumber: 352,
      devActive: true,
      inputReason: "permission_prompt",
      agentState: "waiting_input",
    });
    expect(container.querySelectorAll("button")).toHaveLength(0);
    expect(container.querySelectorAll("a")).toHaveLength(0);
  });
});
