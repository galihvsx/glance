// C88T1: fetchWidgetCsv / downloadWidgetCsv gain an optional
// bucket param (granularity + bucket_start) alongside the existing
// metric param; the URL builder composes them for the T0 endpoint.
// Existing call shapes (metric only, neither) are unchanged.
import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchWidgetCsv } from "./dashboards";

const mockFetch = vi.hoisted(() => vi.fn());

function csvResponse(filename = "dash-widget-week-2026-10-26.csv") {
  return {
    ok: true,
    status: 200,
    statusText: "OK",
    text: async () => "",
    blob: async () => new Blob(["id,title\n1,x\n"], { type: "text/csv" }),
    headers: new Headers({
      "Content-Disposition": `attachment; filename="${filename}"`,
    }),
  };
}

afterEach(() => {
  mockFetch.mockReset();
  vi.unstubAllGlobals();
});

describe("fetchWidgetCsv bucket param (C88T1)", () => {
  it("bucket only builds granularity + bucket_start with no metric", async () => {
    vi.stubGlobal("fetch", mockFetch);
    mockFetch.mockResolvedValue(csvResponse());
    await fetchWidgetCsv("w1", undefined, {
      granularity: "week",
      bucket_start: "2026-10-26",
    });
    expect(mockFetch).toHaveBeenCalledWith(
      "/api/v1/widgets/w1/export.csv?granularity=week&bucket_start=2026-10-26",
      { credentials: "include" },
    );
  });

  it("metric + bucket compose in one query string", async () => {
    vi.stubGlobal("fetch", mockFetch);
    mockFetch.mockResolvedValue(csvResponse());
    await fetchWidgetCsv("w1", "in_app", {
      granularity: "day",
      bucket_start: "2026-10-27",
    });
    const url = mockFetch.mock.calls[0][0] as string;
    expect(url).toContain("/api/v1/widgets/w1/export.csv?");
    expect(url).toContain("metric=in_app");
    expect(url).toContain("granularity=day");
    expect(url).toContain("bucket_start=2026-10-27");
  });

  it("metric only is unchanged (no bucket params)", async () => {
    vi.stubGlobal("fetch", mockFetch);
    mockFetch.mockResolvedValue(csvResponse());
    await fetchWidgetCsv("w1", "accepted");
    expect(mockFetch).toHaveBeenCalledWith(
      "/api/v1/widgets/w1/export.csv?metric=accepted",
      { credentials: "include" },
    );
  });

  it("neither metric nor bucket builds a bare export URL", async () => {
    vi.stubGlobal("fetch", mockFetch);
    mockFetch.mockResolvedValue(csvResponse());
    await fetchWidgetCsv("w1");
    expect(mockFetch).toHaveBeenCalledWith(
      "/api/v1/widgets/w1/export.csv",
      { credentials: "include" },
    );
  });

  it("carries the server filename and the truncation flag for bucket exports", async () => {
    vi.stubGlobal("fetch", mockFetch);
    const res = csvResponse("dash-widget-week-2026-10-26.csv");
    res.headers = new Headers({
      "Content-Disposition":
        'attachment; filename="dash-widget-week-2026-10-26.csv"',
      "X-Result-Truncated": "true",
    });
    mockFetch.mockResolvedValue(res);
    const out = await fetchWidgetCsv("w1", undefined, {
      granularity: "week",
      bucket_start: "2026-10-26",
    });
    expect(out.filename).toBe("dash-widget-week-2026-10-26.csv");
    expect(out.truncated).toBe(true);
  });

  it("non-ok bucket export throws (caller leaves the modal untouched)", async () => {
    vi.stubGlobal("fetch", mockFetch);
    mockFetch.mockResolvedValue({
      ok: false,
      status: 400,
      statusText: "Bad Request",
      text: async () => "misaligned bucket_start",
      blob: async () => new Blob([]),
      headers: new Headers(),
    });
    await expect(
      fetchWidgetCsv("w1", undefined, {
        granularity: "week",
        bucket_start: "2026-10-27",
      }),
    ).rejects.toThrow("misaligned bucket_start");
  });
});
