import { describe, expect, it, vi, beforeEach } from "vitest";
import { ApiError, api } from "./api";
import {
  draftDescription,
  isAINotConfigured,
  isAIProviderFailure,
  probeAIAvailable,
  triageIssue,
} from "./ai";

vi.mock("./api", () => {
  class ApiError extends Error {
    readonly status: number;
    readonly body: string;
    constructor(status: number, body: string) {
      super(body || `Request failed with status ${status}`);
      this.name = "ApiError";
      this.status = status;
      this.body = body;
    }
  }
  return {
    ApiError,
    api: { get: vi.fn(), post: vi.fn(), patch: vi.fn() },
  };
});

const post = api.post as unknown as ReturnType<typeof vi.fn>;
const get = api.get as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  post.mockReset();
  get.mockReset();
});

describe("isAINotConfigured", () => {
  it("matches the honest-unconfigured 503", () => {
    expect(isAINotConfigured(new ApiError(503, "AI not configured"))).toBe(
      true,
    );
  });
  it("rejects provider failures and other statuses", () => {
    expect(isAIProviderFailure(new ApiError(502, "bad gateway"))).toBe(true);
    expect(isAINotConfigured(new ApiError(502, "bad gateway"))).toBe(false);
    expect(isAINotConfigured(new ApiError(404, "not found"))).toBe(false);
    expect(isAINotConfigured(new Error("boom"))).toBe(false);
    expect(isAINotConfigured(null)).toBe(false);
  });
});

describe("draftDescription", () => {
  it("posts the member-scoped body and returns the description", async () => {
    post.mockResolvedValue({ description: "drafted" });
    const desc = await draftDescription("acme", "GLA", "Broken login", "ctx");
    expect(desc).toBe("drafted");
    expect(post).toHaveBeenCalledWith("/api/v1/ai/draft", {
      workspace_slug: "acme",
      project_identifier: "GLA",
      title: "Broken login",
      context: "ctx",
    });
  });
  it("omits a blank context", async () => {
    post.mockResolvedValue({ description: "d" });
    await draftDescription("acme", "GLA", "t", "  ");
    expect(post).toHaveBeenCalledWith("/api/v1/ai/draft", {
      workspace_slug: "acme",
      project_identifier: "GLA",
      title: "t",
    });
  });
  it("propagates 503 so the UI can disable honestly", async () => {
    post.mockRejectedValue(new ApiError(503, "not configured"));
    await expect(draftDescription("acme", "GLA", "t")).rejects.toThrow();
  });
});

describe("triageIssue", () => {
  it("posts the member-scoped body and returns the taxonomy result", async () => {
    const result = { priority: 3, label_names: ["bug"], state_name: "Todo" };
    post.mockResolvedValue(result);
    const got = await triageIssue("acme", "GLA", "Broken login", "desc");
    expect(got).toEqual(result);
    expect(post).toHaveBeenCalledWith("/api/v1/ai/triage", {
      workspace_slug: "acme",
      project_identifier: "GLA",
      title: "Broken login",
      description: "desc",
    });
  });
  it("omits a missing description", async () => {
    post.mockResolvedValue({ priority: 0, label_names: [] });
    await triageIssue("acme", "GLA", "t");
    expect(post).toHaveBeenCalledWith("/api/v1/ai/triage", {
      workspace_slug: "acme",
      project_identifier: "GLA",
      title: "t",
    });
  });
});

describe("probeAIAvailable", () => {
  it("returns the server's configured flag", async () => {
    get.mockResolvedValue({ configured: false });
    await expect(probeAIAvailable()).resolves.toBe(false);
    expect(get).toHaveBeenCalledWith("/api/v1/ai/status");
  });
  it("resolves unknown (missing route / error) to enabled", async () => {
    get.mockRejectedValue(new ApiError(404, "not found"));
    await expect(probeAIAvailable()).resolves.toBe(true);
  });
});
