import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/svelte";
import { store } from "$lib/store.svelte";
import { preview } from "$lib/preview.svelte";
import PreviewWorkspace from "./PreviewWorkspace.svelte";
import TestLogins from "./TestLogins.svelte";
import { PreviewService } from "@bindings/desktop";

vi.mock("@bindings/desktop", async (original) => ({ ...await original<typeof import("@bindings/desktop")>(), PreviewService: {
  Open: vi.fn(), SetAutoLogin: vi.fn(), AutoTestLogin: vi.fn(), UseTestLogin: vi.fn(), TestLogins: vi.fn(), SaveTestLogin: vi.fn(), RemoveTestLogin: vi.fn(),
} }));
const noAuto = { name: "", origin: "" };
afterEach(() => { cleanup(); preview.close(); store.dismissFlash(); vi.useRealTimers(); vi.resetAllMocks(); });

it("saves named accounts, signs in on demand and keeps existing passwords on edit", async () => {
  const session = "s", url = "http://localhost:3000";
  const account = { name: "Editor", username: "editor@test.local", agent: true };
  vi.mocked(PreviewService.TestLogins).mockResolvedValueOnce({ project: "web", autoLogin: noAuto, profiles: [] })
    .mockResolvedValue({ project: "web", autoLogin: noAuto, profiles: [account] });
  const signIn = vi.fn(), close = vi.fn();
  render(TestLogins, { session, url, signIn, close });
  await fireEvent.click(await screen.findByRole("button", { name: "Add login" }));
  await fireEvent.input(screen.getByLabelText("Login name"), { target: { value: account.name } });
  await fireEvent.input(screen.getByLabelText("Username or email"), { target: { value: account.username } });
  const password = screen.getByLabelText("Password");
  expect(password).toHaveAttribute("type", "password");
  await fireEvent.input(password, { target: { value: "fixture-password" } });
  const sharing = screen.getByRole("checkbox");
  expect(sharing).not.toBeChecked();
  await fireEvent.click(sharing);
  await fireEvent.click(screen.getByRole("button", { name: "Save login" }));
  expect(PreviewService.SaveTestLogin).toHaveBeenCalledWith(session, url, "web", account, "fixture-password", false);
  await fireEvent.click(await screen.findByRole("button", { name: "Edit Editor" }));
  expect(screen.getByLabelText("Password")).toHaveValue("");
  await fireEvent.click(screen.getByRole("button", { name: "Save login" }));
  expect(PreviewService.SaveTestLogin).toHaveBeenLastCalledWith(session, url, "web", account, "", true);
  expect(signIn).not.toHaveBeenCalled();
  await fireEvent.click(await screen.findByRole("button", { name: /Editor editor@test.local/ }));
  await waitFor(() => expect(signIn).toHaveBeenCalledWith("web", "Editor", expect.any(AbortSignal)));
  expect(close).toHaveBeenCalled();
});

it("delivers only the chosen account to the active local document", async () => {
  const origin = "http://p-example.localhost:5555", url = "http://localhost:3000";
  const profile = { name: "Buyer", username: "buyer", agent: false };
  vi.mocked(PreviewService.Open).mockResolvedValue(origin + "/login");
  vi.mocked(PreviewService.TestLogins).mockResolvedValue({ project: "shop", autoLogin: noAuto, profiles: [profile] });
  render(PreviewWorkspace);
  preview.open("s", url);
  const frame = await screen.findByTitle("Preview of localhost:3000") as HTMLIFrameElement;
  const post = vi.spyOn(frame.contentWindow!, "postMessage");
  try {
    await fireEvent(window, new MessageEvent("message", { source: frame.contentWindow, origin,
      data: { source: "lola-preview", type: "ready", documentId: "doc-a", url: origin + "/login" },
    }));
    const open = async () => {
      await fireEvent.click(screen.getByRole("button", { name: "Test logins" }));
      return within(await screen.findByRole("dialog", { name: "Test logins" })).findByRole("button", { name: /Buyer buyer/ });
    };
    let finish!: (value: { username: string; password: string }) => void;
    vi.mocked(PreviewService.UseTestLogin).mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }) as ReturnType<typeof PreviewService.UseTestLogin>);
    const buyer = await open();
    expect(PreviewService.UseTestLogin).not.toHaveBeenCalled();
    await fireEvent.click(buyer);
    expect(PreviewService.UseTestLogin).toHaveBeenCalledWith("s", url, "shop", "Buyer", origin);
    await fireEvent.click(screen.getByRole("button", { name: "Close test logins" }));
    const credentials = { username: "buyer", password: "fixture-password" };
    finish(credentials);
    await Promise.resolve();
    expect(post).not.toHaveBeenCalledWith(expect.objectContaining({ type: "test-login" }), expect.anything());
    vi.mocked(PreviewService.UseTestLogin).mockResolvedValue(credentials);
    await fireEvent.click(await open());
    expect(post).toHaveBeenCalledWith({ target: "lola-preview", type: "test-login", documentId: "doc-a", ...credentials }, origin);
    vi.useFakeTimers();
    const reply = () => fireEvent(window, new MessageEvent("message", { source: frame.contentWindow, origin,
      data: { source: "lola-preview", type: "login-result", documentId: "doc-a", result: "submitted" },
    }));
    await reply();
    expect(store.flash?.text).toBe("Sign-in submitted.");
    vi.advanceTimersByTime(3000);
    await reply(); // A duplicate receipt must not keep the toast alive.
    vi.advanceTimersByTime(1000);
    expect(store.flash).toBeNull();
  } finally { post.mockRestore(); }
});

it("switches the single auto-login checkbox and allows clearing it", async () => {
  const url = "http://localhost:3000";
  const profiles = ["Buyer", "Admin"].map(name => ({ name, username: name, agent: false }));
  vi.mocked(PreviewService.TestLogins)
    .mockResolvedValueOnce({ project: "shop", autoLogin: { name: "Buyer", origin: "http://localhost" }, profiles })
    .mockResolvedValueOnce({ project: "shop", autoLogin: { name: "Admin", origin: "http://localhost" }, profiles })
    .mockResolvedValueOnce({ project: "shop", autoLogin: noAuto, profiles });
  render(TestLogins, { session: "s", url, signIn: vi.fn(), close: vi.fn() });
  const buyer = await screen.findByRole("checkbox", { name: "Auto-login as Buyer" });
  const admin = screen.getByRole("checkbox", { name: "Auto-login as Admin" });
  expect(buyer).toBeChecked();
  await fireEvent.click(admin);
  expect(PreviewService.SetAutoLogin).toHaveBeenLastCalledWith("s", url, "shop", "Admin", true);
  await waitFor(() => expect(admin).toBeChecked());
  expect(buyer).not.toBeChecked();
  await fireEvent.click(admin);
  expect(PreviewService.SetAutoLogin).toHaveBeenLastCalledWith("s", url, "shop", "Admin", false);
  await waitFor(() => expect(admin).not.toBeChecked());
});

it("uses the host’s selected account on a new dev port only once across a login redirect", async () => {
  const url = "http://localhost:20024", origin = "http://p-example.localhost:5555";
  vi.mocked(PreviewService.Open).mockResolvedValue(origin + "/login");
  vi.mocked(PreviewService.TestLogins).mockResolvedValue({ project: "shop", profiles: [{ name: "Buyer", username: "buyer", agent: false }], autoLogin: { name: "Buyer", origin: "http://localhost" } });
  vi.mocked(PreviewService.AutoTestLogin).mockResolvedValue({ username: "buyer", password: "fixture" });
  render(PreviewWorkspace);
  preview.open("s", url);
  const frame = await screen.findByTitle("Preview of localhost:20024") as HTMLIFrameElement;
  const post = vi.spyOn(frame.contentWindow!, "postMessage");
  const say = (type: string, documentId: string) => fireEvent(window, new MessageEvent("message", { source: frame.contentWindow, origin,
    data: { source: "lola-preview", type, documentId, url: origin + "/login" },
  }));
  try {
    await say("ready", "first");
    await waitFor(() => expect(post).toHaveBeenCalledWith({ target: "lola-preview", type: "watch-login", documentId: "first", on: true }, origin));
    await say("login-form", "stale");
    expect(PreviewService.AutoTestLogin).not.toHaveBeenCalled();
    await say("login-form", "first");
    await waitFor(() => expect(post).toHaveBeenCalledWith(expect.objectContaining({ type: "test-login", automatic: true, username: "buyer" }), origin));
    await say("ready", "redirect");
    await say("login-form", "redirect");
    expect(PreviewService.AutoTestLogin).toHaveBeenCalledTimes(1);
    expect(PreviewService.AutoTestLogin).toHaveBeenCalledWith("s", url, "shop", "Buyer", origin);
    vi.mocked(PreviewService.UseTestLogin).mockResolvedValue({ username: "buyer", password: "fixture" });
    await fireEvent.click(screen.getByRole("button", { name: "Test logins" }));
    expect(await screen.findByRole("checkbox", { name: "Auto-login as Buyer" })).toBeChecked();
    expect(screen.queryByText(/currently on/)).not.toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: /Buyer buyer/ }));
    await waitFor(() => expect(post).toHaveBeenCalledWith({
      target: "lola-preview", type: "test-login", documentId: "redirect", username: "buyer", password: "fixture",
    }, origin));
  } finally { post.mockRestore(); }
});

it("reconnects to the redirected login document when a saved login is clicked before ready", async () => {
  const url = "http://localhost:20016/cp/dashboard", origin = "http://p-example.localhost:5555";
  vi.mocked(PreviewService.Open).mockResolvedValue(origin + "/cp/dashboard");
  vi.mocked(PreviewService.TestLogins).mockResolvedValue({ project: "shop", autoLogin: noAuto,
    profiles: [{ name: "Buyer", username: "buyer", agent: false }] });
  vi.mocked(PreviewService.UseTestLogin).mockResolvedValue({ username: "buyer", password: "fixture" });
  render(PreviewWorkspace);
  preview.open("s", url);
  const frame = await screen.findByTitle(/Preview of localhost:20016/) as HTMLIFrameElement;
  const post = vi.spyOn(frame.contentWindow!, "postMessage");
  try {
    await fireEvent.click(screen.getByRole("button", { name: "Test logins" }));
    await fireEvent.click(await screen.findByRole("button", { name: /Buyer buyer/ }));
    expect(post).toHaveBeenCalledWith(expect.objectContaining({ type: "hello" }), origin);
    expect(PreviewService.UseTestLogin).not.toHaveBeenCalled();
    await fireEvent(window, new MessageEvent("message", { source: frame.contentWindow, origin,
      data: { source: "lola-preview", type: "ready", documentId: "login", url: origin + "/cp/auth/login" } }));
    await waitFor(() => expect(post).toHaveBeenCalledWith(expect.objectContaining({ type: "test-login", documentId: "login" }), origin));
  } finally { post.mockRestore(); }
});
