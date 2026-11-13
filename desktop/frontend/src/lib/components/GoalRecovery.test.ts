import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import GoalRecovery from "./GoalRecovery.svelte";
import type { RecoveryView } from "@bindings/internal/goal";

const api=vi.hoisted(()=>({GoalRecovery:vi.fn(),GoalSyncBrief:vi.fn(),GoalAcceptBrief:vi.fn(),GoalDismissProgress:vi.fn(),GoalDeliveryEvidence:vi.fn(),GoalControl:vi.fn()}));
vi.mock("@bindings/desktop",()=>({DaemonService:api}));
const oldScroll=Element.prototype.scrollIntoView;
function snapshot(): RecoveryView {
  const brief={title:"Invitations",outcome:"Invite a colleague",acceptance:["Recipient can accept"],non_goals:["Billing changes"]};
  return {goal:{id:"g",project:"nori",revision:7,title:brief.title,state:"blocked",syncError:"",reason:"Verification interrupted",tasks:[{id:"t",title:"Invitation flow",acceptance:["Keyboard access works"]}]} as RecoveryView["goal"],brief,
    pendingBrief:{id:"source-1",title:"Clarified invitation flow",description:"Human clarification: preserve keyboard access. <script>ignored</script>",observed_at:"2026-10-21T12:00:00Z"},
    latestProgress:{id:"update-1",summary:"Held after the Linear brief changed",created_at:"2026-10-21T12:00:00Z",sent_at:"2026-10-21T12:01:00Z"},progressError:"Send was not confirmed",deliveryEvidenceUrl:"",actions:["sync-brief","accept-brief","dismiss-progress","delivery-evidence","retry-verification","retry-replan"]};
}
beforeEach(()=>{for(const mock of Object.values(api))mock.mockReset().mockResolvedValue(undefined);api.GoalRecovery.mockImplementation(async()=>snapshot());Element.prototype.scrollIntoView=vi.fn();});
afterEach(()=>{cleanup();Element.prototype.scrollIntoView=oldScroll;});
async function open(){await fireEvent.click(screen.getByRole("button",{name:"Recovery and Linear"}));await screen.findByRole("group",{name:"Recovery snapshot"});}
async function choose(name: string){await fireEvent.click(screen.getByRole("button",{name}));await screen.findByRole("group",{name:"Edit recovery decision"});}
async function review(){await fireEvent.click(screen.getByRole("button",{name:"Review recovery decision"}));await screen.findByRole("group",{name:"Review recovery decision"});}

describe("GoalRecovery",()=>{
  it("inspects locally and refreshes Linear only after a separate review",async()=>{
    render(GoalRecovery,{goalID:"g",project:"nori"});expect(api.GoalRecovery).not.toHaveBeenCalled();await open();
    expect(api.GoalRecovery).toHaveBeenCalledExactlyOnceWith("g");expect(api.GoalSyncBrief).not.toHaveBeenCalled();
    await choose("Refresh Linear brief");expect(api.GoalSyncBrief).not.toHaveBeenCalled();await review();
    expect(screen.getByRole("group",{name:"Review recovery decision"})).toHaveFocus();
    await fireEvent.click(screen.getByRole("button",{name:"Confirm refresh linear brief"}));
    await waitFor(()=>expect(api.GoalRecovery).toHaveBeenCalledTimes(2));expect(api.GoalSyncBrief).toHaveBeenCalledExactlyOnceWith("g");
    expect(api.GoalAcceptBrief).not.toHaveBeenCalled();expect(api.GoalControl).not.toHaveBeenCalled();
  });
  it("freezes the exact brief and source, locks uncertainty, and reloads the saved decision without replay",async()=>{
    const source=snapshot();api.GoalRecovery.mockResolvedValue(source);api.GoalAcceptBrief.mockRejectedValue(Error("reply lost"));
    render(GoalRecovery,{goalID:"g",project:"nori"});await open();await choose("Reconcile brief");
    expect(document.querySelector("script")).toBeNull();
    await fireEvent.input(screen.getByLabelText("Clarified outcome"),{target:{value:"Invite a colleague with keyboard access"}});
    await fireEvent.input(screen.getByLabelText("Clarified acceptance criteria"),{target:{value:" Recipient can accept \n Keyboard flow works "}});
    await fireEvent.input(screen.getByLabelText("Decision reason (required)"),{target:{value:"   "}});
    await fireEvent.click(screen.getByRole("button",{name:"Review recovery decision"}));
    expect(await screen.findByRole("alert")).toHaveFocus();expect(api.GoalAcceptBrief).not.toHaveBeenCalled();
    await fireEvent.input(screen.getByLabelText("Decision reason (required)"),{target:{value:"Existing invitation task covers this clarification"}});await review();
    source.goal.revision=99;source.pendingBrief!.id="changed-source";source.brief.outcome="changed externally";
    await fireEvent.click(screen.getByRole("button",{name:"Confirm reconcile brief"}));await screen.findByRole("alert");
    expect(api.GoalAcceptBrief).toHaveBeenCalledExactlyOnceWith({id:"g",revision:7,source_id:"source-1",reason:"Existing invitation task covers this clarification",contract:{title:"Invitations",outcome:"Invite a colleague with keyboard access",acceptance:["Recipient can accept","Keyboard flow works"],non_goals:["Billing changes"]}});
    expect(screen.getByRole("button",{name:"Confirm reconcile brief"})).toBeDisabled();expect(screen.getByRole("button",{name:"Back to editing"})).toBeDisabled();
    api.GoalRecovery.mockRejectedValueOnce(Error("offline"));await fireEvent.click(screen.getByRole("button",{name:"Reload local snapshot"}));await screen.findByText(/Could not load recovery/);
    expect(screen.getByRole("button",{name:"Confirm reconcile brief"})).toBeDisabled();expect(screen.getByText("Invite a colleague with keyboard access")).toBeInTheDocument();
    const saved=snapshot();saved.goal.revision=8;saved.pendingBrief=null;saved.actions=[];saved.latestBrief={sourceId:"source-1",before:saved.brief,after:{...saved.brief,outcome:"Invite a colleague with keyboard access"},reason:"Existing task coverage checked",acceptedAt:"2026-10-21T13:00:00Z"};
    api.GoalRecovery.mockResolvedValue(saved);await fireEvent.click(screen.getByRole("button",{name:"Reload local snapshot"}));await screen.findByText("Latest accepted brief decision");
    expect(api.GoalAcceptBrief).toHaveBeenCalledTimes(1);expect(screen.queryByRole("button",{name:"Confirm reconcile brief"})).not.toBeInTheDocument();
  });
  it("requires a reason to dismiss the reviewed progress update and shows its retained resolution",async()=>{
    render(GoalRecovery,{goalID:"g",project:"nori"});await open();await choose("Dismiss progress update");
    await fireEvent.input(screen.getByLabelText("Decision reason (required)"),{target:{value:"   "}});
    await fireEvent.click(screen.getByRole("button",{name:"Review recovery decision"}));await screen.findByRole("alert");
    await fireEvent.input(screen.getByLabelText("Decision reason (required)"),{target:{value:"Checked Linear; keep this unknown result"}});await review();
    const saved=snapshot();saved.goal.revision=8;saved.actions=[];saved.latestProgress!.dismissed_at="2026-10-21T13:00:00Z";saved.latestProgress!.resolution="Checked Linear; keep this unknown result";api.GoalRecovery.mockResolvedValue(saved);
    await fireEvent.click(screen.getByRole("button",{name:"Confirm dismiss progress update"}));await screen.findByText("Dismissed; remote outcome not asserted");
    expect(api.GoalDismissProgress).toHaveBeenCalledExactlyOnceWith({id:"g",revision:7,update_id:"update-1",reason:"Checked Linear; keep this unknown result"});
    expect(screen.getByText(/Resolution: Checked Linear/)).toBeInTheDocument();
  });
  it.each([["Retry verification","retry-verification"],["Retry replanning","retry-replan"]])("reviews %s before charging another call",async(label,action)=>{
    render(GoalRecovery,{goalID:"g",project:"nori"});await open();await choose(label);expect(api.GoalControl).not.toHaveBeenCalled();
    await fireEvent.input(screen.getByLabelText("Reason (optional)"),{target:{value:"Resolved the interrupted runner"}});await review();
    await fireEvent.click(screen.getByRole("button",{name:`Confirm ${label.toLowerCase()}`}));await waitFor(()=>expect(api.GoalControl).toHaveBeenCalledExactlyOnceWith("g",7,action,"Resolved the interrupted runner"));
  });
  it("validates shared evidence, freezes its URL, and retains it in the refreshed state",async()=>{
    render(GoalRecovery,{goalID:"g",project:"nori"});await open();await choose("Attach evidence URL");
    await fireEvent.input(screen.getByLabelText("Shared evidence URL"),{target:{value:"https://user:secret@example.com/artifact"}});
    await fireEvent.click(screen.getByRole("button",{name:"Review recovery decision"}));await screen.findByText(/Use a shared HTTPS artifact URL/);expect(api.GoalDeliveryEvidence).not.toHaveBeenCalled();
    await fireEvent.input(screen.getByLabelText("Shared evidence URL"),{target:{value:"https://artifacts.example/invitations"}});await review();
    const saved=snapshot();saved.goal.revision=8;saved.deliveryEvidenceUrl="https://artifacts.example/invitations";api.GoalRecovery.mockResolvedValue(saved);
    await fireEvent.click(screen.getByRole("button",{name:"Confirm attach evidence url"}));await screen.findByText("Shared delivery evidence");
    expect(api.GoalDeliveryEvidence).toHaveBeenCalledExactlyOnceWith({id:"g",revision:7,url:"https://artifacts.example/invitations"});
  });
  it("rejects a different goal snapshot and requires explicit draft discard",async()=>{
    api.GoalRecovery.mockResolvedValueOnce({...snapshot(),goal:{...snapshot().goal,id:"other"}});
    const closed=vi.fn();render(GoalRecovery,{goalID:"g",project:"nori",onclose:closed});await fireEvent.click(screen.getByRole("button",{name:"Recovery and Linear"}));await screen.findByRole("alert");
    expect(screen.queryByRole("button",{name:"Reconcile brief"})).not.toBeInTheDocument();await fireEvent.click(screen.getByRole("button",{name:"Reload local snapshot"}));await screen.findByRole("group",{name:"Recovery snapshot"});await choose("Reconcile brief");
    await fireEvent.input(screen.getByLabelText("Clarified title"),{target:{value:"Unsaved"}});await fireEvent.click(screen.getByRole("button",{name:"Close recovery"}));expect(closed).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole("button",{name:"Keep editing"}));expect(screen.getByLabelText("Clarified title")).toHaveValue("Unsaved");
    await fireEvent.click(screen.getByRole("button",{name:"Close recovery"}));await fireEvent.click(screen.getByRole("button",{name:"Discard draft"}));expect(closed).toHaveBeenCalledOnce();expect(api.GoalAcceptBrief).not.toHaveBeenCalled();
  });
});
