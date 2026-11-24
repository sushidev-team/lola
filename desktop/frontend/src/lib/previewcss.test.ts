import { afterEach, expect, it, vi } from "vitest";
import { readDeclaration } from "./previewcss";

// jsdom lacks CSS.supports. Let its CSSOM parse ordinary values as the browser does.
function installSupports() {
  vi.stubGlobal("CSS", { supports(name: string, value: string) {
    const style = document.createElement("div").style;
    style.setProperty(name, value);
    return style.getPropertyValue(name) !== "";
  } });
}
afterEach(() => vi.unstubAllGlobals());

it("keeps strings, custom values and priority intact when stripping the declaration terminator", () => {
  installSupports();
  expect(readDeclaration('content: "x:y;z" !important;', [])).toMatchObject({ value: '"x:y;z" !important', error: "" });
  expect(readDeclaration("--token: x\\;", [])).toMatchObject({ value: "x\\;", error: "" });
  expect(readDeclaration("color: red; display: block", []).error).toMatch(/one declaration/);
});

it("only offers removal for an existing inline declaration and respects custom property case", () => {
  installSupports();
  const inline = [["visibility", "hidden"], ["--Accent", "red"]];
  expect(readDeclaration("Visibility: ;", inline)).toMatchObject({ remove: true, error: "" });
  expect(readDeclaration("--Accent:", inline).remove).toBe(true);
  expect(readDeclaration("--accent:", inline).error).toMatch(/Enter a value/);
  expect(readDeclaration("display: !important", inline).error).toMatch(/Invalid value/);
  expect(readDeclaration("not-a-property: none", inline).error).toMatch(/Unknown CSS property/);
});
