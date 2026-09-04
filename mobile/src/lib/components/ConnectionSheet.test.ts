import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import ConnectionSheet from "./ConnectionSheet.svelte";
import { connection } from "@mobile/lib/connection.svelte";
import { daemonLabel, learnDaemonName } from "@mobile/lib/daemonname";

// The settings sheet behind the header's gear, and specifically the row that
// names the Mac.
//
// WHY THE NAME IS HERE AT ALL. Discovery means the same machine answers on a
// different address at home and at the office, so an address names a network
// rather than the thing somebody left work running on. The daemon reports its
// own hostname as the default; this row exists because a hostname is often
// neither chosen nor readable, and somebody with two Macs wants "work" and
// "home" rather than two variations on their own name.

const ID = "192.168.10.160:7717";

beforeEach(() => {
  globalThis.localStorage?.clear();
  connection.host = "192.168.10.160";
  connection.port = 7717;
  connection.hasStoredKey = true;
  connection.nameEpoch++;
});

describe("ConnectionSheet", () => {
  it("names the Mac by its address until something better is known", () => {
    render(ConnectionSheet, {
      props: { onleave: () => {}, onclose: () => {} },
    });
    expect(screen.getByText(/Connected to 192\.168\.10\.160/)).toBeTruthy();
  });

  it("prefers the name the daemon reported for itself", () => {
    learnDaemonName(ID, "marvin");
    connection.nameEpoch++;
    render(ConnectionSheet, {
      props: { onleave: () => {}, onclose: () => {} },
    });
    expect(screen.getByText(/Connected to marvin/)).toBeTruthy();
    // And the field offers that name rather than a hint about typing.
    expect(screen.getByLabelText(/Name for this Mac/i)).toHaveProperty(
      "placeholder",
      "marvin",
    );
  });

  it("renames it, and the rename is what every later sentence uses", async () => {
    learnDaemonName(ID, "Martins-MacBook-Pro");
    connection.nameEpoch++;
    render(ConnectionSheet, {
      props: { onleave: () => {}, onclose: () => {} },
    });

    const field = screen.getByLabelText(/Name for this Mac/i);
    await fireEvent.input(field, { target: { value: "work" } });
    await fireEvent.submit(field.closest("form")!);

    expect(daemonLabel(ID, "192.168.10.160")).toBe("work");
    expect(screen.getByText(/Disconnect from work/)).toBeTruthy();
  });

  it("clearing the field is the UNDO, falling back to the daemon's own name", async () => {
    learnDaemonName(ID, "marvin");
    connection.rename("work");
    render(ConnectionSheet, {
      props: { onleave: () => {}, onclose: () => {} },
    });

    const field = screen.getByLabelText(/Name for this Mac/i);
    await fireEvent.input(field, { target: { value: "  " } });
    await fireEvent.submit(field.closest("form")!);

    expect(connection.renamed).toBe(false);
    expect(daemonLabel(ID, "192.168.10.160")).toBe("marvin");
  });

  it("still names what the destructive button leaves", () => {
    const onleave = vi.fn();
    learnDaemonName(ID, "marvin");
    connection.nameEpoch++;
    render(ConnectionSheet, { props: { onleave, onclose: () => {} } });
    fireEvent.click(
      screen.getByRole("button", { name: /Disconnect from marvin/ }),
    );
    expect(onleave).toHaveBeenCalledWith(false);
  });
});
