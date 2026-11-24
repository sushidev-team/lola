import { render, fireEvent, screen, waitFor, within } from "@testing-library/svelte";
import { EditorView } from "@codemirror/view";
import { expect, it, vi } from "vitest";
import PreviewStyles from "./PreviewStyles.svelte";

it("edits on the declaration row and tabs through names and values in both directions", async () => {
  vi.stubGlobal("CSS", { supports: () => true });
  const onapply = vi.fn(async () => "");
  try {
    render(PreviewStyles, { element: {
      tag: "div", width: 20, height: 20, inline: [["visibility", "visible"], ["opacity", "1"]],
      css: [], attrs: [], rules: [], unreadable: 0, truncated: false,
    }, onapply, oncomputed: vi.fn() });
    expect(screen.queryByRole("textbox", { name: "New CSS declaration" })).toBeNull();
    const selected = () => window.getSelection()?.toString();
    await fireEvent.click(screen.getByRole("button", { name: "Edit value of visibility" }));
    const editor = await screen.findByRole("textbox", { name: "Edit visibility" });
    await waitFor(() => expect(selected()).toBe("visible"));
    await fireEvent.keyDown(editor, { key: "Tab", shiftKey: true });
    expect(selected()).toBe("visibility");
    await fireEvent.keyDown(editor, { key: "Tab" });
    expect(selected()).toBe("visible");
    const view = EditorView.findFromDOM(editor)!;
    view.dispatch(view.state.replaceSelection("hidden"));
    await fireEvent.keyDown(editor, { key: "Tab" });
    await waitFor(() => expect(selected()).toBe("opacity"));
    expect(onapply).toHaveBeenCalledWith("visibility", "hidden", "visibility");
    expect(screen.queryByRole("textbox", { name: "New CSS declaration" })).toBeNull();
    await fireEvent.keyDown(document.activeElement!, { key: "Tab", shiftKey: true });
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Edit visibility" })).toHaveFocus());
  } finally {
    vi.unstubAllGlobals();
  }
});

it("edits and adds declarations inside the matching rule, and restores focus to its insertion line", async () => {
  vi.stubGlobal("CSS", { supports: () => true });
  const onapply = vi.fn(async () => "");
  try {
    render(PreviewStyles, { element: {
      tag: "img", width: 80, height: 80, inline: [], css: [], attrs: [], unreadable: 0, truncated: false,
      rules: [{ id: 7, selector: ".photo img", source: "app.css", context: "", declarations: [["object-fit", "cover"]] }],
    }, onapply, oncomputed: vi.fn() });
    const rule = within(screen.getByRole("group", { name: ".photo img" }));
    expect(screen.queryByRole("button", { name: "Apply" })).toBeNull();
    await fireEvent.click(rule.getByRole("button", { name: "Edit value of object-fit" }));
    const edit = await rule.findByRole("textbox", { name: "Edit object-fit" });
    const view = EditorView.findFromDOM(edit)!;
    view.dispatch(view.state.replaceSelection("contain"));
    await fireEvent.keyDown(edit, { key: "Tab" });
    await waitFor(() => expect(rule.getByRole("button", { name: "Add declaration" })).toHaveFocus());
    expect(onapply).toHaveBeenCalledWith("object-fit", "contain", "object-fit", 7);

    await fireEvent.click(rule.getByRole("button", { name: "Add declaration" }));
    const add = await rule.findByRole("textbox", { name: "New CSS declaration" });
    await waitFor(() => expect(add).toHaveFocus());
    const draft = EditorView.findFromDOM(add)!;
    draft.dispatch(draft.state.replaceSelection("opacity: .5"));
    await fireEvent.keyDown(add, { key: "Enter" });
    await waitFor(() => expect(rule.getByRole("button", { name: "Add declaration" })).toHaveFocus());
    expect(onapply).toHaveBeenCalledWith("opacity", ".5", undefined, 7);
    expect(screen.queryByRole("textbox", { name: "New CSS declaration" })).toBeNull();
  } finally {
    vi.unstubAllGlobals();
  }
});

it("removes a cleared declaration on Enter and targets the rule from both row actions", async () => {
  vi.stubGlobal("CSS", { supports: () => true });
  const onapply = vi.fn(async () => "");
  try {
    render(PreviewStyles, { element: {
      tag: "img", width: 20, height: 20, inline: [], css: [], attrs: [], unreadable: 0, truncated: false,
      rules: [{ id: 4, selector: "img", source: "a.css", context: "", declarations: [["object-fit", "cover"]] }],
    }, onapply, oncomputed: vi.fn() });
    await fireEvent.click(screen.getByRole("button", { name: "Disable object-fit" }));
    expect(onapply).toHaveBeenLastCalledWith("object-fit", "", "object-fit", 4, false);
    await fireEvent.click(screen.getByRole("button", { name: "Remove object-fit" }));
    expect(onapply).toHaveBeenLastCalledWith("object-fit", "", "object-fit", 4);
    await fireEvent.click(screen.getByRole("button", { name: "Edit value of object-fit" }));
    const field = await screen.findByRole("textbox", { name: "Edit object-fit" });
    const view = EditorView.findFromDOM(field)!;
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: "" } });
    await fireEvent.keyDown(field, { key: "Enter" });
    expect(onapply).toHaveBeenCalledTimes(3);
    expect(onapply).toHaveBeenLastCalledWith("object-fit", "", "object-fit", 4);
  } finally { vi.unstubAllGlobals(); }
});
