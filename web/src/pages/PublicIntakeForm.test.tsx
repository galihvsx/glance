// @vitest-environment jsdom
// C82T1: PublicIntakeForm — the public /f/:token submission page: 404
// state for unknown/disabled tokens, field rendering through the shared
// FormRenderer, client-side required validation, success state with a
// "Submit another response" reset.
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import PublicIntakeForm from "./PublicIntakeForm";
import type { PublicIntakeForm as PublicForm } from "../lib/intakeForms";

const mockApi = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}));

vi.mock("../lib/api", () => ({
  api: { get: mockApi.get, post: mockApi.post },
  ApiError: class ApiError extends Error {},
}));

const publicForm: PublicForm = {
  name: "Bug report",
  description: "Tell us what broke.",
  project_name: "Eng",
  fields: [
    { key: "title", label: "Summary", required: true, type: "title", options: [] },
    {
      key: "description",
      label: "Details",
      required: false,
      type: "description",
      options: [],
    },
  ],
};

function renderPage(
  token = "tok123",
  getImpl?: (path: string) => Promise<unknown>,
) {
  mockApi.get.mockReset();
  mockApi.post.mockReset();
  // The implementation must be set BEFORE render: react-query fires the
  // query in an effect flushed synchronously by RTL's act().
  if (getImpl) mockApi.get.mockImplementation(getImpl);
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[`/f/${token}`]}>
        <Routes>
          <Route path="/f/:token" element={<PublicIntakeForm />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("PublicIntakeForm", () => {
  it("shows the unavailable state for an unknown token", async () => {
    renderPage("bogus", () => Promise.reject(new Error("nope")));
    await waitFor(() =>
      expect(screen.getByText("This form is unavailable.")).toBeInTheDocument(),
    );
    // No manage UI, no login prompts leak.
    expect(screen.queryByText(/sign in/i)).not.toBeInTheDocument();
  });

  it("renders the form name, description, and fields", async () => {
    renderPage("tok123", () => Promise.resolve(publicForm));
    await waitFor(() =>
      expect(screen.getByText("Bug report")).toBeInTheDocument(),
    );
    expect(screen.getByText("Tell us what broke.")).toBeInTheDocument();
    expect(screen.getByLabelText(/Summary/)).toBeInTheDocument();
    expect(screen.getByLabelText("Details")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Submit" })).toBeInTheDocument();
  });

  it("blocks submit when required fields are empty", async () => {
    renderPage("tok123", () => Promise.resolve(publicForm));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Submit" })).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Submit" }));
    expect(
      await screen.findByText(/Please fill in the required fields/),
    ).toBeInTheDocument();
    expect(mockApi.post).not.toHaveBeenCalled();
  });

  it("submits and shows the success state with a reset", async () => {
    renderPage("tok123", () => Promise.resolve(publicForm));
    mockApi.post.mockResolvedValue({ issue_id: "iss-1" });
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Submit" })).toBeInTheDocument(),
    );
    fireEvent.change(screen.getByLabelText(/Summary/), {
      target: { value: "Crash on login" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Submit" }));
    await waitFor(() =>
      expect(mockApi.post).toHaveBeenCalledWith(
        "/api/v1/forms/tok123/submit",
        { values: { title: "Crash on login" } },
      ),
    );
    expect(
      await screen.findByText(
        "Response submitted — the team will review it in Intake.",
      ),
    ).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "Submit another response" }),
    );
    expect(
      await screen.findByRole("button", { name: "Submit" }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/Summary/)).toHaveValue("");
  });
});
