import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { freshSession, setTalk, ssh, store, talk, talkSvc } from "./talk.testkit";
import ModelSettings from "./ModelSettings.svelte";

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.clearAllMocks(); });

async function show(agent = "claude", composer = "resting") {
  const id = freshSession({ agent });
  talkSvc.Setting.mockResolvedValue({ sent: true });
  await setTalk(id, { caps: { settings: true }, composer });
  render(ModelSettings, { sessionId: id });
  return id;
}

it("sends a Claude model choice and explains the default-setting effect", async () => {
  const id = await show();
  expect(screen.getByRole("combobox", { name: "Model settings" })).toHaveAttribute("title", expect.stringContaining("also update Claude"));
  expect(screen.getAllByRole("combobox")).toHaveLength(1);
  await fireEvent.change(screen.getByRole("combobox", { name: "Model settings" }), { target: { value: "model=opus" } });
  await waitFor(() => expect(talkSvc.Setting).toHaveBeenCalledWith({ session: id, agent: "claude", setting: "model", value: "opus" }));
  expect(screen.getByRole("status").textContent).toContain("Requested model: opus");
});

it("offers Codex effort and speed without Claude-only defaults", async () => {
  const id = await show("codex");
  expect(screen.queryByRole("option", { name: "Model default" })).toBeNull();
  await fireEvent.change(screen.getByRole("combobox", { name: "Model settings" }), { target: { value: "effort=ultra" } });
  await waitFor(() => expect(talkSvc.Setting).toHaveBeenCalledWith({ session: id, agent: "codex", setting: "effort", value: "ultra" }));
  await fireEvent.change(screen.getByRole("combobox", { name: "Model settings" }), { target: { value: "speed=fast" } });
  await waitFor(() => expect(talkSvc.Setting).toHaveBeenCalledWith({ session: id, agent: "codex", setting: "speed", value: "fast" }));
});

it("opens OpenCode's native model picker inline only after delivery", async () => {
  const id = await show("opencode");
  expect(screen.getByRole("option", { name: "Choose model variant…" })).toBeTruthy();
  await fireEvent.change(screen.getByRole("combobox", { name: "Model settings" }), { target: { value: "model=picker" } });
  await waitFor(() => expect(talk.settingTerminal[id]).toBe(true));
  expect(talkSvc.Setting).toHaveBeenCalledWith({ session: id, agent: "opencode", setting: "model", value: "picker" });
});

it.each(["working", "draft", "prompt", "modal", "unknown", "gone"])("disables settings with a %s composer", async (composer) => {
  await show("claude", composer);
  for (const field of screen.getAllByRole("combobox")) expect(field).toBeDisabled();
  expect(talkSvc.Setting).not.toHaveBeenCalled();
});

it("keeps an in-flight change disabled across remounts and shows refusal", async () => {
  const id = await show();
  let reject!: (err: Error) => void;
  talkSvc.Setting.mockReturnValue(new Promise((_, fail) => { reject = fail; }));
  await fireEvent.change(screen.getByRole("combobox", { name: "Model settings" }), { target: { value: "model=opus" } });
  cleanup();
  render(ModelSettings, { sessionId: id });
  expect(screen.getByRole("combobox", { name: "Model settings" })).toBeDisabled();
  reject(new Error("The active agent changed"));
  await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("The active agent changed"));
  expect(screen.queryByRole("status")).toBeNull();
});

it("does not send partial custom IDs and keeps drafts separate", async () => {
  const id = await show();
  ssh.view(id).agentDraft = "keep this message";
  await fireEvent.change(screen.getByRole("combobox", { name: "Model settings" }), { target: { value: "custom" } });
  await fireEvent.input(screen.getByRole("textbox", { name: "Custom model" }), { target: { value: "private-model" } });
  expect(talkSvc.Setting).not.toHaveBeenCalled();
  await fireEvent.click(screen.getByRole("button", { name: "Set model" }));
  await waitFor(() => expect(talkSvc.Setting).toHaveBeenCalledWith({ session: id, agent: "claude", setting: "model", value: "private-model" }));
  expect(ssh.view(id).agentDraft).toBe("keep this message");
});

it("disables settings offline and hides them for ended sessions", async () => {
  await show();
  store.alive = false;
  await waitFor(() => expect(screen.getByRole("combobox", { name: "Model settings" })).toBeDisabled());
  store.sessions = store.sessions.map((s) => ({ ...s, agentState: "dead" }));
  await waitFor(() => expect(screen.queryByRole("combobox")).toBeNull());
});

it("keeps the terminal fallback compact when the daemon lacks settings support", async () => {
  const id = await show();
  await setTalk(id, { caps: { settings: false }, composer: "working" });
  const field = screen.getByRole("combobox", { name: "Model settings" });
  expect(screen.queryByText("Choose model and settings in the agent’s terminal.")).toBeNull();
  await fireEvent.change(field, { target: { value: "terminal" } });
  expect(talk.settingTerminal[id]).toBe(true);
  expect(talkSvc.Setting).not.toHaveBeenCalled();
});
