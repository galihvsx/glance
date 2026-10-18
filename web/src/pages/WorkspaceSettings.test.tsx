// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import WorkspaceSettings from "./WorkspaceSettings";
import { fetchWorkspaceAuditLog } from "../lib/workspaceAudit";

vi.mock("../lib/api", () => ({
  api: { get: vi.fn(), post: vi.fn(), patch: vi.fn(), del: vi.fn() },
  ApiError: class ApiError extends Error {},
}));
vi.mock("../lib/workspaceAudit", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../lib/workspaceAudit")>();
  return { ...actual, fetchWorkspaceAuditLog: vi.fn() };
});
vi.mock("../lib/export", () => ({ downloadExport: vi.fn() }));
vi.mock("../components/shell/PageHeader", () => ({
  default: () => null,
}));
vi.mock("../components/settings/WebhooksSection", () => ({
  default: () => null,
}));
vi.mock("../components/settings/SlackSection", () => ({
  default: () => null,
}));
vi.mock("../components/settings/DataSection", () => ({
  default: () => null,
}));
vi.mock("../components/settings/ProjectTemplatesSection", () => ({
  default: () => null,
}));

import { api } from "../lib/api";

const mockApiGet = vi.mocked(api.get);
const mockFetchAudit = vi.mocked(fetchWorkspaceAuditLog);

let currentRole = 20;

function renderSettings() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={["/w/acme/settings"]}>
        <Routes>
          <Route path="/w/:slug/settings" element={<WorkspaceSettings />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  currentRole = 20;
});

describe("WorkspaceSettings audit log visibility", () => {
  it("renders the Audit log section for admins", async () => {
    mockApiGet.mockImplementation((path: string) => {
      if (path.endsWith("/members")) return Promise.resolve({ members: [] });
      return Promise.resolve({
        id: "w1",
        slug: "acme",
        name: "Acme",
        role: currentRole,
        slack_configured: false,
    github_webhook_configured: false,
      });
    });
    mockFetchAudit.mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      per_page: 25,
    });
    renderSettings();
    expect(await screen.findByText("Audit log")).toBeTruthy();
    await waitFor(() => expect(mockFetchAudit).toHaveBeenCalled());
  });

  it("never renders the Audit log section for non-admins", async () => {
    currentRole = 15;
    mockApiGet.mockImplementation((path: string) => {
      if (path.endsWith("/members")) return Promise.resolve({ members: [] });
      return Promise.resolve({
        id: "w1",
        slug: "acme",
        name: "Acme",
        role: currentRole,
        slack_configured: false,
    github_webhook_configured: false,
      });
    });
    renderSettings();
    // The read-only notice renders; the audit section never mounts,
    // so it must not even fetch.
    expect(
      await screen.findByText(/only admins can change settings/),
    ).toBeTruthy();
    expect(screen.queryByText("Audit log")).toBeNull();
    expect(mockFetchAudit).not.toHaveBeenCalled();
  });
});
