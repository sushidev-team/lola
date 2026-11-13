import { describe, it, expect } from "vitest";
import { deriveSide, sideHasContent, NOTES_SHOWN } from "./sessionside";

const el = (id: string, element: Record<string, unknown>) => ({ id, kind: "element", element: { id, ...element } }) as never;

describe("deriveSide", () => {
  it("has nothing for a session that used nothing", () => {
    const side = deriveSide([], undefined);
    expect(sideHasContent(side, undefined)).toBe(false);
    expect(sideHasContent(side, {} as never)).toBe(false);
  });
  it("a board's note or todos count", () => {
    const side = deriveSide([], undefined);
    expect(sideHasContent(side, { note: "waiting" } as never)).toBe(true);
    expect(sideHasContent(side, { todos: [{ text: "a" }] } as never)).toBe(true);
  });
  it("orders findings by severity and notes newest first, and caps the notes", () => {
    const items = [
      el("f1", { kind: "finding", title: "low", severity: "low" }),
      el("f2", { kind: "finding", title: "bad", severity: "critical" }),
      ...Array.from({ length: NOTES_SHOWN + 2 }, (_, i) => el(`n${i}`, { kind: "narration", title: `note ${i}` })),
    ];
    const side = deriveSide(items, undefined);
    expect(side.findings.map((f) => f.id)).toEqual(["f2", "f1"]);
    expect(side.notes).toHaveLength(NOTES_SHOWN);
    expect(side.notes[0].id).toBe(`n${NOTES_SHOWN + 1}`);
  });
  it("a board's plan wins over a posted one", () => {
    const items = [el("p", { kind: "plan", steps: [{ text: "s", state: "active" }] })];
    expect(deriveSide(items, undefined).plan?.id).toBe("p");
    expect(deriveSide(items, { todos: [{ text: "t" }] } as never).plan).toBeUndefined();
  });
});
