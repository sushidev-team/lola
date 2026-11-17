import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import FilterSheet from "./FilterSheet.svelte";

const counts = { needs: 2, working: 3, review: 1, done: 4 };

describe("FilterSheet", () => {
  it("is a dialog with one radio per bucket plus All, counts included", () => {
    render(FilterSheet, { props: { value: "", counts, onselect: () => {}, onclose: () => {} } });
    expect(screen.getByRole("dialog", { name: "Filter" })).toBeTruthy();
    expect(screen.getAllByRole("radio")).toHaveLength(7);
    expect(screen.getByRole("radio", { name: /All/ })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("radio", { name: /Working/ }).textContent).toContain("3");
  });

  it("shows a check instead of the count on the selected row only", () => {
    render(FilterSheet, { props: { value: "working", counts, onselect: () => {}, onclose: () => {} } });
    const selected = screen.getByRole("radio", { name: /Working/ });
    expect(selected.querySelector("[data-row-check]")).not.toBeNull();
    expect(selected.textContent).not.toContain("3");
    const other = screen.getByRole("radio", { name: /Done/ });
    expect(other.querySelector("[data-row-check]")).toBeNull();
    expect(other.textContent).toContain("4");
  });

  it("selects a bucket", async () => {
    const onselect = vi.fn();
    render(FilterSheet, { props: { value: "", counts, onselect, onclose: () => {} } });
    await fireEvent.click(screen.getByRole("radio", { name: /In review/ }));
    expect(onselect).toHaveBeenCalledWith("review");
    await fireEvent.click(screen.getByRole("radio", { name: /All/ }));
    expect(onselect).toHaveBeenCalledWith("");
  });

  it("offers Group by with the current choice checked and reports a pick", async () => {
    const ongroup = vi.fn();
    render(FilterSheet, { props: { value: "", counts, group: "project", onselect: () => {}, ongroup, onclose: () => {} } });
    const grp = screen.getByRole("radiogroup", { name: "Group by" });
    expect(grp.querySelectorAll('[role="radio"]')).toHaveLength(2);
    expect(screen.getByRole("radio", { name: "Project" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("radio", { name: "Status" })).toHaveAttribute("aria-checked", "false");
    await fireEvent.click(screen.getByRole("radio", { name: "Status" }));
    expect(ongroup).toHaveBeenCalledWith("status");
  });
});
