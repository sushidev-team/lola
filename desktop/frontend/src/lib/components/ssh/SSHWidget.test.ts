import { describe, it, expect, afterEach, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/svelte";

const sshSvc = vi.hoisted(() => new Proxy({} as Record<string, ReturnType<typeof vi.fn>>, {
  get(t, k: string) {
    return (t[k] ??= vi.fn().mockResolvedValue({}));
  },
}));
vi.mock("@bindings/desktop", () => ({ SSHService: sshSvc, TalkService: {}, DaemonService: {}, ConfigService: {}, TermService: {} }));

const { default: SSHWidget } = await import("./SSHWidget.svelte");
const { setWidgetsUnavailable } = await import("$lib/widgetsoff");

afterEach(() => {
  cleanup();
  setWidgetsUnavailable(false);
});

const element = (html?: string) => ({ id: "w1", kind: "widget", title: "Queue depth", body: "", revision: 1, html }) as never;

describe("SSHWidget", () => {
  it("mounts no frame and calls no service on a client that is never sent pages", () => {
    setWidgetsUnavailable(true);
    vi.useFakeTimers();
    const { container } = render(SSHWidget, { sessionId: "s", element: element(""), agent: "claude" });
    vi.advanceTimersByTime(60_000);
    vi.useRealTimers();
    expect(container.querySelector("iframe")).toBeNull();
    expect(container.querySelector("[data-widget-fault]")).toBeNull();
    expect(screen.getByText("Open on your Mac to view.")).toHaveAttribute("data-widget-unavailable");
    for (const k of Object.keys(sshSvc)) expect(sshSvc[k]).not.toHaveBeenCalled();
  });

  it("frames a widget with an empty page on the Mac, as before", () => {
    const { container } = render(SSHWidget, { sessionId: "s", element: element(""), agent: "claude" });
    expect(container.querySelector("iframe")).not.toBeNull();
    expect(container.querySelector("[data-widget-unavailable]")).toBeNull();
  });

  it("still frames a widget that has its page", () => {
    const { container } = render(SSHWidget, { sessionId: "s", element: element("<b>x</b>"), agent: "claude" });
    expect(container.querySelector("iframe")).not.toBeNull();
    expect(container.querySelector("[data-widget-unavailable]")).toBeNull();
  });
});
