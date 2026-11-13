import { it, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import HubChip from "./HubChip.svelte";
import { hub } from "$lib/hub.svelte";
import { nav } from "$lib/nav.svelte";

it("is a button that opens the Team view on its chore", async () => {
  hub.reset();
  render(HubChip, { hub: { itemId: "it_1", kind: "dep_advisory", label: "GHSA-x · lodash" } });
  const chip = await screen.findByRole("button", { name: /Team chore: GHSA-x · lodash.*came from the team hub/ });
  expect(chip).toHaveAttribute("data-hub-chip", "active");
  expect(chip).toHaveTextContent("GHSA-x · lodash");
  await fireEvent.click(chip);
  expect(hub.focusItem).toBe("it_1");
  expect(nav.view).toBe("team");
});

it("stays a static mark when asked to, or inside another button", async () => {
  const { unmount } = render(HubChip, { hub: { itemId: "it_1", kind: "dep_advisory", label: "GHSA-x" }, static: true });
  expect(screen.getByRole("img", { name: /Team chore: GHSA-x/ })).toBeInTheDocument();
  expect(screen.queryByRole("button")).toBeNull();
  unmount();
  // A host <button> around it, as in the sidebar row.
  const host = document.createElement("button");
  document.body.appendChild(host);
  render(HubChip, { target: host, props: { hub: { itemId: "it_1", kind: "dep_advisory", label: "GHSA-x" } } });
  await new Promise((r) => setTimeout(r, 0));
  expect(host.querySelector("button")).toBeNull();
  expect(host.querySelector('[role="img"]')).not.toBeNull();
  host.remove();
});

it("shows a taken, lost, given-back and in-review chore in plain words", async () => {
  const cases: [object, string, string][] = [
    [{ leaseLost: true, leaseTaken: true }, "· Taken by another Mac", "taken"],
    [{ leaseLost: true }, "· No longer claimed", "lost"],
    [{ released: true }, "· Given back", "released"],
    [{ inReview: true }, "· In review", "review"],
  ];
  for (const [h, text, state] of cases) {
    const { unmount } = render(HubChip, { hub: { itemId: "it_1", kind: "dep_advisory", label: "GHSA-x", ...h } });
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(document.querySelector("[data-hub-chip]")).toHaveAttribute("data-hub-chip", state);
    expect(document.body.textContent).not.toMatch(/lease/i);
    unmount();
  }
});
