// @vitest-environment jsdom
// Component tests for the page-detail "Linked issues" section (C36T1):
// list render + navigation, empty state, role gating, unlink confirm,
// link picker scoping, and the create-from-page payload shape.

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
import PageLinkedIssues from "./PageLinkedIssues";
import { api } from "../../lib/api";
import { makeQueryClient } from "../shell/test-utils";
import type { Member, Page, PageLinkedIssue } from "../../lib/types";

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
let linkedIssues: PageLinkedIssue[] = [];
const searchIssues = [
  {
    id: "iss-9",
    display_id: "ENG-9",
    name: "Searchable issue",
    is_draft: false,
    archived_at: null,
  },
  {
    id: "iss-draft",
    display_id: "ENG-10",
    name: "Draft issue",
    is_draft: true,
    archived_at: null,
  },
];

const page: Page = {
  id: "page-1",
  project_id: "proj",
  parent_id: null,
  title: "API auth runbook",
  content: "# Runbook",
  position: 1,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

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
    if (url.includes("/pages/") && url.includes("/issues")) {
      return Promise.resolve({ issues: linkedIssues });
    }
    if (url.includes("/issues")) {
      return Promise.resolve({ results: searchIssues });
    }
    return Promise.resolve({});
  });
  post().mockImplementation((url: string) => {
    if (url.includes("/pages/")) {
      return Promise.resolve({ id: "link-1" });
    }
    if (url.endsWith("/issues")) {
      return Promise.resolve({ id: "new-issue-id", display_id: "ENG-99" });
    }
    return Promise.resolve({});
  });
  del().mockResolvedValue({});
  const qc = makeQueryClient();
  render(
    <MemoryRouter initialEntries={["/w/ws/p/ENG/pages"]}>
      <QueryClientProvider client={qc}>
        <LocationProbe />
        <PageLinkedIssues slug="ws" identifier="ENG" page={page} />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.restoreAllMocks();
  mockRole = 15;
  linkedIssues = [];
});

describe("PageLinkedIssues", () => {
  it("renders linked issues with state badge, display id, and title", async () => {
    linkedIssues = [
      {
        id: "iss-1",
        display_id: "ENG-45",
        title: "Onboarding wizard polish",
        state: "In Progress",
        state_id: "s1",
        priority: 2,
      },
    ];
    setup();
    expect(await screen.findByText("Onboarding wizard polish")).toBeInTheDocument();
    expect(screen.getByText("ENG-45")).toBeInTheDocument();
    expect(screen.getByText("In Progress")).toBeInTheDocument();
  });

  it("navigates to the issue detail on row click", async () => {
    linkedIssues = [
      {
        id: "iss-1",
        display_id: "ENG-45",
        title: "Onboarding wizard polish",
        state: "In Progress",
        state_id: "s1",
        priority: 2,
      },
    ];
    setup();
    fireEvent.click(await screen.findByText("Onboarding wizard polish"));
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/w/ws/p/ENG/i/iss-1",
      ),
    );
  });

  it("shows the empty state when nothing is linked", async () => {
    setup();
    expect(await screen.findByText("No linked issues yet.")).toBeInTheDocument();
  });

  it("hides link/unlink/create for viewers (role < 15)", async () => {
    mockRole = 5;
    linkedIssues = [
      {
        id: "iss-1",
        display_id: "ENG-45",
        title: "Onboarding wizard polish",
        state: "In Progress",
        state_id: "s1",
        priority: 2,
      },
    ];
    setup();
    await screen.findByText("Onboarding wizard polish");
    expect(screen.queryByRole("button", { name: /Link issue/ })).toBeNull();
    expect(
      screen.queryByRole("button", { name: /Create issue from this page/ }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: /Unlink ENG-45/ }),
    ).toBeNull();
  });

  it("unlinks with confirm", async () => {
    linkedIssues = [
      {
        id: "iss-1",
        display_id: "ENG-45",
        title: "Onboarding wizard polish",
        state: "In Progress",
        state_id: "s1",
        priority: 2,
      },
    ];
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    setup();
    fireEvent.click(
      await screen.findByRole("button", { name: /Unlink ENG-45/ }),
    );
    expect(confirm).toHaveBeenCalled();
    await waitFor(() =>
      expect(del()).toHaveBeenCalledWith(
        expect.stringContaining("/pages/page-1/issues/iss-1"),
      ),
    );
  });

  it("links an issue from the picker, excluding drafts", async () => {
    setup();
    fireEvent.click(
      await screen.findByRole("button", { name: "Link issue" }),
    );
    const dialog = await screen.findByRole("dialog");
    expect(
      await within(dialog).findByText("Searchable issue"),
    ).toBeInTheDocument();
    // Drafts are not linkable targets.
    expect(within(dialog).queryByText("Draft issue")).toBeNull();
    fireEvent.click(within(dialog).getByText("Searchable issue"));
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Link issue" }),
    );
    await waitFor(() =>
      expect(post()).toHaveBeenCalledWith(
        expect.stringContaining("/pages/page-1/issues"),
        { issue_id: "iss-9" },
      ),
    );
  });

  it("create-from-page posts title + 'From page' description, links, navigates", async () => {
    setup();
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Create issue from this page",
      }),
    );
    await waitFor(() =>
      expect(post()).toHaveBeenCalledWith(
        expect.stringMatching(/\/issues$/),
        expect.objectContaining({ name: "API auth runbook" }),
      ),
    );
    const createCall = post().mock.calls.find(
      (call) => typeof call[0] === "string" && !call[0].includes("/pages/"),
    );
    const body = createCall?.[1] as {
      description: {
        content: { content: { text: string }[] }[];
      };
    };
    const paragraphs = body.description.content.map(
      (p) => p.content[0].text,
    );
    expect(paragraphs[0]).toBe("From page: API auth runbook");
    expect(paragraphs[1]).toContain("/w/ws/p/ENG/pages");
    await waitFor(() =>
      expect(post()).toHaveBeenCalledWith(
        expect.stringContaining("/pages/page-1/issues"),
        { issue_id: "new-issue-id" },
      ),
    );
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/w/ws/p/ENG/i/new-issue-id",
      ),
    );
  });
});
