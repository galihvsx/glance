import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  SIMILAR_DEBOUNCE_MS,
  fetchSimilarIssues,
  shouldFetchSimilar,
  type SimilarIssue,
} from "../../lib/similar";

/**
 * useSimilarIssues: C8T7 duplicate detection for the create-issue modal.
 * Debounces the title (400ms — same useState+setTimeout contract as the
 * add-link dialog and parent picker) and fetches the top-5 similar
 * titles only while the dialog is open and the title passes the
 * minimum-length gate. Returns the debounced hits plus loading state for
 * the DuplicateSuggestions list.
 */
export function useSimilarIssues(
  slug: string,
  identifier: string,
  title: string,
  open: boolean,
): { suggestions: SimilarIssue[]; loading: boolean } {
  const [debounced, setDebounced] = useState("");

  useEffect(() => {
    const t = setTimeout(
      () => setDebounced(title.trim()),
      SIMILAR_DEBOUNCE_MS,
    );
    return () => clearTimeout(t);
  }, [title]);

  // Clear stale suggestions the moment the dialog closes — a picked
  // duplicate hint must not leak into the next create.
  useEffect(() => {
    if (!open) setDebounced("");
  }, [open ]);

  const enabled = open && shouldFetchSimilar(debounced);
  const query = useQuery({
    queryKey: ["similar-issues", slug, identifier, debounced],
    queryFn: () => fetchSimilarIssues(slug, identifier, debounced),
    // Only fetch while the dialog is open — no wasted requests, and the
    // gate keeps sub-trigram titles off the wire entirely.
    enabled,
    staleTime: 30_000,
  });

  return {
    suggestions: enabled ? (query.data ?? []) : [],
    loading: enabled && query.isLoading,
  };
}
