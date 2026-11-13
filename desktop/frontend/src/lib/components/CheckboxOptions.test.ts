import { render, screen, fireEvent, cleanup } from "@testing-library/svelte";
import { beforeEach, it, expect, vi } from "vitest";
import CheckboxOptions from "./CheckboxOptions.svelte";

beforeEach(cleanup);

it("filters a long list without dropping selections outside the search", async () => {
  const options = Array.from({ length: 12 }, (_, i) => ({ id: String(i), label: `Label ${i}` }));
  const onChange = vi.fn();
  render(CheckboxOptions, { label: "Match labels", options, selected: ["0", "missing"], onChange });
  await fireEvent.input(screen.getByRole("searchbox"), { target: { value: "Label 11" } });
  expect(screen.queryByRole("checkbox", { name: "Label 0" })).not.toBeInTheDocument();
  await fireEvent.click(screen.getByRole("checkbox", { name: "Label 11" }));
  expect(onChange).toHaveBeenCalledWith(["0", "missing", "11"]);
  expect(screen.getByRole("status")).toHaveTextContent("2 selected · 1 of 12 shown");
  await fireEvent.input(screen.getByRole("searchbox"), { target: { value: "no such label" } });
  expect(screen.getByText("No matches. Try another search.")).toBeInTheDocument();
});

it("keeps short lists simple and removes only the chosen selection", async () => {
  const onChange = vi.fn();
  render(CheckboxOptions, { label: "States", options: [{ id: "todo", label: "Todo" }], selected: ["todo", "other"], onChange });
  expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
  await fireEvent.click(screen.getByRole("checkbox", { name: "Todo" }));
  expect(onChange).toHaveBeenCalledWith(["other"]);
});

it("shows a selected id Linear no longer offers, and lets it be removed", async () => {
  // The org→team label move: the config still names the old org label, which
  // the picker cannot list. Hidden, it could never be unticked.
  const onChange = vi.fn();
  render(CheckboxOptions, {
    label: "Match labels",
    options: [{ id: "new-ready", label: "agent-ready" }],
    selected: ["13ad06f0-dead", "new-ready"],
    onChange,
  });
  const dead = screen.getByRole("checkbox", { name: /Unknown 13ad06f0… — no longer in Linear/ });
  expect(dead).toBeChecked();
  await fireEvent.click(dead);
  expect(onChange).toHaveBeenCalledWith(["new-ready"]);
});
