import { it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor, within } from "@testing-library/svelte";
import NewSession from "./NewSession.svelte";
import { store, type ProjectInfo } from "$lib/store.svelte";
import { nav } from "$lib/nav.svelte";
import { ConfigService } from "@bindings/desktop";
import { tick } from "svelte";

const box = () => document.querySelector("textarea") as HTMLTextAreaElement;
const started = (open: { mock: { calls: unknown[][] } }) => open.mock.calls[0][0] as Record<string, unknown>;

beforeEach(() => {
  cleanup(); vi.restoreAllMocks();
  store.projects = [{name:"acme",label:"Acme",path:"/code/acme",agent:"codex",defaultBranch:"main"} as ProjectInfo];
  vi.spyOn(ConfigService,"GetProject").mockResolvedValue({branchPrefix:"feat/"} as never);
  vi.spyOn(ConfigService,"InspectPath").mockResolvedValue({branches:["main", "release", "feature/login"]} as never);
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
  await fireEvent.click(screen.getByRole("button",{name:"Browse for session directory"}));
  expect(pick).toHaveBeenCalledWith("");
  expect(screen.getByLabelText("Directory")).toHaveValue("/code/Scratch folder");
  await fireEvent.click(screen.getByRole("button",{name:"Browse for session directory"}));
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

it("offers available base branches and submits the selected base with a new branch", async () => {
  const open = vi.spyOn(store, "openManual").mockResolvedValue(undefined);
  render(NewSession);
  await fireEvent.click(screen.getByRole("button", {name:/Options/}));
  const base = screen.getByRole("combobox", {name:"Base branch"});
  await waitFor(() => expect(base).toBeEnabled());
  expect(ConfigService.InspectPath).toHaveBeenCalledWith("/code/acme");
  expect(base).toHaveValue("");
  expect(screen.getByRole("option", {name:"main (project default)"})).toBeInTheDocument();
  expect(within(base).getByRole("option", {name:"feature/login"})).toBeInTheDocument();
  await fireEvent.change(base, {target:{value:"release"}});
  await fireEvent.input(box(), {target:{value:"Fix login"}});
  await fireEvent.click(screen.getByRole("button", {name:"Start"}));
  expect(started(open)).toMatchObject({base:"release", branch:"feat/fix-login"});
});

it("keeps the configured default when it is absent from the branch list", async () => {
  store.projects = [{...store.projects[0], defaultBranch:"trunk"}];
  const open = vi.spyOn(store, "openManual").mockResolvedValue(undefined);
  render(NewSession);
  await fireEvent.click(screen.getByRole("button", {name:/Options/}));
  await waitFor(() => expect(screen.getByRole("option", {name:"trunk (project default)"})).toBeInTheDocument());
  await fireEvent.input(box(), {target:{value:"Fix login"}});
  await fireEvent.click(screen.getByRole("button", {name:"Start"}));
  expect(started(open).base).toBeUndefined();
});

it.each(["failed", "empty"])("allows a custom base when branch discovery is %s", async (result) => {
  if (result === "failed") vi.mocked(ConfigService.InspectPath).mockRejectedValue(new Error("offline"));
  else vi.mocked(ConfigService.InspectPath).mockResolvedValue({branches:[]} as never);
  const open = vi.spyOn(store, "openManual").mockResolvedValue(undefined);
  render(NewSession);
  await fireEvent.click(screen.getByRole("button", {name:/Options/}));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Could not list branches"));
  await fireEvent.input(box(), {target:{value:"Fix login"}});
  await fireEvent.change(screen.getByLabelText("Base branch"), {target:{value:":custom"}});
  expect(screen.getByRole("button", {name:"Start"})).toBeDisabled();
  await fireEvent.input(screen.getByLabelText("Other base branch"), {target:{value:" hotfix/checkout "}});
  await fireEvent.click(screen.getByRole("button", {name:"Start"}));
  expect(started(open).base).toBe("hotfix/checkout");
});

it("resets the base on project changes and ignores stale branch discovery", async () => {
  store.projects = [...store.projects, {name:"other", label:"Other", path:"/code/other", defaultBranch:"trunk"} as ProjectInfo];
  let resolveFirst!: (value: never) => void;
  vi.mocked(ConfigService.InspectPath)
    .mockImplementationOnce(() => new Promise(resolve => { resolveFirst = resolve; }) as never)
    .mockResolvedValueOnce({branches:["trunk", "stable"]} as never)
    .mockResolvedValueOnce({branches:["main", "fresh"]} as never);
  render(NewSession);
  await fireEvent.click(screen.getByRole("button", {name:/Options/}));
  expect(screen.getByLabelText("Base branch")).toBeDisabled();
  await fireEvent.change(screen.getByLabelText("Project"), {target:{value:"other"}});
  await waitFor(() => expect(within(screen.getByLabelText("Base branch")).getByRole("option", {name:"stable"})).toBeInTheDocument());
  await fireEvent.change(screen.getByLabelText("Base branch"), {target:{value:"stable"}});
  await fireEvent.change(screen.getByLabelText("Project"), {target:{value:"acme"}});
  await waitFor(() => expect(within(screen.getByLabelText("Base branch")).getByRole("option", {name:"fresh"})).toBeInTheDocument());
  resolveFirst({branches:["stale"]} as never);
  await waitFor(() => expect(screen.getByLabelText("Base branch")).toHaveValue(""));
  expect(screen.queryByRole("option", {name:"stale"})).not.toBeInTheDocument();
  expect(screen.queryByRole("option", {name:"stable"})).not.toBeInTheDocument();
});

it("works directly on a selected existing branch without needing a new name", async () => {
  const open = vi.spyOn(store, "openManual").mockResolvedValue(undefined);
  render(NewSession);
  const branch = screen.getByRole("combobox", {name:"Branch"});
  await waitFor(() => expect(branch).toBeEnabled());
  await fireEvent.change(branch, {target:{value:"feature/login"}});
  expect(screen.queryByLabelText("New branch")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("Base branch")).not.toBeInTheDocument();
  await fireEvent.input(box(), {target:{value:"Fix the redirect"}});
  await fireEvent.click(screen.getByRole("button", {name:"Start"}));
  expect(started(open)).toMatchObject({branch:"feature/login", existing:true, prompt:"Fix the redirect", agent:true});
  expect(started(open).base).toBeUndefined();
});

it("switches back to creating a new branch with the automatic name", async () => {
  const open = vi.spyOn(store, "openManual").mockResolvedValue(undefined);
  render(NewSession);
  await waitFor(() => expect(screen.getByLabelText("Branch")).toBeEnabled());
  await fireEvent.change(screen.getByLabelText("Branch"), {target:{value:"release"}});
  await fireEvent.input(box(), {target:{value:"Fix login"}});
  await fireEvent.change(screen.getByLabelText("Branch"), {target:{value:":new"}});
  await fireEvent.click(screen.getByRole("button", {name:"Start"}));
  expect(started(open).branch).toBe("feat/fix-login");
  expect(started(open).existing).toBeUndefined();
});

it("keeps the selected branch when polling refreshes the same project", async () => {
  render(NewSession);
  await waitFor(() => expect(screen.getByLabelText("Branch")).toBeEnabled());
  await fireEvent.change(screen.getByLabelText("Branch"), {target:{value:"feature/login"}});
  store.projects = store.projects.map(p => ({...p}));
  await tick();
  expect(screen.getByLabelText("Branch")).toHaveValue("feature/login");
  expect(ConfigService.InspectPath).toHaveBeenCalledTimes(1);
});

it("supports an existing branch in a shell and ignores the choice in the main checkout", async () => {
  const open = vi.spyOn(store, "openManual").mockResolvedValue(undefined);
  render(NewSession);
  await waitFor(() => expect(screen.getByLabelText("Branch")).toBeEnabled());
  await fireEvent.change(screen.getByLabelText("Branch"), {target:{value:":existing"}});
  expect(screen.getByRole("button", {name:"Start"})).toBeDisabled();
  await fireEvent.input(screen.getByLabelText("Existing branch"), {target:{value:" feature/custom "}});
  await fireEvent.change(screen.getByLabelText("Run"), {target:{value:"shell"}});
  await fireEvent.click(screen.getByRole("button", {name:"Start"}));
  expect(started(open)).toMatchObject({branch:"feature/custom", existing:true, agent:false});
  await fireEvent.change(screen.getByLabelText("Location"), {target:{value:"main"}});
  await fireEvent.click(screen.getByRole("button", {name:"Start"}));
  expect(open.mock.calls[1][0]).toMatchObject({branch:"", existing:undefined, location:"main"});
});
