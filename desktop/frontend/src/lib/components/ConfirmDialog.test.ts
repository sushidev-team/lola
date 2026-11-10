import { render, screen, fireEvent, cleanup } from "@testing-library/svelte";
import { describe, it, expect, vi, afterEach } from "vitest";
import ConfirmDialog from "./ConfirmDialog.svelte";
import { confirm } from "$lib/confirm.svelte";

describe("ConfirmDialog choices", () => {
  afterEach(() => {
    confirm.cancel();
    cleanup();
  });

  it("lists the choices as checkboxes and hands the checked ids to onConfirm", async () => {
    const onConfirm = vi.fn();
    confirm.ask({
      title: "Remove stack?",
      body: "Remove stack Donso?",
      confirmLabel: "Remove",
      choices: { label: "Keep in lola", items: [
        { id: "app", label: "Donso App", checked: true },
        { id: "infra", label: "Donso Infra", checked: true },
      ] },
      onConfirm,
    });
    render(ConfirmDialog);
    const box = await screen.findByRole("checkbox", { name: "Keep in lola: Donso App" });
    await fireEvent.click(box);
    await fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(onConfirm).toHaveBeenCalledWith(["infra"]);
  });
});
