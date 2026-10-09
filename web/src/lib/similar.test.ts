import { describe, expect, it, vi } from "vitest";

// C8T7 duplicate detection: unit tests for the suggestion-hook support
// module (path building, minimum-title gate, debounce timing, fetcher
// contract). The debounce gating lives in shouldFetchSimilar so the
// timing behavior is testable without a DOM.

vi.mock("./api", () => ({
  api: {
    get: vi.fn(),
  },
}));

import { api } from "./api";
import {
  SIMILAR_DEBOUNCE_MS,
  SIMILAR_MIN_TITLE_LEN,
  fetchSimilarIssues,
  shouldFetchSimilar,
  similarIssuesPath,
  type SimilarIssue,
} from "./similar";

const getMock = api.get as unknown as ReturnType<typeof vi.fn>;

describe("similarIssuesPath", () => {
  it("builds the C8T7 endpoint path", () => {
    expect(similarIssuesPath("acme", "ENG")).toBe(
      "/api/v1/workspaces/acme/projects/ENG/issues/similar",
    );
  });

  it("URL-encodes path segments", () => {
    expect(similarIssuesPath("my ws", "ENG")).toBe(
      "/api/v1/workspaces/my%20ws/projects/ENG/issues/similar",
    );
  });
});

describe("shouldFetchSimilar", () => {
  it("gates titles below the trigram minimum", () => {
    expect(shouldFetchSimilar("")).toBe(false);
    expect(shouldFetchSimilar("   ")).toBe(false);
    expect(shouldFetchSimilar("ab")).toBe(false);
    expect(shouldFetchSimilar("abc")).toBe(true);
    expect(shouldFetchSimilar("  Fix the login redirect  ")).toBe(true);
  });

  it("pins the minimum length to 3", () => {
    expect(SIMILAR_MIN_TITLE_LEN).toBe(3);
  });
});

describe("SIMILAR_DEBOUNCE_MS", () => {
  it("is 400ms", () => {
    expect(SIMILAR_DEBOUNCE_MS).toBe(400);
  });
});

describe("fetchSimilarIssues", () => {
  it("queries the similar endpoint with the encoded title", async () => {
    const hits: SimilarIssue[] = [
      {
        id: "11111111-1111-1111-1111-111111111111",
        display_id: "ENG-7",
        name: "Fix the login redirect loop",
        state: "Backlog",
        similarity: 0.82,
      },
    ];
    getMock.mockResolvedValue(hits);

    const got = await fetchSimilarIssues("acme", "ENG", "Fix login redirect");

    expect(getMock).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/issues/similar?q=Fix+login+redirect",
    );
    expect(got).toEqual(hits);
    expect(got[0].similarity).toBeGreaterThan(0.3);
  });

  it("trims the title before sending", async () => {
    getMock.mockResolvedValue([]);
    await fetchSimilarIssues("acme", "ENG", "  login loop  ");
    expect(getMock).toHaveBeenCalledWith(
      "/api/v1/workspaces/acme/projects/ENG/issues/similar?q=login+loop",
    );
  });
});
