import { render } from "@testing-library/svelte";
import { describe, expect, it } from "vitest";
import QRCode from "./QRCode.svelte";

describe("QRCode", () => {
  it("draws a code as one path inside a white plate", () => {
    const { container } = render(QRCode, { props: { value: "lola", size: 200 } });
    const svg = container.querySelector("svg");
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("width")).toBe("200");
    // A version 1 code is 21 modules plus a 4-module quiet zone on each side.
    expect(svg?.getAttribute("viewBox")).toBe("0 0 29 29");
    expect(container.querySelectorAll("path").length).toBe(1);
    // The plate is white and the modules are black whatever the theme is: a
    // camera reads contrast, and every flavour would tint one of the two.
    expect(container.querySelector("rect")?.getAttribute("fill")).toBe("#ffffff");
    expect(container.querySelector("path")?.getAttribute("fill")).toBe("#000000");
  });

  it("never puts the value in the accessibility tree", () => {
    // The one caller renders a bearer key. An aria-label carrying it would put
    // the secret somewhere a screen reader — and anything reading the tree —
    // can have it.
    const secret = "lola-insecure1.thisisasecret";
    const { container } = render(QRCode, { props: { value: secret, label: "Connect code" } });
    expect(container.innerHTML).not.toContain(secret);
    expect(container.querySelector("svg")?.getAttribute("aria-label")).toBe("Connect code");
  });

  it("reports a payload it cannot draw instead of throwing", () => {
    // A settings tab must not be taken down by an over-long value.
    const { getByText } = render(QRCode, { props: { value: "x".repeat(5000) } });
    expect(getByText(/Could not draw a code/)).toBeTruthy();
  });
});
