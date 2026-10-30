// @vitest-environment jsdom
// C88T1: WidgetCard intake_breakdown bucket modal gains the Export
// CSV button (C85 metric modal's footer position + visual language,
// prototype cycle88-prototype.html as spec). Clicking it downloads
// the bucket's rows via the T0 endpoint — granularity +
// bucket_start, NO metric in bucket mode, matching the C87 fetch.
// The metric modal button, the card-level aggregate Export CSV and
// Trend CSV menu items are unchanged; a failed export leaves the
// modal untouched; an empty bucket still shows the honest empty
// state with the button present (an empty file is a valid export
// of zero rows, not an error). Live-browser click-through is NOT
// available in this environment (subagent turns have no
// live-browser tool): covered here by jsdom mouseMove+click on the
// real recharts chart, same as the C87 tests.
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
import WidgetCard from "./WidgetCard";
import {
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
const mockDownloadCsv = vi.hoisted(() => vi.fn());
const mockDownloadTrendCsv = vi.hoisted(() => vi.fn());

vi.mock("../../lib/dashboards", async (importOriginal) => {
  const mod =
    await importOriginal<typeof import("../../lib/dashboards")>();
  return {
    ...mod,
    fetchWidgetData: mockFetchWidgetData,
    fetchIntakeTrend: mockFetchIntakeTrend,
    fetchIntakeDrilldown: mockFetchIntakeDrilldown,
    fetchIntakeDrilldownBucket: mockFetchIntakeDrilldownBucket,
    downloadWidgetCsv: mockDownloadCsv,
    downloadWidgetTrendCsv: mockDownloadTrendCsv,
  };
});

// Same probe-verified ResizeObserver shim as the C86/C87 tests.
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
  mockDownloadCsv.mockReset();
  mockDownloadTrendCsv.mockReset();
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

async function waitForTrendBuckets(bucketStarts: string[]) {
  const section = screen.getByLabelText("Intake trend");
  await waitFor(() => {
    const ticks = [...section.querySelectorAll("tspan")].map(
      (t) => t.textContent,
    );
    for (const b of bucketStarts) expect(ticks).toContain(b);
  });
}

async function openBucketModal() {
  mockFetchWidgetData.mockResolvedValue({ intake });
  mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
  mockFetchIntakeDrilldownBucket.mockResolvedValue(
    bucketDrilldown([intakeItem()]),
  );
  renderCard(widget("intake_breakdown", { view: "grid" }));
  await clickMiddlePoint();
  expect(
    await screen.findByText("Intake — week of 2026-10-12"),
  ).toBeInTheDocument();
}

describe("WidgetCard bucket modal Export CSV (C88T1)", () => {
  it("bucket modal renders the Export CSV button next to Close", async () => {
    await openBucketModal();
    expect(
      screen.getByRole("button", { name: "Close" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Export CSV" }),
    ).toBeInTheDocument();
  });

  it("footer order is Close first, then Export CSV (metric modal layout)", async () => {
    await openBucketModal();
    const dialog = screen.getByRole("dialog");
    const buttons = [...dialog.querySelectorAll("button")].map((b) =>
      b.textContent?.trim(),
    );
    expect(buttons).toEqual(["Close", "Export CSV"]);
  });

  it("click sends granularity + bucket_start with no metric", async () => {
    mockDownloadCsv.mockResolvedValue(undefined);
    const user = userEvent.setup();
    await openBucketModal();
    await user.click(screen.getByRole("button", { name: "Export CSV" }));
    expect(mockDownloadCsv).toHaveBeenCalledWith("w1", undefined, {
      granularity: "week",
      bucket_start: "2026-10-12",
    });
    expect(mockDownloadCsv).toHaveBeenCalledTimes(1);
    expect(mockDownloadTrendCsv).not.toHaveBeenCalled();
    expect(mockFetchIntakeDrilldown).not.toHaveBeenCalled();
  });

  it("day granularity: bucket export sends day + that day's bucket_start", async () => {
    const dayBuckets = [
      bucket("2026-10-26", { in_app: 3 }),
      bucket("2026-10-27", { in_app: 5 }),
      bucket("2026-10-28", { in_app: 1 }),
    ];
    mockDownloadCsv.mockResolvedValue(undefined);
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
    expect(
      await screen.findByText("Intake — day of 2026-10-27"),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Export CSV" }));
    expect(mockDownloadCsv).toHaveBeenCalledWith("w1", undefined, {
      granularity: "day",
      bucket_start: "2026-10-27",
    });
  });

  it("month granularity: bucket export sends month + that month's bucket_start", async () => {
    const monthBuckets = [
      bucket("2026-08-01", { in_app: 3 }),
      bucket("2026-09-01", { in_app: 5 }),
      bucket("2026-10-01", { in_app: 1 }),
    ];
    mockDownloadCsv.mockResolvedValue(undefined);
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
    expect(
      await screen.findByText("Intake — month of September 2026"),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Export CSV" }));
    expect(mockDownloadCsv).toHaveBeenCalledWith("w1", undefined, {
      granularity: "month",
      bucket_start: "2026-09-01",
    });
  });

  it("failed export leaves the modal untouched (no partial download)", async () => {
    mockDownloadCsv.mockRejectedValue(new Error("read-gated"));
    const user = userEvent.setup();
    await openBucketModal();
    await user.click(screen.getByRole("button", { name: "Export CSV" }));
    expect(mockDownloadCsv).toHaveBeenCalledTimes(1);
    // Same modal, same rows, no error surface, still open.
    expect(
      screen.getByText("Intake — week of 2026-10-12"),
    ).toBeInTheDocument();
    expect(screen.getByText("Fix login redirect")).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.queryByText(/read-gated/)).not.toBeInTheDocument();
  });

  it("empty bucket still shows the honest empty state with the button present", async () => {
    mockDownloadCsv.mockResolvedValue(undefined);
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    mockFetchIntakeDrilldownBucket.mockResolvedValue(
      bucketDrilldown([], { total: 0 }),
    );
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await clickMiddlePoint();
    expect(
      await screen.findByText("Intake — week of 2026-10-12"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("No intake rows in this period."),
    ).toBeInTheDocument();
    const exportBtn = screen.getByRole("button", { name: "Export CSV" });
    expect(exportBtn).toBeInTheDocument();
    await user.click(exportBtn);
    expect(mockDownloadCsv).toHaveBeenCalledWith("w1", undefined, {
      granularity: "week",
      bucket_start: "2026-10-12",
    });
  });

  it("pagination does not change the export bucket params", async () => {
    mockDownloadCsv.mockResolvedValue(undefined);
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
    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByText("Second row")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Export CSV" }));
    expect(mockDownloadCsv).toHaveBeenCalledWith("w1", undefined, {
      granularity: "week",
      bucket_start: "2026-10-12",
    });
  });

  it("shared-view read-only still offers the bucket Export CSV button", async () => {
    mockDownloadCsv.mockResolvedValue(undefined);
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    mockFetchIntakeDrilldownBucket.mockResolvedValue(
      bucketDrilldown([intakeItem()]),
    );
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }), false);
    await clickMiddlePoint();
    await screen.findByText("Intake — week of 2026-10-12");
    await user.click(screen.getByRole("button", { name: "Export CSV" }));
    expect(mockDownloadCsv).toHaveBeenCalledWith("w1", undefined, {
      granularity: "week",
      bucket_start: "2026-10-12",
    });
  });

  it("metric modal button is unchanged: sends metric, no bucket", async () => {
    mockDownloadCsv.mockResolvedValue(undefined);
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    mockFetchIntakeDrilldown.mockResolvedValue(
      bucketDrilldown([intakeItem()], { metric: "in_app", total: 1 }),
    );
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("In-app volume");
    await user.click(
      screen.getByRole("button", { name: "Drill into In-app volume" }),
    );
    expect(
      await screen.findByText("Intake widget — In-app volume"),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Export CSV/ }));
    expect(mockDownloadCsv).toHaveBeenCalledWith("w1", "in_app");
    expect(mockDownloadCsv).toHaveBeenCalledTimes(1);
  });

  it("card-level aggregate Export CSV menu item is unchanged", async () => {
    mockDownloadCsv.mockResolvedValue(undefined);
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Trend — last 12 weeks");
    await user.click(
      screen.getByRole("button", { name: "Widget options for Intake widget" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: "Export CSV" }),
    );
    expect(mockDownloadCsv).toHaveBeenCalledWith("w1");
    expect(mockDownloadCsv).toHaveBeenCalledTimes(1);
  });

  it("card-level Trend CSV menu item is unchanged", async () => {
    mockDownloadTrendCsv.mockResolvedValue(undefined);
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", weekBuckets));
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Trend — last 12 weeks");
    await user.click(
      screen.getByRole("button", { name: "Widget options for Intake widget" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: "Trend CSV" }),
    );
    expect(mockDownloadTrendCsv).toHaveBeenCalledWith("w1", "week");
    expect(mockDownloadCsv).not.toHaveBeenCalled();
  });
});
