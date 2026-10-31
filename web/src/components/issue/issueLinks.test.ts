import { describe, expect, it } from "vitest";
import { ApiError } from "../../lib/api";
import {
  LINK_KINDS,
  cycleErrorMessage,
  formatCyclePath,
  groupLinks,
  issueLinkCyclesPath,
  issueLinksPath,
  linkGroupKey,
  linkGroupLabel,
  linksTitle,
  otherIssueId,
  removeLinkMessage,
  type IssueLink,
} from "./issueLinks";

const CUR = "issue-a";
const link = (over: Partial<IssueLink> = {}): IssueLink => ({
  id: "link-1",
  issue_id: CUR,
  target_issue_id: "issue-b",
  kind: "blocks",
  created_at: "2026-10-09T10:00:00Z",
  ...over,
});

describe("LINK_KINDS", () => {
  it("matches the backend service vocabulary exactly", () => {
    // service.issueLinkKinds — the handler 400s anything else.
    expect(LINK_KINDS.map((k) => k.value)).toEqual([
      "blocks",
      "relates_to",
      "duplicates",
    ]);
  });
});

describe("otherIssueId", () => {
  it("returns the target for an outgoing edge", () => {
    expect(otherIssueId(link(), CUR)).toBe("issue-b");
  });

  it("returns the source for an incoming edge", () => {
    expect(
      otherIssueId(
        link({ issue_id: "issue-b", target_issue_id: CUR }),
        CUR,
      ),
    ).toBe("issue-b");
  });
});

describe("linkGroupKey", () => {
  it("splits the directed kinds by side", () => {
    expect(linkGroupKey(link({ kind: "blocks" }), CUR)).toBe("blocks");
    expect(
      linkGroupKey(link({ issue_id: "x", target_issue_id: CUR, kind: "blocks" }), CUR),
    ).toBe("blocked_by");
    expect(linkGroupKey(link({ kind: "duplicates" }), CUR)).toBe("duplicates");
    expect(
      linkGroupKey(
        link({ issue_id: "x", target_issue_id: CUR, kind: "duplicates" }),
        CUR,
      ),
    ).toBe("duplicated_by");
  });

  it("keeps relates_to symmetric", () => {
    expect(linkGroupKey(link({ kind: "relates_to" }), CUR)).toBe("relates_to");
    expect(
      linkGroupKey(
        link({ issue_id: "x", target_issue_id: CUR, kind: "relates_to" }),
        CUR,
      ),
    ).toBe("relates_to");
  });

  it("passes unknown future kinds through raw", () => {
    expect(linkGroupKey(link({ kind: "parent_of" }), CUR)).toBe("parent_of");
  });
});

describe("linkGroupLabel", () => {
  it("humanizes the five backend groups", () => {
    expect(linkGroupLabel("blocks")).toBe("Blocks");
    expect(linkGroupLabel("blocked_by")).toBe("Is blocked by");
    expect(linkGroupLabel("relates_to")).toBe("Relates to");
    expect(linkGroupLabel("duplicates")).toBe("Duplicates");
    expect(linkGroupLabel("duplicated_by")).toBe("Duplicated by");
  });

  it("humanizes unknown kinds instead of rendering blank", () => {
    expect(linkGroupLabel("parent_of")).toBe("Parent Of");
  });
});

describe("groupLinks", () => {
  it("returns no groups for no links", () => {
    expect(groupLinks([], CUR)).toEqual([]);
  });

  it("groups by side and orders groups canonically", () => {
    const links = [
      link({ id: "l1", kind: "duplicates" }),
      link({ id: "l2", issue_id: "x", target_issue_id: CUR, kind: "blocks" }),
      link({ id: "l3", kind: "blocks" }),
      link({ id: "l4", kind: "relates_to" }),
    ];
    const groups = groupLinks(links, CUR);
    expect(groups.map((g) => g.key)).toEqual([
      "blocks",
      "blocked_by",
      "relates_to",
      "duplicates",
    ]);
    expect(groups.map((g) => g.label)).toEqual([
      "Blocks",
      "Is blocked by",
      "Relates to",
      "Duplicates",
    ]);
    expect(groups[0].links.map((l) => l.id)).toEqual(["l3"]);
    expect(groups[1].links.map((l) => l.id)).toEqual(["l2"]);
  });

  it("trails unknown-kind groups alphabetically after the known ones", () => {
    const links = [
      link({ id: "l1", kind: "zz_kind" }),
      link({ id: "l2", kind: "blocks" }),
      link({ id: "l3", kind: "aa_kind" }),
    ];
    const groups = groupLinks(links, CUR);
    expect(groups.map((g) => g.key)).toEqual(["blocks", "aa_kind", "zz_kind"]);
    expect(groups[1].label).toBe("Aa Kind");
  });

  it("preserves edge order within a group", () => {
    const links = [
      link({ id: "l1", kind: "blocks", target_issue_id: "b" }),
      link({ id: "l2", kind: "blocks", target_issue_id: "c" }),
    ];
    const groups = groupLinks(links, CUR);
    expect(groups[0].links.map((l) => l.id)).toEqual(["l1", "l2"]);
  });
});

describe("linksTitle", () => {
  it("is plain with no links and carries the count otherwise", () => {
    expect(linksTitle(0)).toBe("Linked issues");
    expect(linksTitle(3)).toBe("Linked issues (3)");
  });
});

describe("removeLinkMessage", () => {
  it("names the issue and states the outcome", () => {
    const msg = removeLinkMessage("Fix the flaky test");
    expect(msg).toContain("Fix the flaky test");
    expect(msg).toContain("not affected");
  });
});

describe("issueLinksPath", () => {
  it("builds the nested route and encodes path segments", () => {
    expect(issueLinksPath("my ws", "GLA", "uuid-1")).toBe(
      "/api/v1/workspaces/my%20ws/projects/GLA/issues/uuid-1/links",
    );
  });
});

describe("issueLinkCyclesPath", () => {
  it("builds the project-scoped cycles route and encodes segments", () => {
    expect(issueLinkCyclesPath("my ws", "ENG")).toBe(
      "/api/v1/workspaces/my%20ws/projects/ENG/links/cycles",
    );
  });
});

describe("formatCyclePath", () => {
  it("joins a closed ring verbatim with arrows", () => {
    expect(formatCyclePath(["ENG-12", "ENG-7", "ENG-12"])).toBe(
      "ENG-12 → ENG-7 → ENG-12",
    );
  });
});

describe("cycleErrorMessage", () => {
  it("renders a 409 issue_link_cycle path verbatim", () => {
    const err = new ApiError(
      409,
      "adding this link would create a cycle: ENG-12 → ENG-7 → ENG-12",
      "issue_link_cycle",
      { path: ["ENG-12", "ENG-7", "ENG-12"] },
    );
    expect(cycleErrorMessage(err)).toBe(
      "Adding this link would create a cycle: ENG-12 → ENG-7 → ENG-12",
    );
  });

  it("falls back to the backend message when the cycle code has no path", () => {
    const err = new ApiError(
      409,
      "adding this link would create a cycle",
      "issue_link_cycle",
    );
    expect(cycleErrorMessage(err)).toBe(
      "adding this link would create a cycle",
    );
  });

  it("leaves other 409s untouched so their rendering is unchanged", () => {
    expect(
      cycleErrorMessage(
        new ApiError(409, "issue link already exists", "conflict"),
      ),
    ).toBeNull();
    expect(
      cycleErrorMessage(
        new ApiError(409, "issue cannot link to itself", "conflict"),
      ),
    ).toBeNull();
  });

  it("returns null for non-cycle errors and non-ApiError values", () => {
    expect(cycleErrorMessage(new ApiError(403, "forbidden"))).toBeNull();
    expect(cycleErrorMessage(new Error("boom"))).toBeNull();
    expect(cycleErrorMessage(null)).toBeNull();
  });
});
