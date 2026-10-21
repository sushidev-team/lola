import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import ProjectGoals from "./ProjectGoals.svelte";
import type { Overview } from "@bindings/internal/goal";

const api = vi.hoisted(() => ({ GoalOverview: vi.fn(), GoalControl: vi.fn(), GoalPublish: vi.fn() }));
vi.mock("@bindings/desktop", () => ({ DaemonService: api }));
vi.mock("$lib/store.svelte", () => ({ store: { openURL: vi.fn() } }));
function sample(id = "g", revision = 7) {
  return {nonGoals:[],milestone:{id:"",confirmed:false},milestoneName:"",syncError:"",reason:"",stopping:false,verified:false,updatedAt:"2026-10-21T12:00:00Z",id, revision, project:"nori", title:"Team invitations",state:"running",outcome:"Invite a colleague",acceptance:["Recipient can accept"],actions:["pause","cancel"],members:[],tasks:[],parent:{id:"parent",confirmed:true,identifier:"NOR-42"},authorized:true,tokenLimit:100000,tokens:120,spendUSD:0,spendLimitUSD:10,runLimit:20,deadline:"2026-10-21T12:00:00Z",coverage:{runs:2,missing:1,partial:1,cost_unavailable:2}} satisfies Overview;
}
beforeEach(() => { api.GoalOverview.mockReset().mockResolvedValue([sample()]); api.GoalControl.mockReset().mockResolvedValue(undefined); api.GoalPublish.mockReset().mockResolvedValue(undefined); });
afterEach(cleanup);
async function open() { await fireEvent.click(screen.getByRole("button",{name:/Goals/})); await screen.findByRole("heading",{name:"Team invitations"}); }
describe("ProjectGoals", () => {
  it("loads only when opened and explains missing usage", async () => {
    render(ProjectGoals,{project:"nori"});
    expect(api.GoalOverview).not.toHaveBeenCalled();
    await open();
    expect(api.GoalOverview).toHaveBeenCalledTimes(1);
    expect(screen.getByText(/Missing usage is not zero/)).toBeInTheDocument();
    expect(screen.getByText(/120 \/ 100,000 tokens/)).toBeInTheDocument();
  });
  it("keeps the reviewed revision and exposes a stale-control rejection without retry",async () => {
    api.GoalControl.mockRejectedValue(new Error("goal changed; reload before steering"));
    render(ProjectGoals,{project:"nori"});await open();
    await fireEvent.click(screen.getByRole("button",{name:"Pause"}));
    expect(screen.getByText(/reviewed revision 7/)).toBeInTheDocument();
    expect(api.GoalControl).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Reason (optional)")).toHaveFocus();
    await fireEvent.input(screen.getByLabelText("Reason (optional)"),{target:{value:"Inspect API output"}});
    await fireEvent.click(screen.getByRole("button",{name:"Pause"}));
    await screen.findByRole("alert");
    expect(api.GoalControl).toHaveBeenCalledExactlyOnceWith("g",7,"pause","Inspect API output");
    expect(screen.getByLabelText("Reason (optional)")).toHaveValue("Inspect API output");
    expect(screen.getByRole("button",{name:"Refresh"})).toBeDisabled();
  });
  it("publishes only after reviewing the current revision and holds an uncertain result",async()=>{
    api.GoalOverview.mockResolvedValue([{...sample(),state:"draft",authorized:false,actions:[]}]);
    api.GoalPublish.mockRejectedValue(new Error("response lost"));
    render(ProjectGoals,{project:"nori"});await open();
    await fireEvent.click(screen.getByRole("button",{name:"Publish to Linear"}));
    expect(api.GoalPublish).not.toHaveBeenCalled();
    expect(screen.getByText(/does not authorize execution/)).toBeInTheDocument();
    expect(screen.queryByLabelText("Reason (optional)")).not.toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button",{name:"Publish to Linear"}));
    await screen.findByRole("alert");
    expect(api.GoalPublish).toHaveBeenCalledExactlyOnceWith("g",7);
    expect(api.GoalControl).not.toHaveBeenCalled();
    expect(screen.getByRole("button",{name:"Publish to Linear"})).toBeDisabled();
  });
  it("drops a late response after switching projects",async () => {
    let resolve!: (v: Overview[]) => void;
    api.GoalOverview.mockImplementationOnce(() => new Promise<Overview[]>(r=>resolve=r)).mockResolvedValueOnce([]);
    const result=render(ProjectGoals,{project:"nori"});
    await fireEvent.click(screen.getByRole("button",{name:/Goals/}));
    await result.rerender({project:"other"});
    await screen.findByText(/No goals in this project yet/);
    resolve([sample()]);
    await waitFor(()=>expect(api.GoalOverview).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole("heading",{name:"Team invitations"})).not.toBeInTheDocument();
  });
  it("retains the selected goal when hidden or after switching projects", async () => {
    api.GoalOverview.mockImplementation(async (project: string) => project === "nori" ? [sample(), {...sample("second"),title:"Invite permissions"}] : []);
    const result=render(ProjectGoals,{project:"nori"});await open();
    await fireEvent.change(screen.getByRole("combobox",{name:"Goal"}),{target:{value:"second"}});
    await screen.findByRole("heading",{name:"Invite permissions"});
    await fireEvent.click(screen.getByRole("button",{name:/Goals/}));
    await fireEvent.click(screen.getByRole("button",{name:/Goals/}));
    await screen.findByRole("heading",{name:"Invite permissions"});
    await result.rerender({project:"other"});
    await screen.findByText(/No goals in this project yet/);
    await result.rerender({project:"nori"});
    await screen.findByRole("heading",{name:"Invite permissions"});
  });
  it("shows failed reads and can refresh without inventing an empty result",async () => {
    api.GoalOverview.mockRejectedValueOnce(new Error("daemon down"));
    render(ProjectGoals,{project:"nori"});
    await fireEvent.click(screen.getByRole("button",{name:/Goals/}));
    await screen.findByRole("alert");
    expect(screen.queryByText(/No goals in this project yet/)).not.toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button",{name:"Refresh"}));
    await screen.findByRole("heading",{name:"Team invitations"});
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
