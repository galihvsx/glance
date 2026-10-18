// @vitest-environment jsdom
// Component tests for the per-webhook delivery history panel (C59T1):
// expand/load, Load more paging via the before cursor, Redeliver only
// on failed rows (with transient confirmation + list refresh), and the
// load-failure message.

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi, type Mock } from "vitest";
import WebhooksSection from "./WebhooksSection";
import { ApiError } from "../../lib/api";
import {
  listWebhookDeliveries,
  listWebhooks,
  redeliverWebhookDelivery,
} from "../../lib/webhooks";
import type { Webhook, WebhookDelivery } from "../../lib/webhooks";

vi.mock("../../lib/webhooks", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../../lib/webhooks")>();
  return {
    ...mod,
    listWebhooks: vi.fn(),
    listWebhookDeliveries: vi.fn(),
    redeliverWebhookDelivery: vi.fn(),
  };
});
vi.mock("../ui/toast", () => ({ toast: { add: vi.fn() } }));

const listMock = listWebhooks as unknown as Mock;
const deliveriesMock = listWebhookDeliveries as unknown as Mock;
const redeliverMock = redeliverWebhookDelivery as unknown as Mock;

const HOOK: Webhook = {
  id: "wh-1",
  workspace_id: "ws-1",
  url: "https://example.com/hook",
  events: ["issue.created"],
  active: true,
  created_at: "2026-10-18T00:00:00Z",
  updated_at: "2026-10-18T00:00:00Z",
  last_delivery_at: null,
  last_delivery_status: null,
  last_error: null,
};

const FAILED: WebhookDelivery = {
  id: 30,
  webhook_id: "wh-1",
  event: "issue.created",
  status: "failed",
  http_status: 500,
  error: "boom: connection refused",
  duration_ms: 12,
  attempted_at: "2026-10-18T00:04:00Z",
};

const DELIVERED: WebhookDelivery = {
  id: 29,
  webhook_id: "wh-1",
  event: "issue.updated",
  status: "delivered",
  http_status: 200,
  error: null,
  duration_ms: 8,
  attempted_at: "2026-10-18T00:03:00Z",
};

const RETRYING: WebhookDelivery = {
  id: 28,
  webhook_id: "wh-1",
  event: "issue.created",
  status: "retrying",
  http_status: null,
  error: "dial timeout",
  duration_ms: 0,
  attempted_at: "2026-10-18T00:02:00Z",
};

const PAGE1 = { deliveries: [FAILED, DELIVERED], next_before: 29 };
const PAGE2 = { deliveries: [RETRYING], next_before: null };

function renderSection() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <WebhooksSection slug="acme" />
    </QueryClientProvider>,
  );
}

async function expandHistory() {
  listMock.mockResolvedValue([HOOK]);
  renderSection();
  expect(
    await screen.findByText("https://example.com/hook"),
  ).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Delivery history" }));
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("WebhookDeliveryHistory panel", () => {
  it("renders collapsed: no delivery rows until expanded", async () => {
    listMock.mockResolvedValue([HOOK]);
    renderSection();
    expect(await screen.findByText("https://example.com/hook")).toBeTruthy();
    expect(screen.queryByText("issue.created")).toBeNull();
    expect(deliveriesMock).not.toHaveBeenCalled();
  });

  it("expands to show deliveries newest-first with status, HTTP status, and error", async () => {
    deliveriesMock.mockResolvedValue(PAGE1);
    await expandHistory();
    expect(
      await screen.findByText("boom: connection refused"),
    ).toBeTruthy();
    expect(screen.getByText("HTTP 500")).toBeTruthy();
    expect(screen.getByText("HTTP 200")).toBeTruthy();
    expect(screen.getByText("Failed")).toBeTruthy();
    expect(screen.getByText("Delivered")).toBeTruthy();
    expect(deliveriesMock).toHaveBeenCalledWith("acme", "wh-1");
  });

  it("shows a Redeliver button ONLY on failed rows", async () => {
    deliveriesMock.mockResolvedValue(PAGE1);
    await expandHistory();
    await screen.findByText("boom: connection refused");
    // Exactly one Redeliver button — the delivered row has none.
    expect(
      screen.getAllByRole("button", { name: /Redeliver/ }),
    ).toHaveLength(1);
  });

  it("Load more pages via the before cursor and appends rows", async () => {
    deliveriesMock.mockImplementation(
      (_slug: string, _id: string, opts?: { before?: number }) =>
        Promise.resolve(opts?.before === 29 ? PAGE2 : PAGE1),
    );
    await expandHistory();
    await screen.findByText("boom: connection refused");
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(
      await screen.findByText("dial timeout"),
    ).toBeTruthy();
    expect(deliveriesMock).toHaveBeenCalledWith("acme", "wh-1", {
      before: 29,
    });
    // Page-1 rows are still there (appended, not replaced).
    expect(screen.getByText("boom: connection refused")).toBeTruthy();
    // Page 2 exhausted → no more Load more.
    expect(
      screen.queryByRole("button", { name: "Load more" }),
    ).toBeNull();
  });

  it("redelivers a failed row, shows a transient confirmation, and refreshes", async () => {
    deliveriesMock.mockResolvedValue(PAGE1);
    redeliverMock.mockResolvedValue({ ok: true });
    await expandHistory();
    await screen.findByText("boom: connection refused");
    fireEvent.click(
      screen.getByRole("button", { name: /Redeliver issue\.created delivery 30/ }),
    );
    await waitFor(() =>
      expect(redeliverMock).toHaveBeenCalledWith("acme", "wh-1", 30),
    );
    expect(
      await screen.findByText(/Redelivery queued/),
    ).toBeTruthy();
    // Refresh re-fetches the list from the start.
    await waitFor(() =>
      expect(deliveriesMock.mock.calls.length).toBeGreaterThan(1),
    );
    expect(deliveriesMock).toHaveBeenLastCalledWith("acme", "wh-1");
  });

  it("shows the load failure without crashing", async () => {
    deliveriesMock.mockRejectedValue(new ApiError(500, "backend is down"));
    await expandHistory();
    expect(await screen.findByText("backend is down")).toBeTruthy();
  });
});
