import { describe, expect, it } from "vitest";
import {
  buildCreateBody,
  eventLabel,
  generateWebhookSecret,
  revealReducer,
  toggleEvent,
  validateWebhookUrl,
  WEBHOOK_EVENTS,
} from "./webhooks";

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
