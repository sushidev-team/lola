import { it, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";
import HubChip from "./HubChip.svelte";

it("labels a hub session and says where it came from", () => {
  render(HubChip, { hub: { itemId: "it_1", kind: "dep_advisory", label: "GHSA-x · lodash" } });
  const chip = screen.getByRole("img", { name: /Team chore: GHSA-x · lodash.*came from the team hub/ });
  expect(chip).toHaveAttribute("data-hub-chip", "active");
  expect(chip).toHaveTextContent("GHSA-x · lodash");
});

it("shows a lost lease and a release", () => {
  const { unmount } = render(HubChip, { hub: { itemId: "it_1", kind: "dep_advisory", label: "GHSA-x", leaseLost: true } });
  expect(screen.getByText("· Lease lost")).toBeInTheDocument();
  unmount();
  render(HubChip, { hub: { itemId: "it_1", kind: "dep_advisory", label: "GHSA-x", released: true } });
  expect(screen.getByText("· Released")).toBeInTheDocument();
  expect(document.querySelector("[data-hub-chip]")).toHaveAttribute("data-hub-chip", "released");
});
