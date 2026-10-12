// C29T1: workload lib unit tests.
import { describe, expect, it, vi } from "vitest";
import { fetchWorkload, loadFraction, workloadBarColor } from "./workload";
import { api } from "./api";
import type { WorkloadMember } from "./workload";

vi.mock("./api", () => ({ api: { get: vi.fn() } }));

const base: WorkloadMember = {
  user_id: "u1",
  display_name: "Alice Nguyen",
  role: 15,
  weekly_capacity_points: 20,
  open_count: 3,
  open_points: 26,
  overdue_count: 2,
  priority: { urgent: 1, high: 1, medium: 1, low: 0, none: 0 },
  completed_14d: 5,
  overloaded: true,
  project_ids: ["p1"],
};

describe("loadFraction", () => {
  it("returns open_points / capacity", () => {
    expect(loadFraction(base)).toBeCloseTo(1.3);
  });
  it("returns null when capacity is unset", () => {
    expect(
      loadFraction({ ...base, weekly_capacity_points: null }),
    ).toBeNull();
  });
  it("returns null when capacity is zero", () => {
    expect(loadFraction({ ...base, weekly_capacity_points: 0 })).toBeNull();
  });
});

describe("workloadBarColor", () => {
  it("is neutral blue with no capacity set", () => {
    expect(workloadBarColor(null)).toBe("#60a5fa");
  });
  it("is green for healthy load", () => {
    expect(workloadBarColor(0.5)).toBe("#42c88a");
  });
  it("is amber near capacity", () => {
    expect(workloadBarColor(0.8)).toBe("#fbbf24");
    expect(workloadBarColor(0.99)).toBe("#fbbf24");
  });
  it("is red when overloaded", () => {
    expect(workloadBarColor(1.01)).toBe("#f87171");
  });
});

describe("fetchWorkload", () => {
  it("builds the URL with repeatable project params", async () => {
    const get = api.get as unknown as ReturnType<typeof vi.fn>;
    get.mockResolvedValueOnce({
      members: [],
      unassigned: [],
      generated_at: "",
    });
    await fetchWorkload("acme", ["p1", "p2"]);
    expect(get).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/workload?project=p1&project=p2",
    );
  });

  it("omits the query string when no projects are selected", async () => {
    const get = api.get as unknown as ReturnType<typeof vi.fn>;
    get.mockResolvedValueOnce({
      members: [],
      unassigned: [],
      generated_at: "",
    });
    await fetchWorkload("acme", []);
    expect(get).toHaveBeenCalledWith("/api/v1/workspaces/acme/workload");
  });
});
