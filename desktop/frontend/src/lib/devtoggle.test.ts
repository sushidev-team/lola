import { describe, expect, it } from "vitest";
import { devToggleTitle } from "./devtoggle";

describe("devToggleTitle", () => {
  it("warns about the move for a single-active project", () => {
    expect(devToggleTitle({ devCommands: ["composer dev"], devActive: false })).toBe(
      "run composer dev here (stops them in any other session of this project)",
    );
  });

  it("names the leased ports and leaves other sessions alone when multi-active", () => {
    const t = devToggleTitle({ devCommands: ["composer dev"], devActive: false, devParallel: true, ports: [4100, 4101] });
    expect(t).toBe("run composer dev here on ports 4100, 4101 (other sessions keep theirs)");
    expect(t).not.toContain("stops them");
  });

  it("offers to stop an active stack", () => {
    expect(devToggleTitle({ devCommands: ["npm run dev"], devActive: true, ports: [4100] })).toBe(
      "stop npm run dev on port 4100",
    );
  });
});
