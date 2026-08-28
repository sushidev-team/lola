import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ChannelTransport } from "./channeltransport";
import { bridge } from "./bridge";
import { ConfigService, DaemonService, DoctorService, LinearService, TermService, UpdateService } from "./desktop";
import { UnsupportedOnMobileError } from "./errors";
import { FRAME_RESP, FakeChannel, type Frame } from "../wire";

let ch: FakeChannel;

beforeEach(async () => {
  ch = new FakeChannel();
  ch.onSend = (f: Frame) => {
    if (f.type === "req") {
      ch.deliver({ v: 1, type: FRAME_RESP, id: f.id, payload: { ok: true, data: { echoed: f.cmd } } });
    }
  };
  const t = new ChannelTransport({ open: async () => ch });
  await t.connect({ host: "127.0.0.1", spkiPin: "pin" });
  bridge.installTransport(t);
});

afterEach(() => bridge.installTransport(null));

/** The req frame a single service call produced. */
function frameOf(): Frame {
  const reqs = ch.sent.filter((f) => f.type === "req");
  expect(reqs).toHaveLength(1);
  return reqs[0];
}

describe("a forwarded service call becomes the right frame", () => {
  it("maps the plain reads onto their commands", async () => {
    await DaemonService.Sessions();
    expect(frameOf()).toMatchObject({ cmd: "sessions", payload: { cmd: "sessions" } });
  });

  it("carries scalar fields on the Request payload, not in an args blob", async () => {
    await DaemonService.Pane("lola-fe-42", 60);
    expect(frameOf().payload).toEqual({ cmd: "pane", session: "lola-fe-42", lines: 60 });
  });

  it("carries the project-centric commands as a typed args payload", async () => {
    // prs/tickets/dev/devFreePort/open*/switchAgent/openURL all take a marshalled
    // args struct on the Go side rather than the Request's own scalar fields.
    await DaemonService.PRs("lola", true);
    expect(frameOf().payload).toEqual({ cmd: "prs", args: { project: "lola", refresh: true } });
  });

  it("maps dev on/off through DevArgs", async () => {
    await DaemonService.Dev("lola-fe-42", true);
    expect(frameOf().payload).toEqual({ cmd: "dev", args: { session: "lola-fe-42", on: true } });
  });

  it("maps openURL through OpenURLArgs, keeping the daemon's http(s) guard", async () => {
    await DaemonService.OpenURL("http://127.0.0.1:8000");
    expect(frameOf().payload).toEqual({ cmd: "openURL", args: { url: "http://127.0.0.1:8000" } });
  });

  it("sends answer with the reply text on the Request", async () => {
    await DaemonService.Answer("lola-fe-42", "yes");
    expect(frameOf().payload).toEqual({ cmd: "answer", session: "lola-fe-42", text: "yes" });
  });

  it("maps TermService.Capture onto cmd=pane and returns its text", async () => {
    ch.onSend = (f: Frame) => {
      if (f.type === "req") {
        ch.deliver({ v: 1, type: FRAME_RESP, id: f.id, payload: { ok: true, data: { text: "hi", hasQuestion: false } } });
      }
    };
    await expect(TermService.Capture("lola-fe-42", 40)).resolves.toBe("hi");
    expect(frameOf()).toMatchObject({ cmd: "pane" });
  });

  it("reports the daemon reachable from the connection's own state", async () => {
    await expect(DaemonService.Alive()).resolves.toBe(true);
    bridge.installTransport(null);
    await expect(DaemonService.Alive()).resolves.toBe(false);
  });
});

describe("an unsupported call rejects with a NAMED error", () => {
  // The rule this file exists to enforce: a platform method must reject, never
  // resolve. store.act() flashes a rejection, whereas a silent resolve would
  // report "daemon started" having started nothing, and a missing export would
  // surface as `undefined` — which reads as an empty answer, not an error.
  const platform: [string, () => Promise<unknown>][] = [
    ["DaemonService.StartDaemon", () => DaemonService.StartDaemon()],
    ["DaemonService.StopDaemon", () => DaemonService.StopDaemon()],
    ["DaemonService.RestartDaemon", () => DaemonService.RestartDaemon()],
    ["DaemonService.CLIInfo", () => DaemonService.CLIInfo()],
    ["DaemonService.InstallCLI", () => DaemonService.InstallCLI()],
    ["DaemonService.Reload", () => DaemonService.Reload()],
    ["DaemonService.RenameProject", () => DaemonService.RenameProject("a", "b")],
    ["ConfigService.PickFolder", () => ConfigService.PickFolder("/")],
    ["ConfigService.GetSettings", () => ConfigService.GetSettings()],
    ["ConfigService.SetLinearKey", () => ConfigService.SetLinearKey("k")],
    ["ConfigService.SetProjectLayout", () => ConfigService.SetProjectLayout({} as never)],
    ["TermService.Shells", () => TermService.Shells("lola-fe-42")],
    ["TermService.Shell", () => TermService.Shell("lola-fe-42-shell-1", "/tmp")],
    ["TermService.CloseShell", () => TermService.CloseShell("lola-fe-42-shell-1")],
    ["LinearService.Teams", () => LinearService.Teams()],
    ["DoctorService.Run", () => DoctorService.Run()],
    ["UpdateService.CheckForUpdates", () => UpdateService.CheckForUpdates(true)],
  ];

  for (const [name, call] of platform) {
    it(`${name} rejects rather than resolving`, async () => {
      const err = await call().then(
        () => null,
        (e: unknown) => e,
      );
      expect(err).toBeInstanceOf(UnsupportedOnMobileError);
      expect((err as UnsupportedOnMobileError).method).toContain(name.split("(")[0].split(".")[1]);
      // The message must say WHY, not just that it failed.
      expect((err as UnsupportedOnMobileError).reason.length).toBeGreaterThan(20);
      expect(String(err)).toContain("not available on mobile");
    });

    it(`${name} puts nothing on the wire`, async () => {
      await call().catch(() => {});
      expect(ch.sent.filter((f) => f.type === "req")).toHaveLength(0);
    });
  }

  it("omits GetTheme and SetTheme entirely, because theme-runtime probes for them", () => {
    // theme-runtime.svelte.ts does `if (typeof svc.GetTheme !== "function")
    // return undefined` and degrades to the localStorage cache. Leaving these
    // ABSENT is therefore a supported state the shared code already handles —
    // and a stub returning a fake flavor would be worse than no answer.
    expect((ConfigService as Record<string, unknown>).GetTheme).toBeUndefined();
    expect((ConfigService as Record<string, unknown>).SetTheme).toBeUndefined();
  });

  it("answers ConfigExists optimistically so a phone is never sent to the setup wizard", async () => {
    await expect(ConfigService.ConfigExists()).resolves.toBe(true);
  });

  it("refuses a FORCED kill locally, because the daemon clears force on every remote request", async () => {
    // Sending it anyway would produce the dirty-worktree refusal again, and
    // store.kill matches that refusal in order to re-ask — so the user would
    // confirm "remove it anyway", get the same question back, and loop.
    await expect(DaemonService.Kill("lola-fe-42", true)).rejects.toThrow(/forced kill is refused/);
    expect(ch.sent.filter((f) => f.type === "req")).toHaveLength(0);
    await DaemonService.Kill("lola-fe-42", false);
    expect(frameOf().payload).toEqual({ cmd: "kill", session: "lola-fe-42" });
  });
});

describe("with no transport installed", () => {
  it("rejects a forwarded call with ShimNotConnectedError, naming the method", async () => {
    bridge.installTransport(null);
    await expect(DaemonService.Sessions()).rejects.toMatchObject({
      name: "ShimNotConnectedError",
      method: "DaemonService.Sessions",
    });
  });

  it("still lets a write to a detached pane be a no-op rather than an error", async () => {
    bridge.installTransport(null);
    await expect(TermService.Write("lola-fe-42", "x")).resolves.toBeUndefined();
  });
});
