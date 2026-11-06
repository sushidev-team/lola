import { it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/svelte";
import NewSession from "./NewSession.svelte";
import { store, type ProjectInfo } from "$lib/store.svelte";
import { nav } from "$lib/nav.svelte";
import { ConfigService } from "@bindings/desktop";

const box = () => document.querySelector("textarea") as HTMLTextAreaElement;
const started = (open: { mock: { calls: unknown[][] } }) => open.mock.calls[0][0] as Record<string, unknown>;

beforeEach(() => {
  cleanup(); vi.restoreAllMocks();
  store.projects = [{name:"acme",label:"Acme",path:"/code/acme",agent:"codex"} as ProjectInfo];
  vi.spyOn(ConfigService,"GetProject").mockResolvedValue({branchPrefix:"feat/"} as never);
  nav.newSession("acme");
});

it("prompt alone starts a session", async () => {
  const open = vi.spyOn(store,"openManual").mockResolvedValue(undefined);
  render(NewSession);
  await waitFor(() => expect(document.activeElement).toBe(box()));
  await fireEvent.input(box(), {target:{value:"Fix the login redirect after logout"}});
  await fireEvent.keyDown(box(), {key:"Enter"});
  expect(open).toHaveBeenCalledTimes(1);
  expect(started(open)).toMatchObject({project:"acme",prompt:"Fix the login redirect after logout",agent:true,location:"worktree"});
});

it("branch derives from the prompt", async () => {
  const open = vi.spyOn(store,"openManual").mockResolvedValue(undefined);
  render(NewSession);
  await fireEvent.input(box(), {target:{value:"Fix the login redirect after logout please"}});
  await waitFor(() => expect(screen.getByLabelText("New branch")).toHaveValue("feat/fix-the-login-redirect-after-logout"));
  await fireEvent.click(screen.getByRole("button",{name:"Start"}));
  expect(started(open).branch).toBe("feat/fix-the-login-redirect-after-logout");
});

it("a branch someone typed stays when the prompt changes", async () => {
  render(NewSession);
  await fireEvent.input(screen.getByLabelText("New branch"), {target:{value:"fix/mine"}});
  await fireEvent.input(box(), {target:{value:"Something else entirely"}});
  expect(screen.getByLabelText("New branch")).toHaveValue("fix/mine");
});

it("falls back to the defaults' prefix, then lola's own", async () => {
  vi.spyOn(ConfigService,"GetProject").mockResolvedValue({branchPrefix:""} as never);
  vi.spyOn(ConfigService,"GetSettings").mockResolvedValue({branchPrefix:"team/"} as never);
  render(NewSession);
  await fireEvent.input(box(), {target:{value:"tidy"}});
  await waitFor(() => expect(screen.getByLabelText("New branch")).toHaveValue("team/tidy"));
  cleanup();
  vi.spyOn(ConfigService,"GetProject").mockRejectedValue("no");
  render(NewSession);
  await fireEvent.input(box(), {target:{value:"tidy"}});
  await waitFor(() => expect(screen.getByLabelText("New branch")).toHaveValue("lola/tidy"));
});

it("⇧↩ and ⌥↩ are a newline and start nothing", async () => {
  const open = vi.spyOn(store,"openManual").mockResolvedValue(undefined);
  render(NewSession);
  await fireEvent.input(box(), {target:{value:"one"}});
  await fireEvent.keyDown(box(), {key:"Enter",shiftKey:true});
  await fireEvent.keyDown(box(), {key:"Enter",altKey:true});
  expect(open).not.toHaveBeenCalled();
});

it("names the project's agent in the question, and the override once picked", async () => {
  render(NewSession);
  expect(screen.getByText("What should Codex do?")).toBeInTheDocument();
  await fireEvent.change(screen.getByLabelText("Agent"), {target:{value:"claude"}});
  expect(screen.getByText("What should Claude do?")).toBeInTheDocument();
});

it("cannot start a worktree with neither a prompt nor a branch", async () => {
  const open = vi.spyOn(store,"openManual").mockResolvedValue(undefined);
  render(NewSession);
  expect(screen.getByRole("button",{name:"Start"})).toBeDisabled();
  await fireEvent.keyDown(box(), {key:"Enter"});
  expect(open).not.toHaveBeenCalled();
});

it("keeps options under one line, with a name and the agent inside", async () => {
  const open = vi.spyOn(store,"openManual").mockResolvedValue(undefined);
  render(NewSession);
  await fireEvent.input(screen.getByLabelText("Name"), {target:{value:"Investigate checkout"}});
  await fireEvent.input(screen.getByLabelText("New branch"), {target:{value:"fix/checkout"}});
  await fireEvent.change(screen.getByLabelText("Agent"), {target:{value:"opencode"}});
  await fireEvent.click(screen.getByRole("button",{name:"Start"}));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({project:"acme",name:"Investigate checkout",branch:"fix/checkout",agent:true,agentKind:"opencode",location:"worktree"}));
  expect(screen.getByLabelText("Name")).toHaveValue("Investigate checkout");
  expect(screen.getByRole("alert")).toBeInTheDocument();
});

it("shell hides the prompt", async () => {
  const open = vi.spyOn(store,"openManual").mockResolvedValue(undefined);
  render(NewSession);
  await fireEvent.input(box(), {target:{value:"forget this"}});
  await fireEvent.change(screen.getByLabelText("Run"), {target:{value:"shell"}});
  expect(box()).toBeNull();
  expect(screen.queryByText(/What should/)).toBeNull();
  await fireEvent.change(screen.getByLabelText("Location"), {target:{value:"main"}});
  await fireEvent.click(screen.getByRole("button",{name:"Start"}));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({location:"main",branch:"",base:undefined,agent:false,agentKind:undefined,prompt:undefined}));
  expect(screen.queryByLabelText("New branch")).not.toBeInTheDocument();
});

it("creates a project-free shell in the requested directory", async () => {
  nav.newSession();
  const open = vi.spyOn(store,"openManual").mockResolvedValue(undefined);
  render(NewSession);
  expect(box()).toBeNull();
  await fireEvent.input(screen.getByLabelText("Name"), {target:{value:"Scratch shell"}});
  await fireEvent.input(screen.getByLabelText("Directory"), {target:{value:"~/Downloads"}});
  await fireEvent.click(screen.getByRole("button",{name:"Start"}));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({project:"",location:"temporary",name:"Scratch shell",agent:false,path:"~/Downloads"}));
});

it("uses the native directory picker and keeps the path when cancelled", async () => {
  nav.newSession();
  const pick = vi.spyOn(ConfigService,"PickFolder").mockResolvedValueOnce("/code/Scratch folder").mockResolvedValueOnce("");
  const open = vi.spyOn(store,"openManual").mockResolvedValue(undefined);
  render(NewSession);
  await fireEvent.click(screen.getByRole("button",{name:"Choose folder…"}));
  expect(pick).toHaveBeenCalledWith("");
  expect(screen.getByLabelText("Directory")).toHaveValue("/code/Scratch folder");
  await fireEvent.click(screen.getByRole("button",{name:"Choose folder…"}));
  expect(pick).toHaveBeenLastCalledWith("/code/Scratch folder");
  expect(screen.getByLabelText("Directory")).toHaveValue("/code/Scratch folder");
  await fireEvent.click(screen.getByRole("button",{name:"Start"}));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({path:"/code/Scratch folder"}));
});

it("keeps temporary-session guidance in the title info popover", async () => {
  nav.newSession();
  render(NewSession);
  expect(screen.getByLabelText("Directory")).toHaveAttribute("placeholder", "Optional, uses the default from Settings");
  expect(screen.queryByText(/A shell without project setup/)).not.toBeInTheDocument();
  await fireEvent.click(screen.getByRole("button", {name:"More about New temporary session"}));
  expect(screen.getByRole("note", {name:"New temporary session"})).toHaveTextContent("A shell without project setup.");
});
