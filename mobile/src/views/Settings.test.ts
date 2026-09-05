import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import Settings from "./Settings.svelte";
import { appearance, DEFAULT_THEME_ID, THEME_IDS } from "$lib/theme-runtime.svelte";
import { connection } from "@mobile/lib/connection.svelte";
import { nav } from "@mobile/lib/nav.svelte";

// The settings screen: the connection facts, the two ways to leave a Mac, and
// the flavor.
//
// THE FLAVOR IS THIS PHONE'S. `appearance.commit()` writes `[ui].theme` in
// config.toml on the Mac, which this app must never do — and could not anyway,
// since the shim leaves `ConfigService.SetTheme` absent on purpose. So the
// picker paints and caches locally, and the test that matters most is the one
// asserting nothing tried to reach the daemon.

beforeEach(() => {
  globalThis.localStorage?.clear();
  connection.host = "192.168.10.160";
  connection.port = 7717;
  connection.hasStoredKey = true;
  connection.nameEpoch++;
  appearance.id = DEFAULT_THEME_ID;
  nav.screen = "sessions";
  nav.tab = "settings";
});

describe("Settings — connection", () => {
  it("names the Mac it is attached to, and the address it reached it on", () => {
    render(Settings);
    // The two answer different questions: the name says which machine, the
    // address says over which network — and discovery means the same Mac
    // answers on a different one at home and at the office.
    expect(screen.getByText(/Connected to 192\.168\.10\.160/)).toBeTruthy();
    expect(screen.getByText("192.168.10.160:7717")).toBeTruthy();
  });

  it("names the Mac in the button that leaves it", () => {
    render(Settings);
    // "Disconnect" alone was the original bug in the sessions header: a control
    // whose subject the user has to infer is ambiguous wherever it is drawn.
    expect(
      screen.getByRole("button", { name: /Disconnect from 192\.168\.10\.160/ }),
    ).toBeTruthy();
  });

  // THE NICKNAME, which arrived here when the sessions header's Mac sheet was
  // removed. It was the one thing that sheet held and this screen did not, so
  // these are the assertions its own test file used to make; the sheet is gone
  // and there is now exactly one field in the app that writes a machine name.
  it("offers a nickname field showing the daemon's name as the placeholder", () => {
    render(Settings);
    const field = screen.getByLabelText("Name for this Mac") as HTMLInputElement;
    // EMPTY, with the current name as the PLACEHOLDER. Seeding the value from
    // `connection.label` would put the hostname in the field as if a person had
    // typed it, and the next Return would freeze it as an override that can no
    // longer follow a rename on the Mac.
    expect(field.value).toBe("");
    expect(field.placeholder).toBe("192.168.10.160");
  });

  it("commits a nickname on Return, without a Save button", () => {
    // A form, so the keyboard's Return commits: a bare input in a phone screen
    // is one a person can only leave by dismissing the keyboard, and a separate
    // Save button read as two decisions rather than one.
    const rename = vi.spyOn(connection, "rename").mockImplementation(() => {});
    const { container } = render(Settings);
    const field = screen.getByLabelText("Name for this Mac") as HTMLInputElement;

    fireEvent.input(field, { target: { value: "mars" } });
    fireEvent.submit(container.querySelector("form")!);

    expect(rename).toHaveBeenCalledWith("mars");
    expect(screen.queryByRole("button", { name: /save/i })).toBeNull();
  });

  it("treats an empty field as the undo, not as a nameless Mac", () => {
    // Clearing it falls back to whatever the daemon reports for itself, which
    // is why the placeholder shows that name rather than a hint.
    const rename = vi.spyOn(connection, "rename").mockImplementation(() => {});
    const { container } = render(Settings);

    fireEvent.submit(container.querySelector("form")!);
    expect(rename).toHaveBeenCalledWith("");
  });

  it("disconnects without forgetting, and returns to the pairing screen", async () => {
    const disconnect = vi.spyOn(connection, "disconnect").mockResolvedValue();
    const forget = vi.spyOn(connection, "forget").mockResolvedValue();
    render(Settings);

    await fireEvent.click(screen.getByRole("button", { name: /^Disconnect from/ }));
    expect(disconnect).toHaveBeenCalled();
    expect(forget).not.toHaveBeenCalled();
    expect(nav.screen).toBe("connect");

    disconnect.mockRestore();
    forget.mockRestore();
  });

  it("forgets the key as well when asked, and forgets before it disconnects", async () => {
    // Disconnecting sets a process-local flag; the Keychain entry outlives the
    // process, so a disconnect alone came back to an authenticated list on the
    // next launch. Forgetting is what makes leaving durable.
    const order: string[] = [];
    const disconnect = vi
      .spyOn(connection, "disconnect")
      .mockImplementation(async () => void order.push("disconnect"));
    const forget = vi
      .spyOn(connection, "forget")
      .mockImplementation(async () => void order.push("forget"));
    render(Settings);

    await fireEvent.click(screen.getByRole("button", { name: "Forget this Mac" }));
    expect(order).toEqual(["forget", "disconnect"]);

    disconnect.mockRestore();
    forget.mockRestore();
  });

  it("offers nothing to forget when nothing is stored", () => {
    connection.hasStoredKey = false;
    render(Settings);
    expect(screen.queryByRole("button", { name: "Forget this Mac" })).toBeNull();
    expect(screen.getByText(/starts at the pairing screen/)).toBeTruthy();
  });
});

describe("Settings — appearance", () => {
  it("offers exactly the flavors Go knows about", () => {
    render(Settings);
    // THEME_IDS matches internal/config's UIThemes exactly — same order, same
    // spelling — so a flavor added there appears here with no edit, and one
    // this app invented would be rejected by `Validate` on the Mac.
    const pressed = screen
      .getAllByRole("button")
      .filter((b) => b.hasAttribute("aria-pressed"));
    expect(pressed).toHaveLength(THEME_IDS.length);
  });

  it("marks the live flavor, and only it", () => {
    render(Settings);
    const pressed = screen
      .getAllByRole("button")
      .filter((b) => b.getAttribute("aria-pressed") === "true");
    expect(pressed).toHaveLength(1);
    expect(pressed[0].textContent).toContain("Mocha");
  });

  it("applies a flavor and remembers it for the next launch", async () => {
    render(Settings);

    await fireEvent.click(screen.getByRole("button", { name: /Latte/ }));

    expect(appearance.id).toBe("catppuccin-latte");
    // The same key `appearance.init()` reads synchronously on boot, which is
    // what keeps a chosen flavor from flashing the default on every launch.
    expect(globalThis.localStorage?.getItem("lola.theme")).toBe("catppuccin-latte");
  });

  it("never asks the Mac to change its theme", async () => {
    // This app is read-only about the daemon. `commit` writes config.toml,
    // which would repaint the desktop app and the TUI from a phone.
    const commit = vi.spyOn(appearance, "commit");
    render(Settings);
    await fireEvent.click(screen.getByRole("button", { name: /Frapp/ }));
    expect(commit).not.toHaveBeenCalled();
    commit.mockRestore();
  });

  it("says where the flavor lives, so the Mac's is not assumed to have moved", () => {
    render(Settings);
    expect(screen.getByText(/stored on this phone/)).toBeTruthy();
  });
});
