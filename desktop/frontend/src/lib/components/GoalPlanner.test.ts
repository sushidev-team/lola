import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import GoalPlanner from "./GoalPlanner.svelte";
import type { Record as Proposal } from "@bindings/internal/goalplan";

const api = vi.hoisted(() => ({ GoalProposals: vi.fn(), GoalProposal: vi.fn(), GoalPropose: vi.fn(), GoalAcceptPlan: vi.fn() }));
vi.mock("@bindings/desktop", () => ({ DaemonService: api }));
function sample(): Proposal {
 return { id:"proposal-id", goal_id:"goal-id", revision:7, state:"proposed", agent:"codex", image:"image", input_sha256:"digest", started_at:"2026-10-21T12:00:00Z", input:{ request:{id:"proposal-id",project:"nori",title:"Invitations",outcome:"Invite a colleague",acceptance:["Recipient accepts"],non_goals:["Billing"],max_tasks:10,timeout_seconds:180},team_id:"team",linear_project_id:"linear-project",stack:false,members:[],labels:[],knowledge:{lessons:[],references:[]}},output:{tasks:[{title:"Invitation API",description:"Implement invitation endpoints",acceptance:["Acceptance test passes"],apps:["api"],paths:{api:["src/invitations"]},covers:[1]}],unknowns:["Which mail provider is configured?"]} };
}
beforeEach(() => {
 api.GoalProposals.mockReset().mockResolvedValue([]);
 api.GoalProposal.mockReset().mockResolvedValue(sample());
 api.GoalPropose.mockReset().mockResolvedValue(sample());
 api.GoalAcceptPlan.mockReset().mockResolvedValue("goal-id");
});
afterEach(cleanup);
async function open() {
 await fireEvent.click(screen.getByRole("button",{name:"Plan a goal"}));
 await waitFor(()=>expect(screen.getByLabelText("Title")).toBeEnabled());
}
async function fill() {
 await fireEvent.input(screen.getByLabelText("Title"),{target:{value:"Invitations"}});
 await fireEvent.input(screen.getByLabelText("Desired outcome"),{target:{value:"Invite a colleague"}});
 await fireEvent.input(screen.getByLabelText("Acceptance criteria"),{target:{value:"Recipient accepts\n  Expired links fail "}});
}
describe("GoalPlanner",()=>{
 it("requires review after one bounded proposal and keeps the exact acceptance revision",async()=>{
  const accepted=vi.fn();render(GoalPlanner,{project:"nori",onaccepted:accepted});
  expect(api.GoalProposals).not.toHaveBeenCalled();await open();await fill();
  await fireEvent.click(screen.getByRole("button",{name:"Request task proposal"}));
  await screen.findByText("Which mail provider is configured?");
  expect(api.GoalPropose).toHaveBeenCalledTimes(1);
  expect(api.GoalPropose.mock.calls[0][0]).toMatchObject({project:"nori",max_tasks:10,timeout_seconds:180,acceptance:["Recipient accepts","Expired links fail"]});
  expect(api.GoalPropose.mock.calls[0][0].id).toMatch(/^[a-f0-9-]{36}$/);
  expect(api.GoalAcceptPlan).not.toHaveBeenCalled();
  expect(screen.getByRole("button",{name:"Accept as local goal"})).toBeDisabled();
  await fireEvent.click(screen.getByRole("checkbox"));
  await fireEvent.click(screen.getByRole("button",{name:"Accept as local goal"}));
  await screen.findByText(/Local goal created/);
  expect(api.GoalAcceptPlan).toHaveBeenCalledExactlyOnceWith("proposal-id",7);
  expect(accepted).toHaveBeenCalledExactlyOnceWith("goal-id");
 });
 it("recovers an uncertain call by reading its retained identity without replaying it",async()=>{
  api.GoalPropose.mockRejectedValue(new Error("socket timeout"));
  render(GoalPlanner,{project:"nori",onaccepted:vi.fn()});await open();await fill();
  await fireEvent.click(screen.getByRole("button",{name:"Request task proposal"}));
  await screen.findByRole("alert");
  const id=api.GoalPropose.mock.calls[0][0].id;
  expect(screen.getByText(`Proposal ID: ${id}`)).toBeInTheDocument();
  expect(screen.queryByRole("button",{name:"Request task proposal"})).not.toBeInTheDocument();
  await fireEvent.click(screen.getByRole("button",{name:"Refresh result"}));
  await screen.findByRole("heading", { name: "1. Invitation API" });
  expect(api.GoalProposal).toHaveBeenCalledExactlyOnceWith(id);
  expect(api.GoalPropose).toHaveBeenCalledTimes(1);
 });
 it("requires a fresh read and review after rejected acceptance",async()=>{
  api.GoalAcceptPlan.mockRejectedValue(new Error("changed revision"));
  render(GoalPlanner,{project:"nori",onaccepted:vi.fn()});await open();await fill();
  await fireEvent.click(screen.getByRole("button",{name:"Request task proposal"}));
  await screen.findByRole("checkbox");await fireEvent.click(screen.getByRole("checkbox"));
  await fireEvent.click(screen.getByRole("button",{name:"Accept as local goal"}));
  await screen.findByRole("alert");
  expect(screen.getByRole("button",{name:"Accept as local goal"})).toBeDisabled();
  api.GoalProposal.mockResolvedValue({...sample(),revision:8});
  await fireEvent.click(screen.getByRole("button",{name:"Refresh result"}));
  await screen.findByText(/revision 8/);
  expect(screen.getByRole("checkbox")).not.toBeChecked();
  expect(api.GoalAcceptPlan).toHaveBeenCalledTimes(1);
 });
 it("clears the previous review if inspection of another saved proposal fails",async()=>{
  api.GoalProposals.mockResolvedValue([{id:"first",title:"First",state:"proposed"},{id:"second",title:"Second",state:"proposed"}]);
  render(GoalPlanner,{project:"nori",onaccepted:vi.fn()});await open();
  await fireEvent.change(screen.getByLabelText("Saved proposals"),{target:{value:"first"}});
  await screen.findByRole("checkbox");
  expect(api.GoalProposal).toHaveBeenLastCalledWith("first");
  api.GoalProposal.mockRejectedValue(new Error("missing"));
  await fireEvent.change(screen.getByLabelText("Saved proposals"),{target:{value:"second"}});
  await screen.findByRole("alert");
  expect(api.GoalProposal).toHaveBeenLastCalledWith("second");
  expect(screen.queryByRole("button",{name:"Accept as local goal"})).not.toBeInTheDocument();
 });
 it("keeps the pending operation busy when hidden and reopened",async()=>{
  let resolve!: (p:Proposal)=>void;
  api.GoalPropose.mockImplementation(()=>new Promise<Proposal>(r=>resolve=r));
  render(GoalPlanner,{project:"nori",onaccepted:vi.fn()});await open();await fill();
  await fireEvent.click(screen.getByRole("button",{name:"Request task proposal"}));
  await screen.findByRole("status");
  await fireEvent.click(screen.getByRole("button",{name:"Hide goal planner"}));
  await fireEvent.click(screen.getByRole("button",{name:"Plan a goal"}));
  expect(api.GoalProposals).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button",{name:"Refresh result"})).toBeDisabled();
  expect(screen.getByRole("button",{name:"New proposal brief"})).toBeDisabled();
  resolve(sample());await screen.findByRole("checkbox");
 });
 it("does not notify a new project after the initiating planner unmounts",async()=>{
  let resolve!: (id:string)=>void;
  api.GoalAcceptPlan.mockImplementation(()=>new Promise<string>(r=>resolve=r));
  const accepted=vi.fn();const view=render(GoalPlanner,{project:"nori",onaccepted:accepted});await open();await fill();
  await fireEvent.click(screen.getByRole("button",{name:"Request task proposal"}));
  await screen.findByRole("checkbox");await fireEvent.click(screen.getByRole("checkbox"));
  await fireEvent.click(screen.getByRole("button",{name:"Accept as local goal"}));
  view.unmount();resolve("goal-id");
  await Promise.resolve();expect(accepted).not.toHaveBeenCalled();
 });
});
