import { describe, it, expect, afterEach, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { svc } from "./story.testkit";
import { talk } from "$lib/talk.svelte";
import { store } from "$lib/store.svelte";
import { terms, AGENT } from "$lib/terms.svelte";
import FeedNotice from "./FeedNotice.svelte";

const SID = "lola-notice-1";

beforeEach(() => {
  store.sessions = [{ id: SID, agent: "claude", agentState: "idle", tmuxName: SID }] as never;
});
afterEach(cleanup);

describe("FeedNotice", () => {
  it("partial shows a line", () => {
    const { container } = render(FeedNotice, { sessionId: SID, health: "partial" });
    expect(screen.getByText(/Some of this conversation could not be read/)).toBeInTheDocument();
    expect(container.querySelector("[data-feed-notice=partial]")?.className).toContain("text-faint");
    expect(screen.getByRole("button", { name: /Terminal/ })).toBeInTheDocument();
  });

  it("unreadable shows the terminal button", async () => {
    render(FeedNotice, { sessionId: SID, health: "unreadable" });
    expect(screen.getByText(/log isn't readable/)).toBeInTheDocument();
    expect(screen.getByText(/Your agent is fine/)).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: /Terminal/ }));
    expect(terms.activeTab(SID)).toBe(AGENT);
  });

  it("unsupported reads the same, and a healthy log says nothing", () => {
    const { unmount, container } = render(FeedNotice, { sessionId: SID, health: "ok" });
    expect(container.querySelector("[data-feed-notice]")).toBeNull();
    unmount();
    render(FeedNotice, { sessionId: SID, health: "unsupported" });
    expect(screen.getByRole("button", { name: /Terminal/ })).toBeInTheDocument();
  });

  it("a daemon older than the app is named, and the log is not blamed", async () => {
    const id = "lola-notice-old";
    store.sessions = [{ id, agent: "claude", agentState: "idle", tmuxName: id }] as never;
    svc.State.mockRejectedValueOnce(new Error('unknown cmd "talkState"'));
    await talk.refresh(id);
    render(FeedNotice, { sessionId: id, health: "unsupported" });
    expect(screen.getByText(/daemon is older than this app/)).toBeInTheDocument();
    expect(screen.queryByText(/log isn't readable/)).toBeNull();
    expect(screen.getByRole("button", { name: "Restart" })).toBeInTheDocument();
  });
});
