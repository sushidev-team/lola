// Opens the preview page inside a test, the way a browser would. One page per
// test FILE: the stores and the router are process singletons, and a second page
// in the same module registry would inherit the first one's state.

export async function openPreview(query: string): Promise<HTMLElement> {
  document.body.innerHTML = '<div id="app"></div>';
  window.history.replaceState({}, "", `/preview.html${query}`);
  await import("../preview");
  return document.getElementById("app")!;
}

/** Async rendering through lazy chunks is slow under jsdom; the budget is generous and never reached when it works. */
export const PAGE_TIMEOUT = 8000;
