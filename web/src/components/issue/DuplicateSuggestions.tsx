import { AlertTriangle, Loader2 } from "lucide-react";
import { useSimilarIssues } from "./useSimilarIssues";

/**
 * DuplicateSuggestions (C8T7): "Possible duplicates" for the
 * create-issue modal. Fetches the top-5 trigram-similar titles (debounced
 * 400ms) while the user types; each row opens the peek drawer so the
 * user can check whether the issue is a true duplicate before creating
 * another one. Silent while loading, when there are no hits, or when
 * the title is too short to be meaningful.
 */
export default function DuplicateSuggestions({
  slug,
  identifier,
  title,
  open,
  onOpenPeek,
}: {
  slug: string;
  identifier: string;
  title: string;
  open: boolean;
  onOpenPeek: (uuid: string) => void;
}) {
  const { suggestions, loading } = useSimilarIssues(
    slug,
    identifier,
    title,
    open,
  );

  if (!open) return null;
  if (loading) {
    return (
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
        Checking for possible duplicates…
      </div>
    );
  }
  if (suggestions.length === 0) return null;

  return (
    <div
      className="rounded-md border border-amber-500/30 bg-amber-500/5 p-2"
      aria-live="polite"
    >
      <div className="flex items-center gap-1.5 px-1 pb-1 text-xs font-medium text-amber-700 dark:text-amber-400">
        <AlertTriangle className="h-3.5 w-3.5" aria-hidden />
        Possible duplicates
      </div>
      <ul className="space-y-0.5">
        {suggestions.map((s) => (
          <li key={s.id}>
            <button
              type="button"
              onClick={() => onOpenPeek(s.id)}
              title={`Open ${s.display_id} in the peek drawer`}
              className="flex w-full items-center gap-2 rounded px-1 py-1 text-left text-xs transition-colors hover:bg-muted/70"
            >
              <span className="shrink-0 font-mono text-muted-foreground">
                {s.display_id}
              </span>
              <span className="min-w-0 flex-1 truncate text-foreground">
                {s.name}
              </span>
              <span className="shrink-0 text-muted-foreground">{s.state}</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
