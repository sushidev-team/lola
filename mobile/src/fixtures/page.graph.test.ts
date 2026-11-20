import { expect, it } from "vitest";
import { fireEvent, screen, within } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

it("opens the graph by route and keeps PR labels on the matching branch tip", async () => {
  await openPreview("?route=/projects/lola/graph");
  const pr = await screen.findByText("#90", {}, { timeout: PAGE_TIMEOUT });
  expect(pr.closest("button")).toHaveTextContent("Prepare the next release");
  expect(screen.getByRole("button", { name: /Merge the iOS Git client/ })).toHaveAccessibleName(/2 parents/);
  await fireEvent.click(screen.getByRole("button", { name: /Filter history/ }));
  await fireEvent.click(screen.getByRole("button", { name: "main" }));
  const list = await screen.findByRole("list", { name: "Graph" });
  expect(within(list).getByText("Adjust conversation spacing")).toBeTruthy();
  expect(within(list).queryByText("#90")).toBeNull();
}, PAGE_TIMEOUT * 2);
