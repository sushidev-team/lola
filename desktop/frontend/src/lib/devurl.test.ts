import { describe, it, expect } from "vitest";
import { devUrlLabel } from "./devurl";

describe("devUrlLabel", () => {
  it("drops the scheme and a trailing slash", () => {
    expect(devUrlLabel("http://127.0.0.1:8001")).toBe("127.0.0.1:8001");
    expect(devUrlLabel("https://nori-app.test/")).toBe("nori-app.test");
    expect(devUrlLabel("http://localhost:5175/")).toBe("localhost:5175");
  });

  // The chip has to match what the pane printed or it stops being verifiable at
  // a glance — so the host is never "helpfully" rewritten, and a path is part of
  // the address the tool advertised.
  it("keeps the host and the path exactly", () => {
    expect(devUrlLabel("http://127.0.0.1:8000/admin")).toBe("127.0.0.1:8000/admin");
  });

  it("never renders an empty chip", () => {
    expect(devUrlLabel("http://")).toBe("http://");
  });
});
