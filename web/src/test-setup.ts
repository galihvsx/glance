// Shared vitest setup: jest-dom matchers + RTL auto-cleanup between tests
// (vitest doesn't install globals here, so RTL's auto-cleanup doesn't run).
import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => {
  cleanup();
});
