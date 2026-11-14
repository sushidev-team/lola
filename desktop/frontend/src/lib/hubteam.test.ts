import { describe, expect, it } from "vitest";
import { digestWebhookError, inviteLabelError, linearKeyError, parseCreateCode, slugTeamName, statusClassText, teamErrorText, teamNameError } from "./hubteam";

const CODE = "C".repeat(32);

describe("parseCreateCode", () => {
  it("accepts a create code and names its hub", () => {
    expect(parseCreateCode(` lola-hub-create:https://hub.example.com/#${CODE}\n`)).toEqual({ ok: true, url: "https://hub.example.com" });
    expect(parseCreateCode(`lola-hub-create:http://127.0.0.1:8099#${CODE}`)).toEqual({ ok: true, url: "http://127.0.0.1:8099" });
  });

  it("refuses everything else without echoing it", () => {
    for (const bad of [
      "",
      CODE,
      `lola-hub:https://hub.example.com#${CODE}`,
      "lola-hub-create:https://hub.example.com",
      `lola-hub-create:http://hub.example.com#${CODE}`,
      `lola-hub-create:https://u:p@hub.example.com#${CODE}`,
      "lola-hub-create:https://hub.example.com#short",
      `lola-hub-create:https://hub.example.com#${CODE} x`,
    ]) {
      const r = parseCreateCode(bad);
      expect(r.ok, bad).toBe(false);
      if (!r.ok && bad) expect(r.error).not.toContain(CODE);
    }
  });

  it("says an invite belongs in Join", () => {
    const r = parseCreateCode(`lola-hub:https://hub.example.com#${CODE}`);
    expect(!r.ok && r.error).toMatch(/Join a team/);
  });
});

describe("team name", () => {
  it("slugs what a person types", () => {
    expect(slugTeamName("Acme Inc")).toBe("acme-inc");
    expect(slugTeamName("  Web Team")).toBe("web-team");
  });
  it("checks the hub's shape", () => {
    expect(teamNameError("acme")).toBe("");
    expect(teamNameError("")).toBe("");
    expect(teamNameError("-acme")).not.toBe("");
    expect(teamNameError("Acme")).not.toBe("");
  });
});

describe("secrets", () => {
  it("accepts only a Linear key", () => {
    expect(linearKeyError("lin_api_" + "a".repeat(40))).toBe("");
    expect(linearKeyError("")).toBe("");
    const msg = linearKeyError("hunter2");
    expect(msg).not.toBe("");
    expect(msg).not.toContain("hunter2");
  });
  it("accepts only a Slack webhook", () => {
    expect(digestWebhookError("https://hooks.slack.com/services/T0001/B0001/abcDEF123")).toBe("");
    for (const bad of [
      "http://hooks.slack.com/services/T0001/B0001/abc",
      "https://169.254.169.254/latest/meta-data",
      "https://hooks.slack.com.evil.example/services/T0001/B0001/abc",
      "https://hooks.slack.com:8443/services/T0001/B0001/abc",
      "https://hooks.slack.com/services/T0001/B0001/abc?x=1",
    ]) {
      expect(digestWebhookError(bad), bad).not.toBe("");
    }
  });
  it("checks an invite label", () => {
    expect(inviteLabelError("Ada Lovelace")).toBe("");
    expect(inviteLabelError("a\nb")).not.toBe("");
    expect(inviteLabelError("x".repeat(41))).not.toBe("");
  });
});

describe("words", () => {
  it("explains known error classes and passes unknown ones through", () => {
    expect(statusClassText("")).toBe("");
    expect(statusClassText("linear: unauthorized")).toMatch(/no longer accepts/);
    expect(statusClassText("linear: odd")).toContain("linear: odd");
  });
  it("turns daemon errors into something to act on", () => {
    expect(teamErrorText("Error: hub: HTTP 409 conflict: refused")).toMatch(/taken/);
    expect(teamErrorText("hub: Linear refused that key; nothing was changed")).toMatch(/did not accept that key/);
    expect(teamErrorText("hub: this machine already holds a team admin key (team a at https://h); creating a team would discard it")).toMatch(/another team/);
    expect(teamErrorText("something else")).toBe("something else");
  });
});
