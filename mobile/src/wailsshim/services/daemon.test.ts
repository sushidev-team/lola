import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../bridge", () => ({ bridge: { request: vi.fn(), alive: vi.fn(() => true) } }));
vi.mock("../../lib/openurl", async () => {
  const actual = await vi.importActual<typeof import("../../lib/openurl")>("../../lib/openurl");
  return { ...actual, openOnPhone: vi.fn(async () => {}) };
});

import { OpenOnPhoneError, openOnPhone } from "../../lib/openurl";
import { bridge } from "../bridge";
import { UnsupportedOnMobileError } from "../errors";
import * as Daemon from "./daemon";
import { MAC_LINK } from "./reasons";
import { describeRoutes } from "./testkit";

const request = vi.mocked(bridge.request);
/** The one request a call made: [method, cmd, fields]. */
const sent = () => {
  expect(request).toHaveBeenCalledTimes(1);
  return request.mock.calls[0];
};

beforeEach(() => {
  request.mockReset();
  request.mockResolvedValue({});
  vi.mocked(openOnPhone).mockReset();
});

describeRoutes("DaemonService", Daemon, Daemon.ROUTES, {
  extras: ["Panes", "ShellCreate", "PaneClose", "PaneResize"],
  skipSend: ["Panes"],
});

describe("field placement", () => {
  it("carries Go's flat fields flat", async () => {
    await Daemon.Pane("s1", 60);
    expect(sent().slice(1)).toEqual(["pane", { session: "s1", lines: 60 }]);
  });

  it("carries project-centric commands in args", async () => {
    await Daemon.PRs("lola", true);
    expect(sent().slice(1)).toEqual(["prs", { args: { project: "lola", refresh: true } }]);
  });

  it("nests the number of a PR command under args beside the flat session", async () => {
    await Daemon.MergePR("s1", 7, "o/r", "abc", "squash", false);
    expect(sent().slice(1)).toEqual([
      "mergePR",
      { session: "s1", args: { number: 7, repo: "o/r", head: "abc", method: "squash", confirmUnready: false } },
    ]);
  });

  it("maps renameSession's id to the flat session and the name to text", async () => {
    await Daemon.RenameSession("s1", "New name");
    expect(sent().slice(1)).toEqual(["renameSession", { session: "s1", text: "New name" }]);
  });

  it("sends a review package diff with from/to under args", async () => {
    await Daemon.ReviewPackageDiff("s1", "a", "b");
    expect(sent().slice(1)).toEqual(["reviewPackageDiff", { session: "s1", args: { from: "a", to: "b" } }]);
  });

  it("uses openExisting only for a reused branch", async () => {
    await Daemon.OpenManual({ existing: true } as never);
    expect(sent()[1]).toBe("openExisting");
    request.mockClear();
    await Daemon.OpenManual({} as never);
    expect(sent()[1]).toBe("openManual");
  });

  it("is alive exactly when the connection is", async () => {
    await expect(Daemon.Alive()).resolves.toBe(true);
    vi.mocked(bridge.alive).mockReturnValueOnce(false);
    await expect(Daemon.Alive()).resolves.toBe(false);
  });
});

describe("Kill", () => {
  it("takes three arguments", () => {
    expect(Daemon.Kill.length).toBe(3);
  });

  it("sends a plain kill", async () => {
    await Daemon.Kill("s1", false, false);
    expect(sent().slice(1)).toEqual(["kill", { session: "s1" }]);
  });

  it("refuses force locally, because the daemon clears it", async () => {
    await expect(Daemon.Kill("s1", true, false)).rejects.toThrow(/forced kill is refused/);
    expect(request).not.toHaveBeenCalled();
  });

  it("forwards keepWorktree", async () => {
    await Daemon.Kill("s1", false, true);
    expect(sent().slice(1)).toEqual(["kill", { session: "s1", keepWorktree: true }]);
  });
});

describe("OpenURL", () => {
  it("opens on the phone and never sends a request", async () => {
    await Daemon.OpenURL("https://example.com");
    expect(openOnPhone).toHaveBeenCalledWith("https://example.com");
    expect(request).not.toHaveBeenCalled();
  });

  it("tells a loopback link to be opened on the Mac", async () => {
    vi.mocked(openOnPhone).mockRejectedValueOnce(new OpenOnPhoneError("loopback"));
    const err = await Daemon.OpenURL("http://localhost:3000").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(UnsupportedOnMobileError);
    expect((err as UnsupportedOnMobileError).reason).toBe(MAC_LINK);
  });

  it("passes any other refusal through", async () => {
    vi.mocked(openOnPhone).mockRejectedValueOnce(new OpenOnPhoneError("not_http"));
    await expect(Daemon.OpenURL("ftp://x")).rejects.toBeInstanceOf(OpenOnPhoneError);
  });
});

describe("mobile extras", () => {
  it("lists panes normalized for the session", async () => {
    request.mockResolvedValueOnce({ panes: null });
    const d = await Daemon.Panes("s1");
    expect(sent().slice(1)).toEqual(["panes", { session: "s1" }]);
    expect(d.panes).toEqual([]);
  });

  it("sends the phone's size to shellCreate only when it is measured", async () => {
    await Daemon.ShellCreate("s1", 80, 24);
    expect(sent()[2]).toEqual({ session: "s1", cols: 80, rows: 24 });
    request.mockClear();
    await Daemon.ShellCreate("s1");
    expect(sent()[2]).toEqual({ session: "s1" });
  });

  it("nests paneClose and paneResize under args", async () => {
    await Daemon.PaneClose("s1", "p");
    expect(sent().slice(1)).toEqual(["paneClose", { args: { session: "s1", pane: "p" } }]);
    request.mockClear();
    await Daemon.PaneResize("s1", "p", 80, 24);
    expect(sent().slice(1)).toEqual(["paneResize", { args: { session: "s1", pane: "p", cols: 80, rows: 24 } }]);
  });
});
