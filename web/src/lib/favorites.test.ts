// favorites lib tests (C7T4): pure helpers — star lookup and deep-link
// builders for the sidebar section.
import { describe, expect, it } from "vitest";
import {
  isStarred,
  issueFavoriteHref,
  projectFavoriteHref,
  type FavoritesList,
} from "./favorites";

function list(): FavoritesList {
  return {
    issues: [
      {
        id: "i1",
        display_id: "ENG-1",
        name: "Fix it",
        workspace_slug: "acme",
        project_id: "p1",
        project_identifier: "ENG",
        project_name: "Engineering",
        starred_at: "2026-10-09T00:00:00Z",
      },
    ],
    projects: [
      {
        id: "p2",
        identifier: "PLT",
        name: "Platform",
        workspace_slug: "acme",
        starred_at: "2026-10-09T00:00:00Z",
      },
    ],
  };
}

describe("isStarred", () => {
  it("finds starred issues and projects by id", () => {
    const l = list();
    expect(isStarred(l, "issue", "i1")).toBe(true);
    expect(isStarred(l, "project", "p2")).toBe(true);
  });

  it("returns false for unknown ids and wrong types", () => {
    const l = list();
    expect(isStarred(l, "issue", "p2")).toBe(false);
    expect(isStarred(l, "project", "i1")).toBe(false);
    expect(isStarred(l, "issue", "nope")).toBe(false);
  });

  it("returns false when the list is undefined", () => {
    expect(isStarred(undefined, "issue", "i1")).toBe(false);
  });
});

describe("favorite hrefs", () => {
  it("builds the issue detail deep link", () => {
    expect(issueFavoriteHref(list().issues[0])).toBe("/w/acme/p/ENG/i/i1");
  });

  it("builds the project deep link", () => {
    expect(projectFavoriteHref(list().projects[0])).toBe("/w/acme/p/PLT");
  });

  it("encodes slugs and identifiers", () => {
    const f = { ...list().issues[0], workspace_slug: "my ws" };
    expect(issueFavoriteHref(f)).toBe("/w/my%20ws/p/ENG/i/i1");
  });
});
