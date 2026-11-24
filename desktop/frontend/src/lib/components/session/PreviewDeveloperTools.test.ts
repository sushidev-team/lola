import { render, fireEvent, screen, within, waitFor } from "@testing-library/svelte";
import { expect, it, vi } from "vitest";
import PreviewDeveloperTools from "./PreviewDeveloperTools.svelte";
import type { DebugCommand, DebugSnapshot } from "$lib/previewtools";

const nodeFs = "node:" + "fs";
const { readFileSync } = await import(/* @vite-ignore */ nodeFs);
const root = (globalThis as unknown as { process: { cwd(): string } }).process.cwd();
const source = readFileSync(root + "/../../internal/preview/debug.js", "utf8");

it("closes visible branches at their depth and keeps end tags out of element navigation", async () => {
  const frame = document.createElement("iframe");
  document.body.appendChild(frame);
  const win = frame.contentWindow as Window & typeof globalThis;
  const doc = win.document;
  doc.head.innerHTML = "<meta>";
  doc.body.innerHTML = '<main><section><img><span>price</span></section><div></div></main><iframe></iframe><script>0</script>';
  doc.body.appendChild(doc.createElementNS("urn:test", "input"));
  const run = win.Function(source + "\nreturn lolaPreviewDebugger();")() as (cmd: DebugCommand) => DebugSnapshot;
  vi.stubGlobal("webkit", { messageHandlers: { lolaPreviewTools: { postMessage: async (cmd: DebugCommand) =>
    cmd.op === "open" ? null : JSON.stringify(run(cmd)),
  } } });
  const mounted = render(PreviewDeveloperTools, { id: 1, external: true, close: vi.fn() });
  try {
    await fireEvent.click(screen.getByRole("button", { name: "Elements" }));
    const tree = screen.getByRole("tree", { name: "DOM tree" });
    const rows = () => Array.from(tree.querySelectorAll(".tree-row"), el => el.textContent!.trim());
    const items = within(tree);
    await items.findByText("</body>", { exact: true });
    expect(rows()).toEqual([
      "<html>", "<head>…</head> 1 child", "<body>", "<main>", "<section>", "<img>", "<span>price</span>",
      "</section>", "<div></div>", "</main>", "<iframe></iframe>", "<script>…</script>", "<input></input>", "</body>", "</html>",
    ]);
    const span = items.getByRole("treeitem", { name: /price/ });
    await fireEvent.keyDown(span, { key: "ArrowDown" });
    expect(items.getByRole("treeitem", { name: /<div/ })).toHaveFocus();

    const section = items.getByRole("treeitem", { name: /<section/ });
    await fireEvent.click(section.querySelector("[data-branch]")!);
    await waitFor(() => expect(rows()).toContain("<section>…</section> 2 children"));
    expect(rows()).not.toContain("</section>");
    expect(rows().slice(-2)).toEqual(["</body>", "</html>"]);
  } finally {
    mounted.unmount();
    vi.unstubAllGlobals();
    frame.remove();
  }
});
