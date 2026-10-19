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
  fetchWebhookDelivery,
  listWebhookDeliveries,
  listWebhooks,
  redeliverWebhookDelivery,
} from "../../lib/webhooks";
import { copyText } from "../../lib/clipboard";
import type {
  Webhook,
  WebhookDelivery,
  WebhookDeliveryDetail,
} from "../../lib/webhooks";

vi.mock("../../lib/webhooks", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../../lib/webhooks")>();
  return {
    ...mod,
    listWebhooks: vi.fn(),
    listWebhookDeliveries: vi.fn(),
    redeliverWebhookDelivery: vi.fn(),
    fetchWebhookDelivery: vi.fn(),
  };
});
vi.mock("../../lib/clipboard", () => ({ copyText: vi.fn() }));
vi.mock("../ui/toast", () => ({ toast: { add: vi.fn() } }));

const listMock = listWebhooks as unknown as Mock;
const deliveriesMock = listWebhookDeliveries as unknown as Mock;
const redeliverMock = redeliverWebhookDelivery as unknown as Mock;
const detailMock = fetchWebhookDelivery as unknown as Mock;
const copyMock = copyText as unknown as Mock;

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
  filter: null,
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

// View-payload dialog (C60T1): a per-row "View payload" button on EVERY
// row (all statuses, not just failed) opens a dialog with the delivery's
// status/event/attempted-at in the header, the event + payload as
// pretty-printed JSON, and a copy button with a transient "Copied"
// confirmation.

const DETAIL: WebhookDeliveryDetail = {
  ...FAILED,
  data: {
    issue: { id: "iss-1", title: "Fix it" },
    workspace: { id: "ws-1" },
  },
};

const PAGE3 = { deliveries: [FAILED, DELIVERED, RETRYING], next_before: null };

async function openPayloadDialog() {
  deliveriesMock.mockResolvedValue(PAGE1);
  detailMock.mockResolvedValue(DETAIL);
  await expandHistory();
  await screen.findByText("boom: connection refused");
  fireEvent.click(
    screen.getByRole("button", { name: /View payload.*delivery 30/ }),
  );
}

describe("View payload dialog (C60T1)", () => {
  it("renders a View payload button on every row, whatever the status", async () => {
    deliveriesMock.mockResolvedValue(PAGE3);
    await expandHistory();
    await screen.findByText("boom: connection refused");
    expect(
      screen.getAllByRole("button", { name: /View payload/ }),
    ).toHaveLength(3);
  });

  it("opens a dialog with status/event/attempted-at in the header and the payload as pretty JSON", async () => {
    await openPayloadDialog();
    await waitFor(() =>
      expect(detailMock).toHaveBeenCalledWith("acme", "wh-1", 30),
    );
    const dialog = await screen.findByRole("dialog");
    // Header: the delivery's status label, event, and attempted-at.
    expect(dialog.textContent).toContain("Failed");
    expect(dialog.textContent).toContain("issue.created");
    // Body: pretty-printed event + payload, not the bookkeeping fields.
    expect(dialog.textContent).toContain('"event": "issue.created"');
    expect(dialog.textContent).toContain('"title": "Fix it"');
    expect(dialog.textContent).not.toContain("http_status");
  });

  it("copies the payload JSON and confirms transiently", async () => {
    copyMock.mockResolvedValue(true);
    await openPayloadDialog();
    await screen.findByRole("dialog");
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() =>
      expect(copyMock).toHaveBeenCalledWith(
        expect.stringContaining('"event": "issue.created"'),
      ),
    );
    expect(
      await screen.findByRole("button", { name: "Copied" }),
    ).toBeTruthy();
  });

  it("shows the detail load failure in the dialog without crashing", async () => {
    deliveriesMock.mockResolvedValue(PAGE1);
    detailMock.mockRejectedValue(new ApiError(500, "backend is down"));
    await expandHistory();
    await screen.findByText("boom: connection refused");
    fireEvent.click(
      screen.getByRole("button", { name: /View payload.*delivery 30/ }),
    );
    expect(await screen.findByRole("dialog")).toBeTruthy();
    expect(await screen.findByText("backend is down")).toBeTruthy();
  });

  it("closes the dialog without refetching the detail", async () => {
    await openPayloadDialog();
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toBeTruthy();
    // Radix Dialog closes on Escape by default.
    fireEvent.keyDown(dialog, { key: "Escape" });
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).toBeNull(),
    );
    expect(detailMock).toHaveBeenCalledTimes(1);
  });
});
