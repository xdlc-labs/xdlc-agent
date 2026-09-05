import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";
import "@testing-library/jest-dom/vitest";

// testing-library auto-registers this afterEach only when vitest's
// `globals: true` is on; we keep globals off (explicit imports in test
// files) so we wire cleanup here instead.
afterEach(() => {
  cleanup();
});

// jsdom has no layout engine, so `offsetParent` is always null. The
// Dialog focus trap (src/components/dialog.tsx) uses offsetParent to
// filter out hidden focusable elements when computing the tab order;
// give it a reasonable stand-in so those tests exercise real behavior
// instead of falling into the "no visible focusables" branch.
Object.defineProperty(HTMLElement.prototype, "offsetParent", {
  configurable: true,
  get(this: HTMLElement) {
    return this.parentNode;
  },
});
