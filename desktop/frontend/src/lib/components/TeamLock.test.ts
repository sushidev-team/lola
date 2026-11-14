import { describe, it, expect, beforeEach } from "vitest";
import { cleanup, render, screen } from "@testing-library/svelte";
import { createRawSnippet } from "svelte";
import TeamLock from "./TeamLock.svelte";

const field = createRawSnippet(() => ({ render: () => `<input aria-label="Total running agents" type="number" />` }));

beforeEach(cleanup);

describe("TeamLock", () => {
  it("renders the control bare when nothing is locked", () => {
    const { container } = render(TeamLock, { lock: null, version: 3, children: field });
    expect(screen.getByLabelText("Total running agents")).toBeEnabled();
    expect(container.querySelector("fieldset")).toBeNull();
    expect(screen.queryByText(/Set by team policy/)).toBeNull();
  });

  it("disables a locked control and names the policy version and team value", () => {
    render(TeamLock, {
      lock: { key: "max_concurrency_hint", label: "Global concurrency cap", value: "2", local: "6" },
      version: 3,
      children: field,
    });
    expect(screen.getByLabelText("Total running agents")).toBeDisabled();
    expect(screen.getByText("Set by team policy v3")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    expect(document.querySelector('[data-team-lock="max_concurrency_hint"]')).not.toBeNull();
  });
});

describe("TeamLock — ceiling", () => {
  it("keeps a ceiling editable and reads it as the team ceiling", () => {
    const { container } = render(TeamLock, {
      lock: { key: "max_concurrency_hint", label: "Global concurrency cap", value: "2", local: "6", ceiling: true },
      version: 4,
      children: field,
    });
    expect(screen.getByLabelText("Total running agents")).toBeEnabled();
    expect(container.querySelector("fieldset")).toBeNull();
    expect(screen.getByText(/Team ceiling/)).toHaveTextContent("Team ceiling 2 (policy v4)");
    expect(screen.queryByText(/Set by team policy/)).toBeNull();
  });
});
