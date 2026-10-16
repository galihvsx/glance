// @vitest-environment jsdom
// C30T3: risk level badge — user-visible level rendering.
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import RiskLevelBadge from "./RiskLevelBadge";
import type { RiskLevel } from "../../lib/risk";

afterEach(() => cleanup());

describe("RiskLevelBadge", () => {
  it.each([
    ["healthy", "Healthy"],
    ["watch", "Watch"],
    ["at-risk", "At-risk"],
    ["critical", "Critical"],
  ] as [RiskLevel, string][])("renders the %s label", (level, label) => {
    render(<RiskLevelBadge level={level} />);
    const badge = screen.getByTestId("risk-level-badge");
    expect(badge.textContent).toBe(label);
    expect(badge.getAttribute("data-level")).toBe(level);
  });
});
