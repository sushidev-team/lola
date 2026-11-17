import { beforeEach, it, expect, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/svelte";
const close = vi.fn();
const shells = vi.fn();
vi.mock("./LiveTerminal.svelte", async () => ({default: (await import("./TerminalExitHarness.test.svelte")).default}));
vi.mock("@bindings/desktop", () => ({
  DaemonService: { CloseExitedTemporary: (...args: unknown[]) => close(...args) },
  ConfigService: {},
  TermService: { Shells: (...args: unknown[]) => shells(...args) },
}));
const { default: SessionEmbed } = await import("./SessionEmbed.svelte");
const { store } = await import("$lib/store.svelte");
const { nav } = await import("$lib/nav.svelte");
const { terms, AGENT } = await import("$lib/terms.svelte");
const { openSession } = await import("$lib/projectActions");

beforeEach(async () => {
  cleanup(); vi.restoreAllMocks();
  close.mockReset().mockResolvedValue({removed:true});
  shells.mockReset().mockResolvedValue([]);
  vi.spyOn(store,"refresh").mockResolvedValue(undefined);
  store.sessions = [{id:"temp",tmuxName:"temp",worktree:"/tmp",location:"temporary",status:"shell"}] as never;
  terms.select("temp",AGENT);
  await terms.refresh("temp");
  openSession("temp");
});

it.each(["temporary", "main", "checkout"])("retires an exited %s shell session and leaves fullscreen", async location => {
  store.sessions = [{...store.sessions[0],location}] as never;
  render(SessionEmbed,{sessionId:"temp"});
  await fireEvent.click(screen.getByRole("button",{name:"Exit temp"}));
  await waitFor(() => expect(nav.focusedTerm).toBe(""));
  expect(close).toHaveBeenCalledWith("temp");
  expect(store.sessionById("temp")).toBeUndefined();
});

it("keeps a still-running session after an attach client exits", async () => {
  close.mockResolvedValue({removed:false});
  render(SessionEmbed,{sessionId:"temp"});
  await fireEvent.click(screen.getByRole("button",{name:"Exit temp"}));
  await waitFor(() => expect(close).toHaveBeenCalled());
  expect(nav.focusedTerm).toBe("temp");
  expect(store.sessionById("temp")).toBeDefined();
});

it("switches to a remaining shell tab instead of closing it", async () => {
  store.sessions = [{...store.sessions[0],location:"main"}] as never;
  close.mockResolvedValue({removed:false});
  shells.mockResolvedValue(["temp-shell-1"]);
  render(SessionEmbed,{sessionId:"temp"});
  await fireEvent.click(screen.getByRole("button",{name:"Exit temp"}));
  await waitFor(() => expect(terms.activeTab("temp")).toBe("temp-shell-1"));
  expect(nav.focusedTerm).toBe("temp");
  close.mockResolvedValue({removed:true});
  await fireEvent.click(await screen.findByRole("button",{name:"Exit temp-shell-1"}));
  await waitFor(() => expect(store.sessionById("temp")).toBeUndefined());
});

it("keeps an agent session when its auxiliary shell exits", async () => {
  store.sessions = [{...store.sessions[0],location:"main",status:"idle",agentState:"idle"}] as never;
  shells.mockResolvedValue(["temp-shell-1"]);
  await terms.refresh("temp");
  terms.select("temp","temp-shell-1");
  render(SessionEmbed,{sessionId:"temp"});
  await fireEvent.click(await screen.findByRole("button",{name:"Exit temp-shell-1"}));
  expect(close).not.toHaveBeenCalled();
  expect(store.sessionById("temp")).toBeDefined();
});

it("does not navigate away from a different session opened during cleanup", async () => {
  let finish!: (result: {removed:boolean}) => void;
  close.mockImplementation(() => new Promise(resolve => {finish=resolve;}));
  render(SessionEmbed,{sessionId:"temp"});
  await fireEvent.click(screen.getByRole("button",{name:"Exit temp"}));
  openSession("other");
  finish({removed:true});
  await waitFor(() => expect(store.sessionById("temp")).toBeUndefined());
  expect(nav.focusedTerm).toBe("other");
});
