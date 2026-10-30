// @vitest-environment jsdom
// C87T1: WidgetCard intake_breakdown bucket-click drill-down —
// clicking a trend-chart point opens the C85 drill-down modal for
// that bucket (current granularity + the trend payload's
// bucket_start, never recomputed client-side); clicks with no
// active payload open nothing; the modal title follows the
// granularity; an empty bucket shows the honest empty state, not a
// spinner; pagination works inside the bucket modal; metric tile
// clicks are unchanged; shared-view readers drill buckets too.
// Live-browser click-through is NOT available in this environment
// (subagent turns have no live-browser tool): chart clicks are
// covered here by mocked trend payloads + mocked recharts click
// events (extractor unit tests) and by jsdom mouseMove+click
// events on the real recharts chart (integration tests).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import WidgetCard, { bucketStartFromTrendClick } from "./WidgetCard";
import {
  intakeBucketTitle,
  type DashboardWidget,
  type IntakeDrilldown,
  type IntakeDrilldownItem,
  type IntakeTrend,
  type IntakeTrendBucket,
  type IntakeTrendGranularity,
  type WidgetData,
  type WidgetType,
} from "../../lib/dashboards";

const mockFetchWidgetData = vi.hoisted(
  () => vi.fn<(id: string, page?: number) => Promise<WidgetData>>(),
);
const mockFetchIntakeTrend = vi.hoisted(
  () =>
    vi.fn<
      (
        id: string,
        granularity: IntakeTrendGranularity,
        buckets?: number,
      ) => Promise<IntakeTrend>
    >(),
);
const mockFetchIntakeDrilldown = vi.hoisted(
  () =>
    vi.fn<
      (id: string, metric: string, page?: number) => Promise<IntakeDrilldown>
    >(),
);
const mockFetchIntakeDrilldownBucket = vi.hoisted(
  () =>
    vi.fn<
      (
        id: string,
        granularity: IntakeTrendGranularity,
        bucket_start: string,
        page?: number,
      ) => Promise<IntakeDrilldown>
    >(),
);

vi.mock("../../lib/dashboards", async (importOriginal) => {
  const mod =
    await importOriginal<typeof import("../../lib/dashboards")>();
  return {
    ...mod,
    fetchWidgetData: mockFetchWidgetData,
    fetchIntakeTrend: mockFetchIntakeTrend,
    fetchIntakeDrilldown: mockFetchIntakeDrilldown,
    fetchIntakeDrilldownBucket: mockFetchIntakeDrilldownBucket,
  };
});

// Recharts ResponsiveContainer needs a ResizeObserver + a measured
// box; jsdom provides neither, so report a fixed 600x220 box
// (same probe-verified shim as the C86 trend tests).
type ResizeEntry = {
  contentRect: { width: number; height: number };
  borderBoxSize: { inlineSize: number; blockSize: number }[];
  contentBoxSize: { inlineSize: number; blockSize: number }[];
};
class FixedSizeResizeObserver {
  private cb: (entries: ResizeEntry[]) => void;
  constructor(cb: (entries: ResizeEntry[]) => void) {
    this.cb = cb;
  }
  observe() {
    this.cb([
      {
        contentRect: { width: 600, height: 220 },
        borderBoxSize: [{ inlineSize: 600, blockSize: 220 }],
        contentBoxSize: [{ inlineSize: 600, blockSize: 220 }],
      },
    ]);
  }
  unobserve() {}
  disconnect() {}
}

beforeEach(() => {
  vi.stubGlobal("ResizeObserver", FixedSizeResizeObserver);
});

afterEach(() => {
  cleanup();
  mockFetchWidgetData.mockReset();
  mockFetchIntakeTrend.mockReset();
  mockFetchIntakeDrilldown.mockReset();
  mockFetchIntakeDrilldownBucket.mockReset();
  vi.unstubAllGlobals();
});

function widget(
  type: WidgetType,
  config: DashboardWidget["config"] = {},
): DashboardWidget {
  return {
    id: "w1",
    dashboard_id: "d1",
    title: "Intake widget",
    widget_type: type,
    config,
    grid_x: 0,
    grid_y: 0,
    grid_w: 6,
    grid_h: 4,
    created_at: "",
    updated_at: "",
  };
}

const intake = {
  in_app_volume: 12,
  form_volume: 3,
  email_volume: 0,
  total_accepted: 9,
  total_rejected: 2,
  total_duplicate: 4,
  pending: 7,
  snoozed: 3,
  avg_decide_seconds: 5400,
};

const emptyIntake = {
  in_app_volume: 0,
  form_volume: 0,
  email_volume: 0,
  total_accepted: 0,
  total_rejected: 0,
  total_duplicate: 0,
  pending: 0,
  snoozed: 0,
  avg_decide_seconds: 0,
};

function bucket(
  bucket_start: string,
  overrides: Partial<IntakeTrendBucket> = {},
): IntakeTrendBucket {
  return {
    bucket_start,
    in_app: 0,
    form: 0,
    email: 0,
    accepted: 0,
    rejected: 0,
    duplicate: 0,
    ...overrides,
  };
}

function trend(
  granularity: string,
  buckets: IntakeTrendBucket[],
): IntakeTrend {
  return { granularity, buckets, total_buckets: buckets.length };
}

function intakeItem(
  overrides: Partial<IntakeDrilldownItem> = {},
): IntakeDrilldownItem {
  return {
    id: "ii1",
    issue_id: "issue-1",
    title: "Fix login redirect",
    project: { id: "p1", name: "Acme" },
    status: 3,
    status_name: "accepted",
    source: "in_app",
    snoozed_till: null,
    created_at: "2026-10-28T10:00:00Z",
    ...overrides,
  };
}

function bucketDrilldown(
  items: IntakeDrilldownItem[],
  overrides: Partial<IntakeDrilldown> = {},
): IntakeDrilldown {
  return {
    items,
    page: 1,
    per_page: 10,
    total: items.length,
    metric: "",
    ...overrides,
  };
}

function renderCard(w: DashboardWidget, canEdit = true) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={qc}>
      <WidgetCard
        widget={w}
        canEdit={canEdit}
        onEdit={() => {}}
        onDelete={() => {}}
      />
    </QueryClientProvider>,
  );
}

const weekBuckets = [
  bucket("2026-10-05", { in_app: 4, form: 1, accepted: 3 }),
  bucket("2026-10-12", { in_app: 7, form: 2, accepted: 5 }),
  bucket("2026-10-19", { in_app: 2, form: 0, accepted: 1 }),
];

/** Wait for the trend chart to render, then simulate a recharts
 *  point click: mouseMove over the middle of the chart (which sets
 *  recharts' active tooltip index — v3 throttles that state through
 *  requestAnimationFrame, so the TOOLTIP WRAPPER itself must show
 *  the point before the click, otherwise the click reads stale
 *  state and carries no active payload) followed by a click. With
 *  three buckets the middle point is the second bucket. The wait
 *  must target the tooltip wrapper, NOT findByText: jsdom measures
 *  every tick as zero-width, so the XAxis renders all bucket_starts
 *  as ticks and findByText resolves before any tooltip exists. */
async function clickMiddlePoint(middleBucketStart = "2026-10-12") {
  await screen.findByText("In-app + form volume");
  const section = screen.getByLabelText("Intake trend");
  const wrapper = section.querySelector(".recharts-wrapper");
  expect(wrapper).not.toBeNull();
  fireEvent.mouseMove(wrapper!, { clientX: 300, clientY: 100 });
  await waitFor(() => {
    const tip = section.querySelector(".recharts-tooltip-wrapper");
    expect(tip?.textContent ?? "").toContain(middleBucketStart);
  });
  fireEvent.click(wrapper!);
}

/** After a granularity toggle, wait for the chart to re-render with
 *  the new buckets (XAxis ticks carry the new bucket_starts) before
 *  any mouseMove — otherwise the move lands on the stale chart and
 *  the tooltip wait below can never resolve for the new bucket. */
async function waitForTrendBuckets(bucketStarts: string[]) {
  const section = screen.getByLabelText("Intake trend");
  await waitFor(() => {
    const ticks = [...section.querySelectorAll("tspan")].map(
      (t) => t.textContent,
    );
    for (const b of bucketStarts) expect(ticks).toContain(b);
  });
}

describe("bucketStartFromTrendClick (mocked recharts click events)", () => {
  it("reads bucket_start from the active payload's datum", () => {
    expect(
      bucketStartFromTrendClick({
        activePayload: [
          { payload: { bucket_start: "2026-10-12", name: "2026-10-12" } },
        ],
      }),
    ).toBe("2026-10-12");
  });

  it("falls back to the datum name when bucket_start is absent", () => {
    expect(
      bucketStartFromTrendClick({
        activePayload: [{ payload: { name: "2026-10-12" } }],
      }),
    ).toBe("2026-10-12");
  });

  it("falls back to activeLabel when there is no active payload", () => {
    expect(
      bucketStartFromTrendClick({ activeLabel: "2026-10-12" }),
    ).toBe("2026-10-12");
  });

  it("returns null for an empty active payload (empty chart margins)", () => {
    expect(bucketStartFromTrendClick({ activePayload: [] })).toBeNull();
  });

  it("returns null for an active payload with no identifiable bucket", () => {
    expect(
      bucketStartFromTrendClick({ activePayload: [{ payload: {} }] }),
    ).toBeNull();
  });

  it("returns null for null/undefined/non-object events", () => {
    expect(bucketStartFromTrendClick(null)).toBeNull();
    expect(bucketStartFromTrendClick(undefined)).toBeNull();
    expect(bucketStartFromTrendClick("click")).toBeNull();
  });
});

describe("intakeBucketTitle", () => {
  it("week title uses the Monday date verbatim", () => {
    expect(intakeBucketTitle("week", "2026-10-26")).toBe(
      "Intake — week of 2026-10-26",
    );
  });

  it("day title uses the date verbatim", () => {
    expect(intakeBucketTitle("day", "2026-10-27")).toBe(
      "Intake — day of 2026-10-27",
    );
  });

  it("month title renders Month YYYY, never a timezone-shifted date", () => {
    expect(intakeBucketTitle("month", "2026-10-01")).toBe(
      "Intake — month of October 2026",
    );
    expect(intakeBucketTitle("month", "2026-01-01")).toBe(
      "Intake — month of January 2026",
    );
  });

  it("malformed month start falls back to the raw value", () => {
    expect(intakeBucketTitle("month", "not-a-date")).toBe(
      "Intake — month of not-a-date",
    );
  });
});

describe("WidgetCard intake bucket drill-down (C87T1)", () => {
  it("shows the pointer cursor hint line under the chart", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    renderCard(widget("intake_breakdown", { view: "grid" }));
    expect(
      await screen.findByText("Click a point to see that period's rows"),
    ).toBeInTheDocument();
    const section = screen.getByLabelText("Intake trend");
    const wrapper = section.querySelector(".recharts-wrapper");
    expect(wrapper).not.toBeNull();
    expect((wrapper as HTMLElement).style.cursor).toBe("pointer");
  });

  it("click on a chart point opens the modal with granularity + bucket_start params", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    mockFetchIntakeDrilldownBucket.mockResolvedValue(
      bucketDrilldown([intakeItem()]),
    );
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await clickMiddlePoint();
    expect(mockFetchIntakeDrilldownBucket).toHaveBeenCalledWith(
      "w1",
      "week",
      "2026-10-12",
      1,
    );
    expect(
      await screen.findByText("Intake — week of 2026-10-12"),
    ).toBeInTheDocument();
    expect(screen.getByText("Fix login redirect")).toBeInTheDocument();
    expect(screen.getByText("Acme")).toBeInTheDocument();
    expect(screen.getByText("accepted")).toBeInTheDocument();
    expect(screen.getByText("in_app")).toBeInTheDocument();
  });

  it("bucket modal sends no metric (all rows in the bucket); C88 adds its Export CSV", async () => {
    // C88T1 supersedes the C87 "no Export CSV" half of this test:
    // the bucket modal now HAS the button (covered in detail by
    // WidgetCard.c88.test.tsx). What stays true from C87 is the
    // fetch side — the bucket drill-down still sends no metric.
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    mockFetchIntakeDrilldownBucket.mockResolvedValue(
      bucketDrilldown([intakeItem()]),
    );
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await clickMiddlePoint();
    await screen.findByText("Intake — week of 2026-10-12");
    expect(mockFetchIntakeDrilldown).not.toHaveBeenCalled();
    expect(
      screen.getByRole("button", { name: "Export CSV" }),
    ).toBeInTheDocument();
  });

  it("click with no active payload opens nothing", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    mockFetchIntakeDrilldownBucket.mockResolvedValue(
      bucketDrilldown([intakeItem()]),
    );
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("In-app + form volume");
    const section = screen.getByLabelText("Intake trend");
    const wrapper = section.querySelector(".recharts-wrapper");
    expect(wrapper).not.toBeNull();
    // Plain click, no prior mouseMove: recharts has no active
    // payload, so the guard must open nothing.
    fireEvent.click(wrapper!);
    await waitFor(() =>
      expect(mockFetchIntakeDrilldownBucket).not.toHaveBeenCalled(),
    );
    expect(
      screen.queryByText("Intake — week of 2026-10-12"),
    ).not.toBeInTheDocument();
  });

  it("day granularity: bucket click sends day + that day's bucket_start, day title", async () => {
    const dayBuckets = [
      bucket("2026-10-26", { in_app: 3 }),
      bucket("2026-10-27", { in_app: 5 }),
      bucket("2026-10-28", { in_app: 1 }),
    ];
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    mockFetchIntakeDrilldownBucket.mockResolvedValue(
      bucketDrilldown([intakeItem()]),
    );
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Trend — last 12 weeks");
    mockFetchIntakeTrend.mockResolvedValue(trend("day", dayBuckets));
    await user.click(screen.getByRole("button", { name: "Day" }));
    await screen.findByText("Trend — last 12 days");
    await waitForTrendBuckets(["2026-10-26", "2026-10-27", "2026-10-28"]);
    await clickMiddlePoint("2026-10-27");
    expect(mockFetchIntakeDrilldownBucket).toHaveBeenCalledWith(
      "w1",
      "day",
      "2026-10-27",
      1,
    );
    expect(
      await screen.findByText("Intake — day of 2026-10-27"),
    ).toBeInTheDocument();
  });

  it("month granularity: bucket click sends month + that month's bucket_start, month title", async () => {
    const monthBuckets = [
      bucket("2026-08-01", { in_app: 3 }),
      bucket("2026-09-01", { in_app: 5 }),
      bucket("2026-10-01", { in_app: 1 }),
    ];
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    mockFetchIntakeDrilldownBucket.mockResolvedValue(
      bucketDrilldown([intakeItem()]),
    );
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Trend — last 12 weeks");
    mockFetchIntakeTrend.mockResolvedValue(trend("month", monthBuckets));
    await user.click(screen.getByRole("button", { name: "Month" }));
    await screen.findByText("Trend — last 12 months");
    await waitForTrendBuckets(["2026-08-01", "2026-09-01", "2026-10-01"]);
    await clickMiddlePoint("2026-09-01");
    expect(mockFetchIntakeDrilldownBucket).toHaveBeenCalledWith(
      "w1",
      "month",
      "2026-09-01",
      1,
    );
    expect(
      await screen.findByText("Intake — month of September 2026"),
    ).toBeInTheDocument();
  });

  it("empty bucket opens with the honest empty state, not a spinner", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    mockFetchIntakeDrilldownBucket.mockResolvedValue(
      bucketDrilldown([], { total: 0 }),
    );
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await clickMiddlePoint();
    expect(
      await screen.findByText("Intake — week of 2026-10-12"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("No intake rows in this period."),
    ).toBeInTheDocument();
    expect(
      screen.queryByLabelText("Loading intake rows"),
    ).not.toBeInTheDocument();
  });

  it("pagination inside the bucket modal fetches the next page", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    mockFetchIntakeDrilldownBucket.mockImplementation(
      async (_id, _g, _b, page = 1) =>
        bucketDrilldown(
          page === 1
            ? [intakeItem()]
            : [intakeItem({ id: "ii2", title: "Second row" })],
          { page, per_page: 10, total: 25 },
        ),
    );
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await clickMiddlePoint();
    await screen.findByText("Fix login redirect");
    expect(screen.getByText("Page 1 of 3")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(mockFetchIntakeDrilldownBucket).toHaveBeenCalledWith(
      "w1",
      "week",
      "2026-10-12",
      2,
    );
    expect(await screen.findByText("Second row")).toBeInTheDocument();
  });

  it("metric tile click still opens the metric view unchanged", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    mockFetchIntakeDrilldown.mockResolvedValue(
      bucketDrilldown([intakeItem()], { metric: "in_app", total: 1 }),
    );
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Trend — last 12 weeks");
    await user.click(
      screen.getByRole("button", { name: "Drill into In-app volume" }),
    );
    expect(mockFetchIntakeDrilldown).toHaveBeenCalledWith("w1", "in_app", 1);
    expect(mockFetchIntakeDrilldownBucket).not.toHaveBeenCalled();
    expect(
      await screen.findByText("Intake widget — In-app volume"),
    ).toBeInTheDocument();
  });

  it("shared-view read-only still drills a bucket", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    mockFetchIntakeDrilldownBucket.mockResolvedValue(
      bucketDrilldown([intakeItem()]),
    );
    renderCard(widget("intake_breakdown", { view: "grid" }), false);
    await clickMiddlePoint();
    expect(mockFetchIntakeDrilldownBucket).toHaveBeenCalledWith(
      "w1",
      "week",
      "2026-10-12",
      1,
    );
    expect(
      await screen.findByText("Intake — week of 2026-10-12"),
    ).toBeInTheDocument();
  });

  it("empty-chart state shows no chart, no hint, and no clickable affordance", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake: emptyIntake });
    mockFetchIntakeTrend.mockResolvedValue(
      trend("week", [bucket("2026-10-05"), bucket("2026-10-12")]),
    );
    renderCard(widget("intake_breakdown", { view: "grid" }));
    expect(
      await screen.findByText(
        "No intake yet — trends appear once intake rows exist",
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("Click a point to see that period's rows"),
    ).not.toBeInTheDocument();
    expect(mockFetchIntakeDrilldownBucket).not.toHaveBeenCalled();
  });
});
