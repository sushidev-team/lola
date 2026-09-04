import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import DevLinksSheet from "./DevLinksSheet.svelte";

// The dev-server links, as a phone sees them.
//
// The addresses here are the daemon's FORWARDS — the session's loopback servers
// republished on a private interface — not the 127.0.0.1 addresses the Mac
// sees, which on a phone are the phone's own loopback and reach nothing.

const forwards = [
  { url: "http://192.168.20.3:39273", from: "127.0.0.1:8000" },
  { url: "http://192.168.20.3:41005", from: "127.0.0.1:5175" },
];

describe("DevLinksSheet", () => {
  it("is a dialog named for what it holds", () => {
    render(DevLinksSheet, {
      props: { forwards, onopen: () => {}, onclose: () => {} },
    });
    expect(
      screen.getByRole("dialog", { name: "Dev server links" }),
    ).toBeTruthy();
  });

  it("says which machine the link opens on", () => {
    // The same gesture on the desktop opens a link on the MAC. Confusing the
    // two launches a browser on an unattended desktop in another room.
    render(DevLinksSheet, {
      props: { forwards, onopen: () => {}, onclose: () => {} },
    });
    expect(screen.getByText(/Opens on THIS phone/i)).toBeTruthy();
  });

  it("labels each row by the ORIGINAL address, which is what names the thing", () => {
    render(DevLinksSheet, {
      props: { forwards, onopen: () => {}, onclose: () => {} },
    });
    // The forward's own port is kernel-allocated: 39273 identifies nothing and
    // changes on every restart. 8000 is the app and 5175 is the bundler.
    expect(screen.getByText("127.0.0.1:8000")).toBeTruthy();
    expect(screen.getByText("127.0.0.1:5175")).toBeTruthy();
    // And where it actually goes, for anyone checking WHICH machine it reaches.
    expect(screen.getByText(`via ${forwards[0].url}`)).toBeTruthy();
  });

  it("hands back the address that was chosen", async () => {
    const onopen = vi.fn();
    render(DevLinksSheet, { props: { forwards, onopen, onclose: () => {} } });
    await fireEvent.click(screen.getByText("127.0.0.1:5175"));
    expect(onopen).toHaveBeenCalledWith(forwards[1].url);
  });

  it("falls back to the forward when a record carries no original", () => {
    // Both fields come from the daemon and neither is empty in practice, but a
    // row that vanished would be worse than one labelled oddly.
    render(DevLinksSheet, {
      props: {
        forwards: [{ url: "http://192.168.20.3:1", from: "" }],
        onopen: () => {},
        onclose: () => {},
      },
    });
    expect(screen.getAllByText(/192\.168\.20\.3:1/).length).toBeGreaterThan(0);
  });
});
