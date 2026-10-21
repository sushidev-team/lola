import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import GoalAuthorization from "./GoalAuthorization.svelte";
import type { StartOptions } from "@bindings/internal/goal";
const api=vi.hoisted(()=>({GoalStartOptions:vi.fn(),GoalStart:vi.fn()}));
vi.mock("@bindings/desktop",()=>({DaemonService:api}));
function options(): StartOptions {
 return {goal:{id:"g",revision:8,title:"Invitations"} as StartOptions["goal"],authorization:{id:"g",revision:8,policy:{members:[{id:"api",project:"api",source:"/repos/api",repo:"team/api",target:"main"}],checks:[],profiles:["backend"],publish_pr:false,replan:false,limits:{concurrency:1,max_tasks:2,attempts_per_task:2,review_rounds:2,agent_runs:30,runtime_minutes:120,attempt_minutes:30,tokens:500000,spend_usd:0,target_refreshes:0}}},profiles:[{id:"backend",kind:"backend",required:true,artifacts:[]},{id:"uiux",kind:"uiux",required:false,artifacts:["screenshot","runtime"]}]};
}
beforeEach(()=>{api.GoalStartOptions.mockReset().mockImplementation(async()=>options());api.GoalStart.mockReset().mockResolvedValue(undefined);});
afterEach(cleanup);
async function open(){await fireEvent.click(screen.getByRole("button",{name:"Set execution limits"}));await screen.findByText(/revision 8/);}
async function check(){
 await fireEvent.click(screen.getByRole("button",{name:"Add check"}));
 await fireEvent.input(screen.getByLabelText("Check command"),{target:{value:"npm test"}});
 await fireEvent.input(screen.getByLabelText("Check image digest"),{target:{value:`sha256:${"a".repeat(64)}`}});
}
describe("GoalAuthorization",()=>{
 it("requires a check and a separate review before sending exact scope and revision",async()=>{
  const started=vi.fn();render(GoalAuthorization,{goalID:"g",onstarted:started});
  expect(api.GoalStartOptions).not.toHaveBeenCalled();await open();
  expect(api.GoalStartOptions).toHaveBeenCalledExactlyOnceWith("g");
  expect(screen.getByRole("button",{name:"Review authorization"})).toBeDisabled();
  expect(screen.getByRole("checkbox",{name:/backend/})).toBeChecked();
  expect(screen.getByRole("checkbox",{name:/backend/})).toBeDisabled();
  await check();
  await fireEvent.input(screen.getByLabelText("Allowed path prefixes for api"),{target:{value:" src/invitations \n\n tests "}});
  await fireEvent.input(screen.getByLabelText("Observed token limit"),{target:{value:"80000"}});
  await fireEvent.click(screen.getByRole("button",{name:"Review authorization"}));
  expect(api.GoalStart).not.toHaveBeenCalled();
  expect(screen.getByRole("group",{name:"Review execution authorization"})).toHaveFocus();
  expect(screen.getByRole("button",{name:"Authorize and start"})).toBeDisabled();
  expect(screen.getByText(/Draft PR publication: disabled/)).toBeInTheDocument();
  await fireEvent.click(screen.getByRole("checkbox",{name:/I authorize/}));
  await fireEvent.click(screen.getByRole("button",{name:"Authorize and start"}));
  await waitFor(()=>expect(started).toHaveBeenCalledTimes(1));
  expect(api.GoalStart).toHaveBeenCalledTimes(1);
  expect(api.GoalStart.mock.calls[0][0]).toMatchObject({id:"g",revision:8,policy:{publish_pr:false,replan:false,profiles:["backend"],limits:{tokens:80000},members:[{id:"api",paths:["src/invitations","tests"]}],checks:[{command:"npm test",member:"api",image:`sha256:${"a".repeat(64)}`} ]}});
 });
 it("holds an uncertain authorization until current settings are explicitly reloaded",async()=>{
  api.GoalStart.mockRejectedValue(new Error("socket timeout"));
  render(GoalAuthorization,{goalID:"g",onstarted:vi.fn()});await open();await check();
  await fireEvent.click(screen.getByRole("button",{name:"Review authorization"}));
  await fireEvent.click(screen.getByRole("checkbox",{name:/I authorize/}));
  await fireEvent.click(screen.getByRole("button",{name:"Authorize and start"}));
  await screen.findByRole("alert");
  expect(screen.getByRole("button",{name:"Authorize and start"})).toBeDisabled();
  expect(screen.getByRole("button",{name:"Back to limits"})).toBeDisabled();
  expect(api.GoalStart).toHaveBeenCalledTimes(1);
  api.GoalStartOptions.mockRejectedValue(new Error("already authorized"));
  await fireEvent.click(screen.getByRole("button",{name:"Reload settings"}));
  await screen.findByText(/already authorized/);
  expect(screen.queryByRole("button",{name:"Authorize and start"})).not.toBeInTheDocument();
 });
 it("retains selected specialists and captured evidence in the review",async()=>{
  render(GoalAuthorization,{goalID:"g",onstarted:vi.fn()});await open();await check();
  await fireEvent.click(screen.getByRole("checkbox",{name:/uiux/}));
  await fireEvent.click(screen.getByRole("button",{name:"Captured evidence"}));
  await fireEvent.click(screen.getByRole("button",{name:"Add evidence"}));
  await fireEvent.input(screen.getByLabelText("Evidence ID"),{target:{value:"desktop"}});
  await fireEvent.input(screen.getByLabelText("Evidence kind"),{target:{value:"screenshot"}});
  await fireEvent.input(screen.getByLabelText("Evidence path"),{target:{value:"api/evidence/desktop.png"}});
  await fireEvent.click(screen.getByRole("checkbox",{name:/Allow verified goal branches/}));
  await fireEvent.click(screen.getByRole("button",{name:"Review authorization"}));
  expect(screen.getByText("Required reviews: backend, uiux")).toBeInTheDocument();
  expect(screen.getByText("desktop: screenshot from api/evidence/desktop.png")).toBeInTheDocument();
  expect(screen.getByText(/Draft PR publication: allowed after verification/)).toBeInTheDocument();
  expect(api.GoalStart).not.toHaveBeenCalled();
 });
});
