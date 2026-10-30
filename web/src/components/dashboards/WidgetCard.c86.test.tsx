// @vitest-environment jsdom
// C86T1: WidgetCard intake_breakdown trend section — recharts area
// chart below the tiles (below the table in Table view), Day/Week/
// Month granularity toggle (default Week, fixed 12 buckets), decided
// line, all-zero email series with the honest note, honest empty
// state when no intake rows exist, and the Trend CSV menu item.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import WidgetCard from "./WidgetCard";
import {
  INTAKE_TREND_BUCKETS,
  trendDecided,
  trendVolume,
  type DashboardWidget,
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
const mockDownloadWidgetTrendCsv = vi.hoisted(() => vi.fn());

vi.mock("../../lib/dashboards", async (importOriginal) => {
  const mod =
    await importOriginal<typeof import("../../lib/dashboards")>();
  return {
    ...mod,
    fetchWidgetData: mockFetchWidgetData,
    fetchIntakeTrend: mockFetchIntakeTrend,
    downloadWidgetTrendCsv: mockDownloadWidgetTrendCsv,
  };
});

// Recharts ResponsiveContainer needs a ResizeObserver + a measured
// box; jsdom provides neither, so report a fixed 600x220 box
// (recharts v3 reads borderBoxSize/contentBoxSize, not contentRect
// alone — verified by probe: contentRect-only entries render no svg).
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
  mockDownloadWidgetTrendCsv.mockReset();
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

const mixedBuckets = [
  bucket("2026-10-05", { in_app: 4, form: 1, accepted: 3 }),
  bucket("2026-10-12", { in_app: 0, form: 0 }),
  bucket("2026-10-19", { in_app: 7, form: 2, accepted: 5, rejected: 1, duplicate: 1 }),
];

describe("WidgetCard intake trend (C86T1)", () => {
  it("renders the trend section with the mocked payload, default Week", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    renderCard(widget("intake_breakdown", { view: "grid" }));
    expect(await screen.findByText("Trend — last 12 weeks")).toBeInTheDocument();
    expect(mockFetchIntakeTrend).toHaveBeenCalledWith("w1", "week", 12);
    expect(
      screen.getByRole("button", { name: "Week" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("Day toggle refetches with granularity day and fixed 12 buckets", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Trend — last 12 weeks");
    mockFetchIntakeTrend.mockClear();
    mockFetchIntakeTrend.mockResolvedValue(trend("day", mixedBuckets));
    await user.click(screen.getByRole("button", { name: "Day" }));
    expect(mockFetchIntakeTrend).toHaveBeenCalledWith("w1", "day", 12);
    expect(await screen.findByText("Trend — last 12 days")).toBeInTheDocument();
  });

  it("Month toggle refetches with granularity month and fixed 12 buckets", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Trend — last 12 weeks");
    mockFetchIntakeTrend.mockClear();
    mockFetchIntakeTrend.mockResolvedValue(trend("month", mixedBuckets));
    await user.click(screen.getByRole("button", { name: "Month" }));
    expect(mockFetchIntakeTrend).toHaveBeenCalledWith("w1", "month", 12);
    expect(await screen.findByText("Trend — last 12 months")).toBeInTheDocument();
  });

  it("shows the decided-line legend entry (accepted + rejected + duplicate)", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    renderCard(widget("intake_breakdown", { view: "grid" }));
    expect(
      await screen.findByText("Decided (accepted + rejected + duplicate)"),
    ).toBeInTheDocument();
  });

  it("shows the created-volume legend entry (in-app + form)", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    renderCard(widget("intake_breakdown", { view: "grid" }));
    expect(
      await screen.findByText("In-app + form volume"),
    ).toBeInTheDocument();
  });

  it("shows the all-zero email series in the legend with the honest note, never hidden", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    renderCard(widget("intake_breakdown", { view: "grid" }));
    expect(
      await screen.findByText("Email — 0 (no email ingestion yet)"),
    ).toBeInTheDocument();
  });

  it("renders an svg chart for partial data (zero buckets do not blank the line)", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    const { container } = render(
      <QueryClientProvider
        client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
      >
        <WidgetCard
          widget={widget("intake_breakdown", { view: "grid" })}
          canEdit
          onEdit={() => {}}
          onDelete={() => {}}
        />
      </QueryClientProvider>,
    );
    await screen.findByText("Trend — last 12 weeks");
    const section = screen.getByLabelText("Intake trend");
    // The section heading renders while the trend query is still
    // pending; wait for the chart itself (legend = data rendered).
    await screen.findByText("Email — 0 (no email ingestion yet)");
    await waitFor(() =>
      expect(section.querySelector("svg")).not.toBeNull(),
    );
    expect(container).toBeTruthy();
    expect(
      screen.queryByText("No intake yet — trends appear once intake rows exist"),
    ).not.toBeInTheDocument();
  });

  it("shows the creation-bucket note (decided-by-creation, triage lag signal)", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    renderCard(widget("intake_breakdown", { view: "grid" }));
    expect(
      await screen.findByText(/Buckets count rows by creation week/),
    ).toBeInTheDocument();
    expect(screen.getByText(/that is the honest signal/)).toBeInTheDocument();
  });

  it("shows the honest empty state when no intake rows exist at all, not a flat zero line", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake: emptyIntake });
    mockFetchIntakeTrend.mockResolvedValue(
      trend("week", [bucket("2026-10-05"), bucket("2026-10-12")]),
    );
    renderCard(widget("intake_breakdown", { view: "grid" }));
    expect(
      await screen.findByText("No intake yet — trends appear once intake rows exist"),
    ).toBeInTheDocument();
  });

  it("does not show the empty state when intake exists but the window is all zeros", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(
      trend("week", [bucket("2026-10-05"), bucket("2026-10-12")]),
    );
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Trend — last 12 weeks");
    // Wait for the trend payload to render before asserting on the
    // legend (the heading alone does not mean the query resolved).
    await screen.findByText("Email — 0 (no email ingestion yet)");
    expect(
      screen.queryByText("No intake yet — trends appear once intake rows exist"),
    ).not.toBeInTheDocument();
    expect(screen.getByText("In-app + form volume")).toBeInTheDocument();
  });

  it("shows a loading state while the trend fetch is pending", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockReturnValue(new Promise<IntakeTrend>(() => {}));
    renderCard(widget("intake_breakdown", { view: "grid" }));
    expect(await screen.findByLabelText("Loading intake trend")).toBeInTheDocument();
  });

  it("shows an error state with Retry, and Retry refetches", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockRejectedValue(new Error("boom"));
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    // The trend query retries once (retry: 1 in the component), so
    // the error state only lands after the automatic retry fails too.
    expect(
      await screen.findByText("Couldn't load the intake trend.", {}, { timeout: 5000 }),
    ).toBeInTheDocument();
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    await user.click(screen.getByRole("button", { name: "Retry" }));
    // Initial attempt + automatic retry + manual Retry.
    expect(mockFetchIntakeTrend).toHaveBeenCalledTimes(3);
    expect(await screen.findByText("In-app + form volume")).toBeInTheDocument();
  });

  it("Trend CSV menu item downloads with the current granularity (week by default)", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    mockDownloadWidgetTrendCsv.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Trend — last 12 weeks");
    await user.click(
      screen.getByRole("button", { name: "Widget options for Intake widget" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: "Trend CSV" }),
    );
    expect(mockDownloadWidgetTrendCsv).toHaveBeenCalledWith("w1", "week");
  });

  it("Trend CSV follows the granularity toggle (month after switching)", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    mockDownloadWidgetTrendCsv.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Trend — last 12 weeks");
    mockFetchIntakeTrend.mockResolvedValue(trend("month", mixedBuckets));
    await user.click(screen.getByRole("button", { name: "Month" }));
    await screen.findByText("Trend — last 12 months");
    await user.click(
      screen.getByRole("button", { name: "Widget options for Intake widget" }),
    );
    await user.click(
      await screen.findByRole("menuitem", { name: "Trend CSV" }),
    );
    expect(mockDownloadWidgetTrendCsv).toHaveBeenCalledWith("w1", "month");
  });

  it("table view renders the trend section under the table", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    const { container } = render(
      <QueryClientProvider
        client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
      >
        <WidgetCard
          widget={widget("intake_breakdown", { view: "table" })}
          canEdit
          onEdit={() => {}}
          onDelete={() => {}}
        />
      </QueryClientProvider>,
    );
    await screen.findByText("Trend — last 12 weeks");
    const table = container.querySelector("table");
    const trendSection = screen.getByLabelText("Intake trend");
    expect(table).not.toBeNull();
    expect(
      table!.compareDocumentPosition(trendSection) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("read-only (shared-view) cards still fetch the trend and offer Trend CSV", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    mockDownloadWidgetTrendCsv.mockResolvedValue(undefined);
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }), false);
    expect(await screen.findByText("Trend — last 12 weeks")).toBeInTheDocument();
    expect(mockFetchIntakeTrend).toHaveBeenCalledWith("w1", "week", 12);
    await user.click(
      screen.getByRole("button", { name: "Widget options for Intake widget" }),
    );
    expect(
      await screen.findByRole("menuitem", { name: "Trend CSV" }),
    ).toBeInTheDocument();
  });

  it("non-intake widgets get no Trend CSV menu item", async () => {
    mockFetchWidgetData.mockResolvedValue({ value: 42 });
    const user = userEvent.setup();
    renderCard(widget("number"));
    await screen.findByText("42");
    await user.click(
      screen.getByRole("button", { name: "Widget options for Intake widget" }),
    );
    expect(await screen.findByRole("menuitem", { name: "Export CSV" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Trend CSV" })).not.toBeInTheDocument();
  });

  it("trend helpers: volume sums channels, decided sums outcomes", () => {
    const b = bucket("2026-10-05", {
      in_app: 4,
      form: 2,
      email: 0,
      accepted: 3,
      rejected: 1,
      duplicate: 2,
    });
    expect(trendVolume(b)).toBe(6);
    expect(trendDecided(b)).toBe(6);
    expect(INTAKE_TREND_BUCKETS).toBe(12);
  });
});

describe("WidgetCard intake trend toggle state", () => {
  it("only one granularity button is pressed at a time", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    const user = userEvent.setup();
    renderCard(widget("intake_breakdown", { view: "grid" }));
    await screen.findByText("Trend — last 12 weeks");
    mockFetchIntakeTrend.mockResolvedValue(trend("day", mixedBuckets));
    await user.click(screen.getByRole("button", { name: "Day" }));
    expect(screen.getByRole("button", { name: "Day" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(screen.getByRole("button", { name: "Week" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
    expect(screen.getByRole("button", { name: "Month" })).toHaveAttribute(
      "aria-pressed",
      "false",
    );
  });

  it("tiles still render above the trend section in grid view", async () => {
    mockFetchWidgetData.mockResolvedValue({ intake });
    mockFetchIntakeTrend.mockResolvedValue(trend("week", mixedBuckets));
    const { container } = render(
      <QueryClientProvider
        client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
      >
        <WidgetCard
          widget={widget("intake_breakdown", { view: "grid" })}
          canEdit
          onEdit={() => {}}
          onDelete={() => {}}
        />
      </QueryClientProvider>,
    );
    await screen.findByText("Trend — last 12 weeks");
    const tile = screen.getByRole("button", { name: "Drill into In-app volume" });
    const trendSection = screen.getByLabelText("Intake trend");
    expect(
      tile.compareDocumentPosition(trendSection) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(container).toBeTruthy();
  });
});
