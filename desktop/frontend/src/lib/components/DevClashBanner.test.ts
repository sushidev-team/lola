import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import DevClashBanner from "./DevClashBanner.svelte";
import { store } from "$lib/store.svelte";
import { confirm } from "$lib/confirm.svelte";
import type { SessionInfo } from "@bindings/internal/protocol";

// A dev tab that loses a port race dies instantly and usually clears its own
// screen on the way out, so the terminal says nothing at all. The banner is the
// only place the reason — and the process responsible — is ever stated.
describe("DevClashBanner", () => {
  const session = (devClash?: SessionInfo["devClash"]) =>
    ({ id: "lola-app-eng-1", issue: "ENG-1", devCommands: ["cd desktop && wails3 dev"], devClash }) as SessionInfo;

  beforeEach(() => {
    cleanup();
    confirm.cancel();
    store.devPending = {};
  });

  it("renders nothing while the dev tabs are healthy", () => {
    render(DevClashBanner, { session: session() });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("names the port, the process and where it runs", () => {
    render(DevClashBanner, {
      session: session({
        tab: "lola-app-eng-1-dev-1",
        command: "cd desktop && wails3 dev",
        port: 9245,
        pid: 52791,
        proc: "node",
        dir: "/Users/someone/code/app/desktop/frontend",
        ours: false,
      }),
    });
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("9245");
    expect(alert).toHaveTextContent("node");
    expect(alert).toHaveTextContent("52791");
    expect(alert).toHaveTextContent("/Users/someone/code/app/desktop/frontend");
    expect(alert).toHaveTextContent("cd desktop && wails3 dev");
  });

  // Killing a process lola did not start is never one click: the button opens
  // the shared confirm dialog, the same one the kill shortcut uses.
  it("asks before killing anything", async () => {
    // The dialog is built from the STORE's copy of the session, not from the
    // rendered prop: what it offers to kill must be what the daemon last
    // reported, since the daemon refuses a request that no longer matches.
    store.sessions = [
      session({ tab: "lola-app-eng-1-dev-1", port: 9245, pid: 52791, proc: "node", dir: "/elsewhere" }),
    ];
    render(DevClashBanner, { session: store.sessions[0] });
    await fireEvent.click(screen.getByRole("button", { name: /free port 9245/i }));
    expect(confirm.request).not.toBeNull();
    expect(confirm.request?.title).toMatch(/9245/);
    // The dialog spells out that this process is not lola's.
    expect(confirm.request?.detail).toMatch(/not started by lola/i);
  });

  // lola's own leftover server is a different sentence: reclaiming it is routine,
  // and saying "anything unsaved is lost" about a dev server nobody owns is noise.
  it("words the question differently for lola's own leftover server", async () => {
    store.sessions = [
      session({
        tab: "lola-app-eng-1-dev-1",
        command: "composer dev",
        port: 8000,
        pid: 77,
        proc: "php",
        dir: "/home/.lola/worktrees/app/lola-app-eng-2",
        ours: true,
      }),
    ];
    render(DevClashBanner, { session: store.sessions[0] });
    await fireEvent.click(screen.getByRole("button", { name: /free port 8000/i }));
    expect(confirm.request?.detail).toMatch(/leftover/i);
  });
});
