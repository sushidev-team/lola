import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import NewSession from "./NewSession.svelte";
import { store, type ProjectInfo } from "$lib/store.svelte";
import { nav } from "$lib/nav.svelte";
import { ConfigService } from "@bindings/desktop";

beforeEach(() => {
  cleanup(); vi.restoreAllMocks();
  store.projects = [{name:"acme",label:"Acme",path:"/code/acme",agent:"codex"} as ProjectInfo];
  nav.newSession("acme");
});

it("creates a named worktree with the selected agent", async () => {
  const open = vi.spyOn(store,"openManual").mockResolvedValue(undefined);
  render(NewSession);
  await fireEvent.input(screen.getByLabelText("Name"), {target:{value:"Investigate checkout"}});
  await fireEvent.input(screen.getByLabelText("New branch"), {target:{value:"fix/checkout"}});
  await fireEvent.change(screen.getByLabelText("Agent"), {target:{value:"opencode"}});
  await fireEvent.click(screen.getByRole("button",{name:"Create session"}));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({project:"acme",name:"Investigate checkout",branch:"fix/checkout",agent:true,agentKind:"opencode",location:"worktree"}));
  expect(screen.getByLabelText("Name")).toHaveValue("Investigate checkout");
  expect(screen.getByRole("alert")).toBeInTheDocument();
});

it("does not carry branch options into a main-checkout shell", async () => {
  const open = vi.spyOn(store,"openManual").mockResolvedValue(undefined);
  render(NewSession);
  await fireEvent.input(screen.getByLabelText("New branch"), {target:{value:"do-not-create"}});
  await fireEvent.change(screen.getByLabelText("Location"), {target:{value:"main"}});
  await fireEvent.change(screen.getByLabelText("Run"), {target:{value:"shell"}});
  await fireEvent.click(screen.getByRole("button",{name:"Create session"}));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({location:"main",branch:"",base:undefined,agent:false,agentKind:undefined}));
  expect(screen.queryByLabelText("New branch")).not.toBeInTheDocument();
});

it("creates a project-free shell in the requested directory", async () => {
  nav.newSession();
  const open = vi.spyOn(store,"openManual").mockResolvedValue(undefined);
  render(NewSession);
  await fireEvent.input(screen.getByLabelText("Name"), {target:{value:"Scratch shell"}});
  await fireEvent.input(screen.getByLabelText("Directory"), {target:{value:"~/Downloads"}});
  await fireEvent.click(screen.getByRole("button",{name:"Create session"}));
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
  await fireEvent.click(screen.getByRole("button",{name:"Create session"}));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({path:"/code/Scratch folder"}));
});

it("keeps temporary-session guidance in the title info popover", async () => {
  nav.newSession();
  render(NewSession);
  expect(screen.getByLabelText("Directory")).toHaveAttribute("placeholder", "Optional — uses default from Settings");
  expect(screen.queryByText(/A shell without project setup/)).not.toBeInTheDocument();
  await fireEvent.click(screen.getByRole("button", {name:"More about New temporary session"}));
  expect(screen.getByRole("note", {name:"New temporary session"})).toHaveTextContent("A shell without project setup.");
});
