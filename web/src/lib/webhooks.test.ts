import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  buildCreateBody,
  deliveryPayloadJson,
  deliveryRowLabel,
  deliveryRowTone,
  deliveryStatusLabel,
  eventLabel,
  fetchWebhookDelivery,
  generateWebhookSecret,
  listWebhookDeliveries,
  redeliverWebhookDelivery,
  revealReducer,
  toggleEvent,
  truncateDeliveryError,
  validateWebhookUrl,
  WEBHOOK_EVENTS,
  type WebhookDeliveryDetail,
} from "./webhooks";
import { api } from "./api";

vi.mock("./api", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    del: vi.fn(),
  },
}));

describe("WEBHOOK_EVENTS vocabulary", () => {
  it("pins the exact backend event vocabulary (service.WebhookDomainEvents)", () => {
    // If the backend adds an event, the UI must add a checkbox here too —
    // this test fails loudly on drift.
    expect(WEBHOOK_EVENTS.map((e) => e.value)).toEqual([
      "issue.created",
      "issue.updated",
      "issue.deleted",
      "comment.created",
    ]);
  });

  it("labels every event", () => {
    for (const e of WEBHOOK_EVENTS) {
      expect(eventLabel(e.value)).toBe(e.label);
    }
  });

  it("falls back to the raw value for unknown events", () => {
    expect(eventLabel("something.new")).toBe("something.new");
  });
});

describe("toggleEvent", () => {
  it("adds an unselected event", () => {
    expect(toggleEvent([], "issue.created")).toEqual(["issue.created"]);
  });

  it("removes a selected event", () => {
    expect(toggleEvent(["issue.created", "comment.created"], "issue.created")).toEqual([
      "comment.created",
    ]);
  });

  it("keeps canonical order regardless of click order", () => {
    expect(toggleEvent(["comment.created"], "issue.created")).toEqual([
      "issue.created",
      "comment.created",
    ]);
  });

  it("drops unknown values", () => {
    expect(toggleEvent(["issue.created", "bogus"], "comment.created")).toEqual([
      "issue.created",
      "comment.created",
    ]);
  });

  it("toggling twice is a no-op", () => {
    const once = toggleEvent(["issue.updated"], "issue.created");
    expect(toggleEvent(once, "issue.created")).toEqual(["issue.updated"]);
  });
});

describe("validateWebhookUrl", () => {
  it("accepts https and http URLs", () => {
    expect(validateWebhookUrl("https://example.com/hook")).toBeNull();
    expect(validateWebhookUrl("http://example.com:8080/hook?x=1")).toBeNull();
  });

  it("rejects empty input", () => {
    expect(validateWebhookUrl("")).toBe("URL is required.");
    expect(validateWebhookUrl("   ")).toBe("URL is required.");
  });

  it("rejects non-http(s) schemes", () => {
    expect(validateWebhookUrl("ftp://example.com/hook")).toBe(
      "The URL must use http or https.",
    );
    expect(validateWebhookUrl("javascript:alert(1)")).not.toBeNull();
  });

  it("rejects unparseable input and missing hosts", () => {
    expect(validateWebhookUrl("not a url")).not.toBeNull();
    expect(validateWebhookUrl("http://")).not.toBeNull();
  });

  it("tolerates surrounding whitespace", () => {
    expect(validateWebhookUrl("  https://example.com/hook  ")).toBeNull();
  });
});

describe("buildCreateBody", () => {
  it("omits the secret key when empty so the server auto-generates one", () => {
    const body = buildCreateBody("https://example.com/hook", ["issue.created"], "", true);
    expect(body).not.toHaveProperty("secret");
    expect(body).toEqual({
      url: "https://example.com/hook",
      events: ["issue.created"],
      active: true,
    });
  });

  it("omits the secret key for whitespace-only input", () => {
    expect(
      buildCreateBody("https://example.com/hook", [], "   ", true),
    ).not.toHaveProperty("secret");
  });

  it("passes a provided secret through (trimmed)", () => {
    const body = buildCreateBody(
      "https://example.com/hook",
      [],
      "  my-secret  ",
      false,
    );
    expect(body.secret).toBe("my-secret");
    expect(body.active).toBe(false);
  });

  it("trims the URL and copies the events array", () => {
    const events = ["issue.created"];
    const body = buildCreateBody(" https://example.com/hook ", events, "", true);
    expect(body.url).toBe("https://example.com/hook");
    expect(body.events).toEqual(["issue.created"]);
    expect(body.events).not.toBe(events);
  });
});

describe("generateWebhookSecret", () => {
  it("produces 32-byte hex secrets (same shape as the server)", () => {
    const s = generateWebhookSecret();
    expect(s).toMatch(/^[0-9a-f]{64}$/);
  });

  it("generates unique secrets", () => {
    expect(generateWebhookSecret()).not.toBe(generateWebhookSecret());
  });
});

describe("revealReducer (show-once secret flow)", () => {
  it("holds the secret after reveal", () => {
    const state = revealReducer(null, {
      type: "revealed",
      kind: "create",
      secret: "s3cr3t",
    });
    expect(state).toEqual({ kind: "create", secret: "s3cr3t" });
  });

  it("a regenerate reveal replaces a create reveal", () => {
    const first = revealReducer(null, {
      type: "revealed",
      kind: "create",
      secret: "old",
    });
    const second = revealReducer(first, {
      type: "revealed",
      kind: "regenerate",
      secret: "new",
    });
    expect(second).toEqual({ kind: "regenerate", secret: "new" });
  });

  it("dismiss clears the secret from state — it is never shown again", () => {
    const state = revealReducer(
      { kind: "create", secret: "s3cr3t" },
      { type: "dismissed" },
    );
    expect(state).toBeNull();
  });
});

describe("deliveryStatusLabel (C57T2)", () => {
  const base = {
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
  const now = new Date("2026-10-18T00:05:00Z");

  it("delivered: green tone with a relative time", () => {
    const label = deliveryStatusLabel(
      {
        ...base,
        last_delivery_at: "2026-10-18T00:04:00Z",
        last_delivery_status: "delivered",
        last_error: null,
      },
      now,
    );
    expect(label.tone).toBe("success");
    expect(label.text).toBe("Delivered 1m ago");
  });

  it("retrying: amber tone with the truncated error", () => {
    const label = deliveryStatusLabel({
      ...base,
      last_delivery_at: "2026-10-18T00:04:00Z",
      last_delivery_status: "retrying",
      last_error: "webhook: https://example.com/hook returned status 500",
    });
    expect(label.tone).toBe("warning");
    expect(label.text).toBe(
      "Retrying — webhook: https://example.com/hook returned status 500",
    );
  });

  it("failed: red tone with the truncated error", () => {
    const label = deliveryStatusLabel({
      ...base,
      last_delivery_at: "2026-10-18T00:04:00Z",
      last_delivery_status: "failed",
      last_error: "webhook: target resolves to a non-public IP (SSRF guard)",
    });
    expect(label.tone).toBe("danger");
    expect(label.text).toBe(
      "Failed — webhook: target resolves to a non-public IP (SSRF guard)",
    );
  });

  it("never attempted: muted tone", () => {
    const label = deliveryStatusLabel({
      ...base,
      last_delivery_at: null,
      last_delivery_status: null,
      last_error: null,
    });
    expect(label.tone).toBe("muted");
    expect(label.text).toBe("No deliveries yet");
  });

  it("truncates long errors so the row stays compact", () => {
    const label = deliveryStatusLabel({
      ...base,
      last_delivery_at: "2026-10-18T00:04:00Z",
      last_delivery_status: "failed",
      last_error: "x".repeat(200),
    });
    expect(label.text.startsWith("Failed — ")).toBe(true);
    expect(label.text.length).toBeLessThan(200);
    expect(label.text.endsWith("…")).toBe(true);
  });

  it("a missing error never renders a dangling dash", () => {
    const label = deliveryStatusLabel({
      ...base,
      last_delivery_at: "2026-10-18T00:04:00Z",
      last_delivery_status: "retrying",
      last_error: null,
    });
    expect(label.text).toBe("Retrying — unknown error");
  });
});

describe("truncateDeliveryError", () => {
  it("passes short errors through untouched", () => {
    expect(truncateDeliveryError("boom")).toBe("boom");
  });

  it("truncates to 80 chars with an ellipsis", () => {
    const out = truncateDeliveryError("x".repeat(200));
    expect(out).toBe("x".repeat(79) + "…");
    expect(out.length).toBe(80);
  });
});

describe("deliveryRowTone / deliveryRowLabel (C59T1)", () => {
  it("maps each delivery status to the summary-indicator tone", () => {
    expect(deliveryRowTone("delivered")).toBe("success");
    expect(deliveryRowTone("retrying")).toBe("warning");
    expect(deliveryRowTone("failed")).toBe("danger");
  });

  it("labels each delivery status", () => {
    expect(deliveryRowLabel("delivered")).toBe("Delivered");
    expect(deliveryRowLabel("retrying")).toBe("Retrying");
    expect(deliveryRowLabel("failed")).toBe("Failed");
  });
});

describe("listWebhookDeliveries (C59T1)", () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset();
  });

  it("fetches the deliveries path with no query string by default", async () => {
    vi.mocked(api.get).mockResolvedValue({ deliveries: [], next_before: null });
    const page = await listWebhookDeliveries("acme", "wh-1");
    expect(api.get).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/webhooks/wh-1/deliveries",
    );
    expect(page).toEqual({ deliveries: [], next_before: null });
  });

  it("passes limit + before as query params for paging", async () => {
    vi.mocked(api.get).mockResolvedValue({ deliveries: [], next_before: 7 });
    await listWebhookDeliveries("acme", "wh-1", { limit: 25, before: 42 });
    expect(api.get).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/webhooks/wh-1/deliveries?limit=25&before=42",
    );
  });

  it("normalizes a missing deliveries/next_before to [] / null", async () => {
    vi.mocked(api.get).mockResolvedValue({});
    const page = await listWebhookDeliveries("acme", "wh-1");
    expect(page).toEqual({ deliveries: [], next_before: null });
  });
});

describe("redeliverWebhookDelivery (C59T1)", () => {
  beforeEach(() => {
    vi.mocked(api.post).mockReset();
  });

  it("POSTs to the redeliver path and returns {ok: true}", async () => {
    vi.mocked(api.post).mockResolvedValue({ ok: true });
    const res = await redeliverWebhookDelivery("acme", "wh-1", 123);
    expect(api.post).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/webhooks/wh-1/deliveries/123/redeliver",
      {},
    );
    expect(res).toEqual({ ok: true });
  });
});

const DETAIL: WebhookDeliveryDetail = {
  id: 30,
  webhook_id: "wh-1",
  event: "issue.created",
  status: "failed",
  http_status: 500,
  error: "boom: connection refused",
  duration_ms: 12,
  attempted_at: "2026-10-18T00:04:00Z",
  data: {
    issue: { id: "iss-1", title: "Fix it" },
    workspace: { id: "ws-1" },
  },
};

describe("fetchWebhookDelivery (C60T1)", () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset();
  });

  it("fetches the delivery-detail path and returns r.delivery", async () => {
    vi.mocked(api.get).mockResolvedValue({ delivery: DETAIL });
    const res = await fetchWebhookDelivery("acme", "wh-1", 30);
    expect(api.get).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/webhooks/wh-1/deliveries/30",
    );
    expect(res).toEqual(DETAIL);
  });

  it("normalizes a missing delivery to null", async () => {
    vi.mocked(api.get).mockResolvedValue({});
    expect(await fetchWebhookDelivery("acme", "wh-1", 30)).toBeNull();
  });
});

describe("deliveryPayloadJson (C60T1)", () => {
  it("pretty-prints the event + data as indented JSON", () => {
    expect(deliveryPayloadJson(DETAIL)).toBe(
      '{\n  "event": "issue.created",\n  "data": {\n    "issue": {\n      "id": "iss-1",\n      "title": "Fix it"\n    },\n    "workspace": {\n      "id": "ws-1"\n    }\n  }\n}',
    );
  });

  it("omits delivery bookkeeping fields (id, http_status, error)", () => {
    const json = deliveryPayloadJson(DETAIL);
    expect(json).not.toContain("http_status");
    expect(json).not.toContain("boom: connection refused");
  });
});
