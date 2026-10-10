// calendarFeed lib tests (C15T3): pure URL builders.

import { describe, expect, it } from "vitest";
import { cycleFeedURL, feedURLPattern, projectFeedURL } from "./calendarFeed";

describe("projectFeedURL", () => {
  it("builds the project feed URL with the token query param", () => {
    expect(
      projectFeedURL("https://glance.example", "acme", "GLA", "glcal_abc123"),
    ).toBe(
      "https://glance.example/api/v1/workspaces/acme/projects/GLA/calendar.ics?token=glcal_abc123",
    );
  });
  it("encodes path segments", () => {
    expect(projectFeedURL("https://x", "my ws", "GL A", "glcal_t")).toBe(
      "https://x/api/v1/workspaces/my%20ws/projects/GL%20A/calendar.ics?token=glcal_t",
    );
  });
});

describe("cycleFeedURL", () => {
  it("builds the cycle feed URL", () => {
    expect(
      cycleFeedURL(
        "https://glance.example",
        "acme",
        "GLA",
        "cyc-uuid-1",
        "glcal_abc123",
      ),
    ).toBe(
      "https://glance.example/api/v1/workspaces/acme/projects/GLA/cycles/cyc-uuid-1/calendar.ics?token=glcal_abc123",
    );
  });
});

describe("feedURLPattern", () => {
  it("shows the pattern without any secret", () => {
    const pattern = feedURLPattern("https://glance.example");
    expect(pattern).toContain("calendar.ics?token=<token>");
    expect(pattern).not.toContain("glcal_");
  });
});
