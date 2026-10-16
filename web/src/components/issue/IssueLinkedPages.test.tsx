// @vitest-environment jsdom
// Component tests for the issue-detail "Linked pages" section (C36T1):
// list render + navigation (Pages view with the page selected), empty
// state, role gating, unlink confirm, and the tree-flattened page
// picker.

import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi, type Mock } from "vitest";
import IssueLinkedPages from "./IssueLinkedPages";
import { api } from "../../lib/api";
import { makeQueryClient } from "../shell/test-utils";
import type { Member, Page, PageLinkedPage } from "../../lib/types";

vi.mock("../../lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...mod,
    api: {
      get: vi.fn(),
      post: vi.fn(),
      put: vi.fn(),
      patch: vi.fn(),
      del: vi.fn(),
    },
  };
});

vi.mock("../../lib/auth", () => ({
  useAuth: () => ({
    user: {
      id: "u-me",
      email: "me@example.com",
      name: "Me",
      avatar_url: null,
      is_active: true,
      is_admin: false,
      last_login_at: null,
      created_at: "",
      updated_at: "",
    },
    loading: false,
    refresh: vi.fn(),
    logout: vi.fn(),
  }),
}));

const get = () => api.get as unknown as Mock;
const post = () => api.post as unknown as Mock;
const del = () => api.del as unknown as Mock;

let mockRole = 15;
let linkedPages: PageLinkedPage[] = [];
const allPages: Page[] = [
  {
    id: "page-1",
    project_id: "proj",
    parent_id: null,
    title: "API auth runbook",
    content: "",
    position: 1,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
  {
    id: "page-child",
    project_id: "proj",
    parent_id: "page-1",
    title: "Child page",
    content: "",
    position: 1,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  },
];

function LocationProbe() {
  const loc = useLocation();
  return (
    <div data-testid="location">
      {loc.pathname}|{JSON.stringify(loc.state ?? null)}
    </div>
  );
}

function setup() {
  get().mockImplementation((url: string) => {
    if (url.includes("/members")) {
      const members: Member[] = [
        { id: "u-me", email: "me@example.com", role: mockRole },
      ];
      return Promise.resolve({ members });
    }
    if (url.includes("/issues/") && url.endsWith("/pages")) {
      return Promise.resolve({ pages: linkedPages });
    }
    if (url.endsWith("/pages")) {
      return Promise.resolve({ pages: allPages });
    }
    return Promise.resolve({});
  });
  post().mockResolvedValue({ id: "link-1" });
  del().mockResolvedValue({});
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/ws/p/ENG/i/iss-1"]}>
      <QueryClientProvider client={qc}>
        <LocationProbe />
        <IssueLinkedPages
          slug="ws"
          identifier="ENG"
          uuid="iss-1"
          onError={vi.fn()}
        />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.restoreAllMocks();
  mockRole = 15;
  linkedPages = [];
});

describe("IssueLinkedPages", () => {
  it("renders linked pages with title and relative updated time", async () => {
    linkedPages = [
      {
        id: "page-1",
        title: "API auth runbook",
        updated_at: new Date(Date.now() - 2 * 3600 * 1000).toISOString(),
      },
    ];
    setup();
    expect(await screen.findByText("API auth runbook")).toBeInTheDocument();
    expect(screen.getByText("updated 2h ago")).toBeInTheDocument();
  });

  it("navigates to the Pages view with the page selected", async () => {
    linkedPages = [
      {
        id: "page-1",
        title: "API auth runbook",
        updated_at: new Date().toISOString(),
      },
    ];
    setup();
    fireEvent.click(await screen.findByText("API auth runbook"));
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/w/ws/p/ENG/pages",
      ),
    );
    expect(screen.getByTestId("location")).toHaveTextContent(
      `{"pageId":"page-1"}`,
    );
  });

  it("shows the empty state when nothing is linked", async () => {
    setup();
    expect(await screen.findByText("No linked pages yet.")).toBeInTheDocument();
  });

  it("hides link/unlink for viewers (role < 15)", async () => {
    mockRole = 5;
    linkedPages = [
      { id: "page-1", title: "API auth runbook", updated_at: new Date().toISOString() },
    ];
    setup();
    await screen.findByText("API auth runbook");
    expect(screen.queryByRole("button", { name: "Link page" })).toBeNull();
    expect(
      screen.queryByRole("button", { name: /Unlink page/ }),
    ).toBeNull();
  });

  it("unlinks with confirm", async () => {
    linkedPages = [
      { id: "page-1", title: "API auth runbook", updated_at: new Date().toISOString() },
    ];
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    setup();
    fireEvent.click(
      await screen.findByRole("button", {
        name: 'Unlink page "API auth runbook"',
      }),
    );
    expect(confirm).toHaveBeenCalled();
    await waitFor(() =>
      expect(del()).toHaveBeenCalledWith(
        expect.stringContaining("/issues/iss-1/pages/page-1"),
      ),
    );
  });

  it("links a page from the tree-flattened picker", async () => {
    linkedPages = [
      { id: "page-1", title: "API auth runbook", updated_at: new Date().toISOString() },
    ];
    setup();
    fireEvent.click(await screen.findByRole("button", { name: "Link page" }));
    const dialog = await screen.findByRole("dialog");
    // Already-linked pages are excluded from the picker.
    expect(within(dialog).queryByText("API auth runbook")).toBeNull();
    expect(
      await within(dialog).findByText(/Child page/),
    ).toBeInTheDocument();
    fireEvent.click(within(dialog).getByText(/Child page/));
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Link page" }),
    );
    await waitFor(() =>
      expect(post()).toHaveBeenCalledWith(
        expect.stringContaining("/issues/iss-1/pages"),
        { page_id: "page-child" },
      ),
    );
  });

  it("filters the page picker by title", async () => {
    setup();
    fireEvent.click(await screen.findByRole("button", { name: "Link page" }));
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByText(/Child page/);
    fireEvent.change(within(dialog).getByPlaceholderText("Search pages…"), {
      target: { value: "Child" },
    });
    await waitFor(() =>
      expect(within(dialog).queryByText(/API auth runbook/)).toBeNull(),
    );
    expect(within(dialog).getByText(/Child page/)).toBeInTheDocument();
  });
});
