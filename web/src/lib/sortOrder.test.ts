import { describe, expect, it } from "vitest";
import {
  dropSortOrder,
  midpoint,
  REBALANCE_GAP,
  SORT_ORDER_STEP,
  type SortableRow,
} from "./sortOrder";

describe("midpoint", () => {
  it("splits the gap between two neighbors", () => {
    expect(midpoint(0, 1024)).toBe(512);
    expect(midpoint(1024, 2048)).toBe(1536);
  });

  it("handles an empty column", () => {
    expect(midpoint(null, null)).toBe(SORT_ORDER_STEP);
  });

  it("steps out when dropping at either end", () => {
    expect(midpoint(null, 2048)).toBe(2048 - SORT_ORDER_STEP);
    expect(midpoint(2048, null)).toBe(2048 + SORT_ORDER_STEP);
  });

  it("returns null once the gap collapses below the threshold", () => {
    expect(midpoint(1, 1 + REBALANCE_GAP / 2)).toBeNull();
    expect(midpoint(5, 5)).toBeNull();
    // Unsorted input is treated as collapsed, never written through.
    expect(midpoint(10, 5)).toBeNull();
  });

  it("keeps halving on repeated drops (the common case)", () => {
    // 50 inserts between the same two neighbors: each drop halves the
    // remaining gap, so by insert ~40 the gap is under 1e-9 and the
    // util must signal "rebalance first" instead of returning a value
    // float64 can no longer distinguish.
    let lo = 0;
    let hi = SORT_ORDER_STEP;
    let sawNull = false;
    for (let i = 0; i < 50; i++) {
      const m = midpoint(lo, hi);
      if (m === null) {
        sawNull = true;
        break;
      }
      hi = m; // keep inserting between lo and the latest card
    }
    expect(sawNull).toBe(true);
  });
});

describe("dropSortOrder", () => {
  const rows = (orders: number[], state = "s1"): SortableRow[] =>
    orders.map((o, i) => ({ id: `i${i}`, state_id: state, sort_order: o }));

  it("moves a card down within a column (lands after the hovered card)", () => {
    const all = rows([1024, 2048, 3072]);
    // Drag i0 over i2: final order [i1, i2, i0] → after 3072.
    expect(dropSortOrder(all, "i0", "s1", "s1", 2)).toBe(3072 + SORT_ORDER_STEP);
  });

  it("moves a card up within a column (lands before the hovered card)", () => {
    const all = rows([1024, 2048, 3072]);
    // Drag i2 over i0: final order [i2, i0, i1] → before 1024.
    expect(dropSortOrder(all, "i2", "s1", "s1", 0)).toBe(1024 - SORT_ORDER_STEP);
  });

  it("drops between two cards", () => {
    const all = rows([1024, 2048, 3072]);
    // Drag i2 over i1: final order [i0, i2, i1] → midpoint(1024, 2048).
    expect(dropSortOrder(all, "i2", "s1", "s1", 1)).toBe(1536);
  });

  it("moves a card across columns", () => {
    const all: SortableRow[] = [
      { id: "a", state_id: "s1", sort_order: 1024 },
      { id: "x", state_id: "s2", sort_order: 1024 },
      { id: "y", state_id: "s2", sort_order: 2048 },
    ];
    // Drop a over y (index 1 in s2): final s2 = [x, a, y].
    expect(dropSortOrder(all, "a", "s1", "s2", 1)).toBe(1536);
  });

  it("drops into an empty column", () => {
    const all = rows([1024, 2048]);
    expect(dropSortOrder(all, "i0", "s1", "s2", 0)).toBe(SORT_ORDER_STEP);
  });

  it("returns null when the target gap has collapsed", () => {
    const all = rows([1, 1 + REBALANCE_GAP / 2, 5]);
    // Drop i2 between the two collapsed cards (over i1): the gap is
    // 5e-10 < 1e-9, so no midpoint can be written — rebalance first.
    expect(dropSortOrder(all, "i2", "s1", "s1", 1)).toBeNull();
  });

  it("returns null for an unknown card", () => {
    const all = rows([1024]);
    expect(dropSortOrder(all, "nope", "s1", "s1", 0)).toBeNull();
  });
});
