import {describe,it,expect} from "vitest";
import {progressState,recoveryProblem} from "./goalrecovery";

describe("recovery validation",()=>{
  const contract={title:"Clarification",outcome:"Existing scope",acceptance:["Observable acceptance"],non_goals:[]};
  it("checks UTF-8 and criterion bounds before freezing a decision",()=>{
    expect(recoveryProblem("accept-brief"," ",contract,"")).toMatch(/reason|Describe/);
    expect(recoveryProblem("accept-brief","界".repeat(667),contract,"")).toMatch(/2000 bytes/);
    expect(recoveryProblem("accept-brief","Checked coverage",{...contract,title:"界".repeat(81)},"")).toMatch(/240 bytes/);
    expect(recoveryProblem("accept-brief","Checked coverage",{...contract,acceptance:[]},"")).toMatch(/1–50/);
    expect(recoveryProblem("accept-brief","Checked coverage",{...contract,acceptance:["<!-- lola-goal:reserved -->"]},"")).toMatch(/reserved/);
    expect(recoveryProblem("accept-brief","Checked coverage",contract,"")).toBe("");
  });
  it("does not turn zero timestamps or dismissed updates into delivery proof",()=>{
    const update={id:"u",summary:"Pending",created_at:"2026-10-21T12:00:00Z",confirmed_at:"0001-01-01T00:00:00Z"};
    expect(progressState(update)).toBe("Pending; no send recorded");
    expect(progressState({...update,sent_at:"2026-10-21T12:01:00Z"})).toMatch(/unconfirmed/);
    expect(progressState({...update,confirmed_at:"2026-10-21T12:02:00Z"})).toBe("Confirmed in Linear");
    expect(progressState({...update,dismissed_at:"2026-10-21T12:02:00Z"})).toMatch(/remote outcome not asserted/);
  });
});
