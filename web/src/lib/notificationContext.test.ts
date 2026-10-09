import { describe, expect, it } from "vitest";
import {
  actorDisplayName,
  actorInitial,
  issueLink,
  type NotificationContext,
} from "./notificationContext";

function makeCtx(): NotificationContext {
  return {
    projects: new Map([
      [
        "proj-1",
        { slug: "acme", identifier: "GLA", name: "Glance" },
      ],
    ]),
    actors: new Map(),
  };
}

describe("actorDisplayName", () => {
  it("prefers name, falls back to email local part", () => {
    expect(
      actorDisplayName({ name: "Alice", email: "alice@example.com" }),
    ).toBe("Alice");
    expect(
      actorDisplayName({ name: null, email: "bob@example.com" }),
    ).toBe("bob");
    expect(actorDisplayName({ name: "  ", email: "" })).toBe("Someone");
  });
});

describe("actorInitial", () => {
  it("returns the uppercased first letter of the display name", () => {
    expect(actorInitial({ name: "Alice", email: "a@x.com" })).toBe("A");
    expect(actorInitial({ name: null, email: "bob@x.com" })).toBe("B");
  });
});

describe("issueLink", () => {
  it("builds the issue route from resolved project context", () => {
    expect(
      issueLink(makeCtx(), {
        project_id: "proj-1",
        issue_id: "issue-9",
      }),
    ).toBe("/w/acme/p/GLA/i/issue-9");
  });

  it("URL-encodes route parts", () => {
    const ctx = makeCtx();
    ctx.projects.set("proj-2", {
      slug: "my ws",
      identifier: "G/L",
      name: "x",
    });
    expect(
      issueLink(ctx, { project_id: "proj-2", issue_id: "a b" }),
    ).toBe("/w/my%20ws/p/G%2FL/i/a%20b");
  });

  it("returns null when the project or issue id is missing or unknown", () => {
    const ctx = makeCtx();
    expect(issueLink(ctx, { project_id: "proj-1" })).toBeNull();
    expect(issueLink(ctx, { issue_id: "issue-9" })).toBeNull();
    expect(
      issueLink(ctx, { project_id: "gone", issue_id: "issue-9" }),
    ).toBeNull();
    expect(issueLink(ctx, {})).toBeNull();
  });
});
