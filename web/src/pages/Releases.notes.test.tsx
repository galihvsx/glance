// @vitest-environment jsdom
// C33T1: release-notes dialog — verbatim Markdown, Copy/Download use the
// same string, disabled states, filename slug (same ASCII rule as C33T0).
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import Releases, {
  downloadTextFile,
  releaseNotesFilename,
  slugifyExportName,
} from "./Releases";
import { api, ApiError } from "../lib/api";
import { copyText } from "../lib/clipboard";
import { makeQueryClient } from "../components/shell/test-utils";
import type { Issue, IssueState, Release, ReleaseNotes } from "../lib/types";

// Same pattern as IssueCopyMenu.test.tsx: mock the clipboard helper,
// not navigator.clipboard itself.
vi.mock("../lib/clipboard", () => ({ copyText: vi.fn() }));

const release: Release = {
  id: "rel-1",
  project_id: "p",
  name: "v2.0",
  description: "Second milestone",
  status: "planned",
  release_date: "2026-12-01",
  issue_count: 1,
  progress_snapshot: {
    triage: 0,
    backlog: 1,
    unstarted: 0,
    started: 0,
    completed: 0,
    cancelled: 0,
  },
  created_at: "2026-01-01",
  updated_at: "2026-01-01",
};

const states: IssueState[] = [
  {
    id: "st-backlog",
    project_id: "p",
    name: "Backlog",
    group: "backlog",
    color: "#000",
    sequence: 0,
    created_at: "2026-01-01",
    updated_at: "2026-01-01",
  },
];

const issues: Issue[] = [
  {
    id: "iss-1",
    project_id: "p",
    sequence_id: 1,
    display_id: "ENG-1",
    name: "Notes issue",
    priority: 0,
    state_id: "st-backlog",
    sort_order: 0,
    is_draft: false,
    created_by: "u",
    created_at: "2026-01-01",
    updated_at: "2026-01-01",
    assignees: [],
    labels: [],
  },
];

const MARKDOWN =
  "# Release notes — v2.0\n\nRelease date: 2026-12-01\n\nSecond milestone\n\n## Backlog (1)\n- ENG-1 Notes issue\n";

const notes: ReleaseNotes = {
  release: {
    id: "rel-1",
    name: "v2.0",
    status: "planned",
    release_date: "2026-12-01",
    description: "Second milestone",
  },
  markdown: MARKDOWN,
  counts: { completed: 0, started: 0, unstarted: 1, total: 1 },
};

function renderPage(opts: {
  seedIssues?: boolean;
  seedNotes?: boolean;
} = {}) {
  const seeds: [readonly unknown[], unknown][] = [
    [["releases", "acme", "ENG"], [release]],
    [["states", "acme", "ENG"], states],
  ];
  if (opts.seedIssues ?? true) {
    seeds.push([["release-issues", "acme", "ENG", "rel-1"], issues]);
  }
  if (opts.seedNotes) {
    seeds.push([["release-notes", "acme", "ENG", "rel-1"], notes]);
  }
  const qc = makeQueryClient(seeds);
  return render(
    <MemoryRouter initialEntries={["/w/acme/p/ENG/releases"]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path="/w/:slug/p/:identifier/releases"
            element={<Releases />}
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

function selectRelease() {
  fireEvent.click(
    screen.getByRole("button", { name: "Open release v2.0" }),
  );
}

function stubClipboard() {
  const mocked = vi.mocked(copyText);
  mocked.mockResolvedValue(true);
  return mocked;
}

/** The dialog's Markdown block: found by tag, not text match —
// testing-library's default normalizer collapses the Markdown's
// newlines, so a text-content matcher would never equal it. */
function findNotesPre() {
  return screen.findByText(
    (_content, el) => el?.tagName === "PRE",
    {},
    { timeout: 3000 },
  );
}

function stubObjectUrl() {
  const blobs: Blob[] = [];
  vi.spyOn(URL, "createObjectURL").mockImplementation((blob) => {
    blobs.push(blob as Blob);
    return "blob:mock";
  });
  vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
  return blobs;
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("releaseNotesFilename", () => {
  it("slugs per the shared ASCII export rule", () => {
    expect(releaseNotesFilename("v2.0 Release!")).toBe(
      "v2-0-release-release-notes.md",
    );
    expect(releaseNotesFilename("  Spaces  And---Dashes ")).toBe(
      "spaces-and-dashes-release-notes.md",
    );
    expect(releaseNotesFilename("Ünïcödé")).toBe("n-c-d-release-notes.md");
    expect(releaseNotesFilename("")).toBe("release-notes.md");
    expect(releaseNotesFilename("!!!")).toBe("release-notes.md");
  });

  it("slugifyExportName collapses runs and trims dashes", () => {
    expect(slugifyExportName("A  B--C")).toBe("a-b-c");
    expect(slugifyExportName("--x--")).toBe("x");
  });
});

describe("downloadTextFile", () => {
  it("downloads the exact text under the given filename", async () => {
    const blobs = stubObjectUrl();
    const appended: Element[] = [];
    vi.spyOn(document.body, "appendChild").mockImplementation(
      (node) => appended.push(node as Element) as unknown as Node,
    );
    downloadTextFile("v2-0-release-notes.md", MARKDOWN);
    expect(blobs).toHaveLength(1);
    expect(await blobs[0].text()).toBe(MARKDOWN);
    const anchor = appended.find((el) => el.tagName === "A");
    expect(anchor?.getAttribute("download")).toBe("v2-0-release-notes.md");
    expect(anchor?.getAttribute("href")).toBe("blob:mock");
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:mock");
  });
});

describe("Release notes dialog", () => {
  it("is absent until a release is selected", () => {
    renderPage({ seedNotes: true });
    expect(
      screen.queryByRole("button", { name: "Release notes" }),
    ).toBeNull();
  });

  it("renders the backend Markdown verbatim", async () => {
    renderPage({ seedNotes: true });
    selectRelease();
    fireEvent.click(screen.getByRole("button", { name: "Release notes" }));
    const pre = await findNotesPre();
    expect(pre.textContent).toBe(MARKDOWN);
  });

  it("Copy puts the exact Markdown on the clipboard", async () => {
    const mockedCopy = stubClipboard();
    renderPage({ seedNotes: true });
    selectRelease();
    fireEvent.click(screen.getByRole("button", { name: "Release notes" }));
    await findNotesPre();
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() =>
      expect(mockedCopy).toHaveBeenCalledWith(MARKDOWN),
    );
    expect(
      await screen.findByRole("button", { name: "Copied" }),
    ).toBeTruthy();
  });

  it("Download uses the same Markdown string", async () => {
    const blobs = stubObjectUrl();
    renderPage({ seedNotes: true });
    selectRelease();
    fireEvent.click(screen.getByRole("button", { name: "Release notes" }));
    await findNotesPre();
    fireEvent.click(screen.getByRole("button", { name: "Download .md" }));
    expect(blobs).toHaveLength(1);
    expect(await blobs[0].text()).toBe(MARKDOWN);
  });

  it("trigger is disabled while issues are loading", async () => {
    vi.spyOn(api, "get").mockImplementation(
      () => new Promise(() => {}) as Promise<never>,
    );
    renderPage({ seedIssues: false, seedNotes: true });
    selectRelease();
    const btn = await screen.findByRole("button", {
      name: "Release notes",
    });
    expect(btn.hasAttribute("disabled")).toBe(true);
  });

  it("Copy/Download are disabled while notes are loading", async () => {
    vi.spyOn(api, "get").mockImplementation((url: string) => {
      if (url.includes("/notes")) {
        return new Promise(() => {}) as Promise<never>;
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderPage({ seedIssues: true });
    selectRelease();
    fireEvent.click(screen.getByRole("button", { name: "Release notes" }));
    const copy = await screen.findByRole("button", { name: "Copy" });
    const download = screen.getByRole("button", { name: "Download .md" });
    expect(copy.hasAttribute("disabled")).toBe(true);
    expect(download.hasAttribute("disabled")).toBe(true);
  });

  it("shows an error when notes fail to load", async () => {
    vi.spyOn(api, "get").mockImplementation((url: string) => {
      if (url.includes("/notes")) {
        return Promise.reject(new ApiError(500, "notes failed"));
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderPage({ seedIssues: true });
    selectRelease();
    fireEvent.click(screen.getByRole("button", { name: "Release notes" }));
    await screen.findByText("notes failed");
  });
});
