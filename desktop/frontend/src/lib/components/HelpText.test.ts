import { render, screen, fireEvent, cleanup } from "@testing-library/svelte";
import { afterEach, describe, it, expect, vi } from "vitest";
import HelpText from "./HelpText.svelte";

afterEach(cleanup);

describe("HelpText", () => {
  it("shows a short hint and reveals details only on click", async () => {
    render(HelpText, { label: "phone access", summary: "Connect a phone.", detail: "Longer explanation." });
    const button = screen.getByRole("button", { name: "More about phone access" });
    expect(screen.getByText("Connect a phone.")).toBeInTheDocument();
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
    expect(button).toHaveAttribute("aria-expanded", "false");
    await fireEvent.click(button);
    expect(screen.getByRole("note")).toHaveTextContent("Longer explanation.");
    expect(button).toHaveAttribute("aria-controls", screen.getByRole("note").id);
    await fireEvent.click(button);
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
  });

  it("floats outside the form and closes on outside click", async () => {
    const { container } = render(HelpText, { label: "setting", detail: "Details." });
    await fireEvent.click(screen.getByRole("button"));
    const popover = screen.getByRole("note");
    expect(popover.parentElement).toBe(document.body);
    expect(container).not.toContainElement(popover);
    await fireEvent.pointerDown(popover);
    expect(screen.getByRole("note")).toBeInTheDocument();
    await fireEvent.pointerDown(document.body);
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
  });

  it("removes the popover when its owner unmounts", async () => {
    const { unmount } = render(HelpText, { label: "setting", detail: "Details." });
    await fireEvent.click(screen.getByRole("button"));
    expect(screen.getByRole("note")).toBeInTheDocument();
    unmount();
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
  });

  it("dismisses help when its anchor scrolls", async () => {
    render(HelpText, { label: "setting", detail: "Details." });
    await fireEvent.click(screen.getByRole("button"));
    await fireEvent.scroll(document);
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
  });

  it("handles click and Escape without activating the enclosing UI", async () => {
    const { container } = render(HelpText, { label: "setting", detail: "Details." });
    const keydown = vi.fn();
    container.addEventListener("keydown", keydown);
    const button = screen.getByRole("button");
    await fireEvent.click(button);
    button.focus();
    await fireEvent.keyDown(button, { key: "Escape" });
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
    expect(keydown).not.toHaveBeenCalled();
    expect(button).toHaveFocus();
  });
});
