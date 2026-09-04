import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import DevLinksSheet from "./DevLinksSheet.svelte";

// The dev-server links, as a phone sees them.
//
// The addresses here are the daemon's FORWARDS — the session's loopback servers
// republished on a private interface — not the 127.0.0.1 addresses the Mac
// sees, which on a phone are the phone's own loopback and reach nothing.

const urls = ["http://192.168.20.3:39273", "http://192.168.20.3:41005"];

describe("DevLinksSheet", () => {
  it("is a dialog named for what it holds", () => {
    render(DevLinksSheet, {
      props: { urls, onopen: () => {}, onclose: () => {} },
    });
    expect(
      screen.getByRole("dialog", { name: "Dev server links" }),
    ).toBeTruthy();
  });

  it("says which machine the link opens on", () => {
    // The same gesture on the desktop opens a link on the MAC. Confusing the
    // two launches a browser on an unattended desktop in another room.
    render(DevLinksSheet, {
      props: { urls, onopen: () => {}, onclose: () => {} },
    });
    expect(screen.getByText(/Opens on THIS phone/i)).toBeTruthy();
  });

  it("labels each row by PORT, which is what tells the app from the bundler", () => {
    render(DevLinksSheet, {
      props: { urls, onopen: () => {}, onclose: () => {} },
    });
    expect(screen.getByText("Port 39273")).toBeTruthy();
    expect(screen.getByText("Port 41005")).toBeTruthy();
    // The full address is still shown: a person checking WHICH machine this is
    // has nothing else to read it from.
    expect(screen.getByText(urls[0])).toBeTruthy();
  });

  it("hands back the address that was chosen", async () => {
    const onopen = vi.fn();
    render(DevLinksSheet, { props: { urls, onopen, onclose: () => {} } });
    await fireEvent.click(screen.getByText("Port 41005"));
    expect(onopen).toHaveBeenCalledWith(urls[1]);
  });

  it("renders an address it cannot parse rather than dropping it", () => {
    // These strings come from the daemon, which builds them from a listener's
    // own address — but a row that vanished would be worse than an ugly one.
    render(DevLinksSheet, {
      props: { urls: ["not a url"], onopen: () => {}, onclose: () => {} },
    });
    expect(screen.getAllByText("not a url").length).toBeGreaterThan(0);
  });
});
