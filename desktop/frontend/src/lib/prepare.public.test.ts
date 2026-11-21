// @vitest-environment-options {"url":"https://example.com/login"}
import { expect, it } from "vitest";

// A page on a public host is never typed into or clicked, whatever the spec says.
const nodeFs = "node:" + "fs";
const { readFileSync } = await import(/* @vite-ignore */ nodeFs);
const root = (globalThis as unknown as { process: { cwd(): string } }).process.cwd();
const source: string = readFileSync(root + "/../../internal/preview/prepare.js", "utf8");

it("refuses every step on a public host", async () => {
  new Function(source)();
  document.body.innerHTML = `<input id="e"><button id="b">Go</button>`;
  let clicked = 0;
  document.getElementById("b")!.addEventListener("click", () => clicked++);
  expect(location.hostname).toBe("example.com");
  const run = (window as unknown as { __lolaPrepare: (s: object, t?: number) => Promise<{ failed: { why: string } | null }> }).__lolaPrepare;
  for (const step of [{ do: "fill", selector: "#e", value: "secret" }, { do: "click", selector: "#b" }, { do: "press", key: "Enter" }, { do: "wait", ms: 10 }]) {
    const r = await run({ steps: [step] }, 100);
    expect(r.failed?.why).toBe("this page is not on a local address");
  }
  expect((document.getElementById("e") as HTMLInputElement).value).toBe("");
  expect(clicked).toBe(0);
});
