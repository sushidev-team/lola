import { describe, expect, it } from "vitest";
import { renderResync, renderResyncBase64, utf8ToBase64 } from "./screen";
import { resyncToBytes } from "@mobile/lib/resync";
import { base64ToBytes, type ResyncPayload } from "../wire";

const base: ResyncPayload = { cols: 80, rows: 24, cursorX: 0, cursorY: 0 };

// This file used to test a SECOND, independent renderer that lived here. It now
// tests the shim's use of the one in `@mobile/lib/resync`: the delegation, the
// base64 encoding the `pty:<name>` event needs, and the properties the reuse
// path depends on. The escape sequence's own rules are covered in
// lib/resync.test.ts, which is now the single place they are stated.

describe("the shim's resync republisher", () => {
  it("renders through the one renderer, byte for byte", () => {
    // The property that matters most here, because its absence is invisible:
    // two implementations of this that merely AGREE will drift the first time
    // one of them is corrected. There is one, and this pins it.
    const screens: ResyncPayload[] = [
      base,
      { ...base, lines: ["one", "two", "three"] },
      { ...base, altScreen: true, lines: ["\x1b[31mred", "plain"], cursorX: 4, cursorY: 2 },
      { ...base, cursorHidden: true },
      { ...base, rows: 2, lines: ["a", "b", "c", "d"] },
    ];
    for (const s of screens) expect(renderResync(s)).toBe(resyncToBytes(s));
  });

  it("keeps the properties LiveTerminal.svelte depends on", () => {
    // The component knows nothing about resync frames: it decodes base64 and
    // calls term.write. So the repaint has to be self-contained — alternate
    // screen, auto-wrap off then on, absolute row placement, an explicit cursor.
    const out = renderResync({ ...base, altScreen: true, lines: ["one", "two"] });
    expect(out).toContain("\x1b[?1049h");
    expect(out.indexOf("\x1b[?7l")).toBeGreaterThanOrEqual(0);
    expect(out.indexOf("\x1b[?7h")).toBeGreaterThan(out.indexOf("\x1b[?7l"));
    expect(out).toContain("\x1b[1;1H");
    expect(out).toContain("\x1b[2;1H");
    expect(out).not.toContain("\r\n");
    expect(out).toContain("\x1b[?25h");
  });

  it("round-trips non-ASCII pane content through base64", () => {
    // btoa throws above U+00FF, and an agent prints box drawing and emoji.
    const screen = { ...base, lines: ["✻ Harmonizing… (5m 58s)"] };
    const decoded = new TextDecoder().decode(base64ToBytes(renderResyncBase64(screen)));
    expect(decoded).toBe(renderResync(screen));
    expect(utf8ToBase64("✻")).toBe("4py7");
  });
});
