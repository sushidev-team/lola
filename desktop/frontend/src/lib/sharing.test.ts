import { describe, expect, it } from "vitest";
import {
  attachBlock,
  attachedLine,
  audienceChoices,
  groupOffers,
  initials,
  isShareStream,
  levelOf,
  sameFingerprint,
  shareStreamName,
  shareTitle,
} from "./sharing";

const offer = (over: Record<string, unknown> = {}) =>
  ({ id: "a".repeat(32), title: "api", level: "view", ownerDevice: "d1", ownerName: "mini", online: true, trusted: true, ...over }) as never;

describe("levelOf", () => {
  it("only an explicit type is type", () => {
    expect(levelOf("type")).toBe("type");
    expect(levelOf("view")).toBe("view");
    expect(levelOf("root")).toBe("view");
    expect(levelOf(undefined)).toBe("view");
  });
});

describe("attachedLine", () => {
  it("says who is attached in a few words", () => {
    expect(attachedLine([])).toBe("");
    expect(attachedLine(null)).toBe("");
    expect(attachedLine([{ name: "Ada" }])).toBe("Ada");
    expect(attachedLine([{ name: "Ada" }, { name: "Bob" }])).toBe("Ada and Bob");
    expect(attachedLine([{ name: "Ada" }, { name: "Bob" }, { name: "Cy" }, { name: "Di" }])).toBe("Ada, Bob and 2 more");
    expect(attachedLine([{ name: " " }])).toBe("");
  });
});

describe("initials", () => {
  it("takes the person, not the machine", () => {
    expect(initials("Ada · office mini")).toBe("A");
    expect(initials("Bob Stone")).toBe("BS");
    expect(initials("")).toBe("?");
  });
});

describe("sameFingerprint", () => {
  it("ignores spacing and case but never matches empty", () => {
    expect(sameFingerprint("00ff 11AA", "00FF11aa")).toBe(true);
    expect(sameFingerprint("00ff", "00fe")).toBe(false);
    expect(sameFingerprint("", "")).toBe(false);
  });
});

describe("groupOffers", () => {
  it("groups by machine and lists online machines first", () => {
    const groups = groupOffers([
      offer({ ownerDevice: "d2", ownerName: "laptop", online: false }),
      offer({ ownerDevice: "d1", ownerName: "mini", ownerMember: "Ada" }),
      offer({ ownerDevice: "d1", ownerName: "mini", ownerMember: "Ada", id: "b".repeat(32) }),
    ]);
    expect(groups.map((g) => g.ownerName)).toEqual(["Ada · mini", "laptop"]);
    expect(groups[0].offers).toHaveLength(2);
    expect(groups[1].online).toBe(false);
    expect(groupOffers(null)).toEqual([]);
  });
});

describe("attachBlock", () => {
  it("names the first thing in the way", () => {
    expect(attachBlock({ online: true, trusted: true, ownerName: "mini" })).toBe("");
    expect(attachBlock({ online: false, trusted: true, ownerName: "mini" })).toBe("mini is offline");
    expect(attachBlock({ online: false, trusted: false, ownerName: "mini" })).toBe("Trust mini first");
  });
});

describe("share streams", () => {
  it("matches the name TermService routes to the remote backend", () => {
    expect(shareStreamName("abc")).toBe("share-abc");
    expect(isShareStream("share-abc")).toBe(true);
    expect(isShareStream("lola-eng-1")).toBe(false);
  });
});

describe("audienceChoices", () => {
  it("offers only the teammates this Mac allowed", () => {
    const m = (id: string, allowed: boolean) => ({ deviceId: id, name: id, fingerprint: "", allowed, trusted: false }) as never;
    expect(audienceChoices([m("a", true), m("b", false)]).map((x) => x.deviceId)).toEqual(["a"]);
    expect(audienceChoices(undefined)).toEqual([]);
  });
});

describe("shareTitle", () => {
  it("reads as a tooltip", () => {
    expect(shareTitle(false, 0)).toBe("Share with team");
    expect(shareTitle(true, 0)).toBe("Shared with your team");
    expect(shareTitle(true, 2)).toBe("Shared with your team · 2 attached");
  });
});
