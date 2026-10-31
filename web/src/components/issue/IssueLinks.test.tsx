// @vitest-environment jsdom
// Component tests for the IssueLinks section (C94T2): the pre-existing
// cycles notice (present / absent) and the 409 cycle-path rendering in
// the add-link dialog. Other 409s keep their existing rendering.

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import IssueLinks from "./IssueLinks";
import { api, ApiError } from "../../lib/api";
import { makeQueryClient } from "../shell/test-utils";

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

const get = () => api.get as unknown as Mock;
const post = () => api.post as unknown as Mock;

const SLUG = "acme";
const IDENT = "ENG";
const CURRENT = "issue-current";
const PROJECT_BASE = `/api/v1/workspaces/${SLUG}/projects/${IDENT}`;

let mockCycles: string[][] = [];

const issueRow = (id: string, displayId: string, name: string) => ({
  id,
  project_id: "proj",
  sequence_id: Number(displayId.split("-")[1]),
  display_id: displayId,
  name,
  priority: 3,
  state_id: "state-1",
  sort_order: 1,
  is_draft: false,
});

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="location">{loc.pathname}</div>;
}

function setup() {
  get().mockImplementation((url: string) => {
    if (url.endsWith("/links/cycles")) {
      return Promise.resolve({ cycles: mockCycles });
    }
    if (url.includes("sequence_id=")) {
      const seq = new URLSearchParams(url.split("?")[1]).get("sequence_id");
      const row =
        seq === "1"
          ? issueRow("issue-1", "ENG-1", "First issue")
          : seq === "2"
            ? issueRow("issue-2", "ENG-2", "Second issue")
            : seq === "7"
              ? issueRow("issue-7", "ENG-7", "Target issue")
              : seq === "12"
                ? issueRow("issue-12", "ENG-12", "Twelfth issue")
                : null;
      return Promise.resolve({ results: row ? [row] : [] });
    }
    if (url.includes("/issues?")) {
      // Add-link dialog search.
      return Promise.resolve({
        results: [issueRow("issue-7", "ENG-7", "Target issue")],
      });
    }
    if (url.endsWith("/links")) {
      return Promise.resolve({ links: [] });
    }
    return Promise.resolve({});
  });

  render(
    <QueryClientProvider client={makeQueryClient()}>
      <MemoryRouter initialEntries={[`/w/${SLUG}/p/${IDENT}/i/${CURRENT}`]}>
        <LocationProbe />
        <IssueLinks
          slug={SLUG}
          identifier={IDENT}
          uuid={CURRENT}
          projectBase={PROJECT_BASE}
          onError={() => {}}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mockCycles = [];
});

describe("cycles notice", () => {
  it("lists each pre-existing cycle with links to its issues", async () => {
    mockCycles = [["ENG-1", "ENG-2", "ENG-1"]];
    setup();

    const notice = await screen.findByRole("note");
    expect(notice).toHaveAttribute(
      "aria-label",
      "Dependency cycles in this project",
    );
    expect(within(notice).getByText("ENG-2")).toBeInTheDocument();
    // The ring closes on ENG-1, so it appears twice, both linked.
    const firstLinks = within(notice).getAllByTitle("Open ENG-1");
    expect(firstLinks).toHaveLength(2);

    fireEvent.click(firstLinks[0]);
    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent(
        `/w/${SLUG}/p/${IDENT}/i/issue-1`,
      ),
    );
  });

  it("renders no notice when the project is clean", async () => {
    mockCycles = [];
    setup();

    await screen.findByText(/No linked issues yet/);
    expect(screen.queryByRole("note")).toBeNull();
  });

  it("renders no notice when the cycles report fails", async () => {
    get().mockImplementation((url: string) => {
      if (url.endsWith("/links/cycles")) {
        return Promise.reject(new ApiError(404, "not found"));
      }
      if (url.endsWith("/links")) {
        return Promise.resolve({ links: [] });
      }
      return Promise.resolve({});
    });
    render(
      <QueryClientProvider client={makeQueryClient()}>
        <MemoryRouter initialEntries={[`/w/${SLUG}/p/${IDENT}/i/${CURRENT}`]}>
          <IssueLinks
            slug={SLUG}
            identifier={IDENT}
            uuid={CURRENT}
            projectBase={PROJECT_BASE}
            onError={() => {}}
          />
        </MemoryRouter>
      </QueryClientProvider>,
    );

    await screen.findByText(/No linked issues yet/);
    expect(screen.queryByRole("note")).toBeNull();
  });
});

describe("add-link cycle error", () => {
  async function openDialogAndSelectTarget() {
    setup();
    await screen.findByText(/No linked issues yet/);
    // The section button and the dialog submit share the label "Add
    // link"; the section button is the only one before the dialog opens.
    fireEvent.click(screen.getByRole("button", { name: "Add link" }));
    const search = await screen.findByPlaceholderText("Search issues…");
    fireEvent.change(search, { target: { value: "Target" } });
    // JSX strips the newline between badge and title, so the accessible
    // name is "ENG-7Target issue" (no space) — match on the title.
    const result = await screen.findByRole("button", {
      name: /Target issue/,
    });
    fireEvent.click(result);
  }

  it("renders the 409 cycle path verbatim in the dialog", async () => {
    await openDialogAndSelectTarget();
    post().mockRejectedValueOnce(
      new ApiError(
        409,
        "adding this link would create a cycle: ENG-12 → ENG-7 → ENG-12",
        "issue_link_cycle",
        { path: ["ENG-12", "ENG-7", "ENG-12"] },
      ),
    );
    // The dialog submit is the last "Add link" button in document order
    // (the dialog portal renders after the section button).
    const submits = screen.getAllByRole("button", { name: "Add link" });
    fireEvent.click(submits[submits.length - 1]);
    await screen.findByRole("alert");
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Adding this link would create a cycle: ENG-12 → ENG-7 → ENG-12",
    );
  });

  it("keeps other 409s on their existing rendering", async () => {
    await openDialogAndSelectTarget();
    post().mockRejectedValueOnce(
      new ApiError(409, "issue link already exists", "conflict"),
    );
    const submits = screen.getAllByRole("button", { name: "Add link" });
    fireEvent.click(submits[submits.length - 1]);
    await screen.findByRole("alert");
    expect(screen.getByRole("alert")).toHaveTextContent(
      "issue link already exists",
    );
  });
});
