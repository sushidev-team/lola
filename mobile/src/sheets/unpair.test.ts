import { beforeEach, describe, expect, it } from "vitest";
import { render } from "@testing-library/svelte";
import { confirm } from "$lib/confirm.svelte";
import { router } from "@mobile/lib/router.svelte";
import Unpair from "./unpair.svelte";

beforeEach(() => {
  confirm.cancel();
  router.sheet = "unpair";
});

describe("unpair sheet", () => {
  it("hands the question to the shared dialog and closes itself", () => {
    render(Unpair);
    expect(confirm.request?.title).toBe("Unpair phone?");
    expect(confirm.request?.confirmLabel).toBe("Unpair");
    expect(router.sheet).toBe("");
  });
});
