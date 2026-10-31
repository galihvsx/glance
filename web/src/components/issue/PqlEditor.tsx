// PQL editor (C91T1) — the PQL-mode content of the filter panel,
// following the cycle91 prototype as spec: mode toggle lives in
// FilterPanel; this is the editor, Apply/Clear row, inline error,
// and field/operator cheat-sheet.
//
// Apply is probe-first: the parent's onApply fetches one page with
// the pql-only params and only commits the query to the list on
// success. A 400 therefore leaves the issue list untouched and the
// failed query stays in this editor with the server's message +
// column shown inline (role="alert"). Empty query = no filter.
import { useState } from "react";
import { Button } from "../ui/button";
import { Label } from "../ui/label";
import { Textarea } from "../ui/textarea";
import { PQL_CHEAT_SHEET, formatPqlError } from "../../lib/pql";

interface PqlEditorProps {
  /** Last applied query ("" when none) — the draft's initial value. */
  appliedQuery: string;
  /**
   * Probe + commit. Resolves once the query is applied; rejects with
   * the server error on a 400 so it can be shown inline here.
   */
  onApply: (query: string) => Promise<void>;
  /** Called when Clear commits an empty query (no filter). */
  onClear: () => void;
  /** Called after a successful apply so the host can close up. */
  onApplied?: () => void;
  /**
   * Show the "Views → Save current view" hint. Default true (the
   * Issues page, where that save path exists). Pass false on
   * surfaces with no Views save path for PQL — Board (its Views
   * menu saves visual filters only), cycle detail, and module
   * detail (no Views menu at all) — so the hint is not wrong
   * there (C96T1).
   */
  showSaveHint?: boolean;
}

export default function PqlEditor({
  appliedQuery,
  onApply,
  onClear,
  onApplied,
  showSaveHint = true,
}: PqlEditorProps) {
  const [draft, setDraft] = useState(appliedQuery);
  const [error, setError] = useState<string | null>(null);
  const [applying, setApplying] = useState(false);

  async function handleApply() {
    setApplying(true);
    setError(null);
    try {
      await onApply(draft);
      onApplied?.();
    } catch (e) {
      // Verbatim server message + column (lib/pql.ts); the list is
      // untouched because onApply only commits on success.
      setError(formatPqlError(e));
    } finally {
      setApplying(false);
    }
  }

  function handleClear() {
    setDraft("");
    setError(null);
    onClear();
  }

  return (
    <div className="px-2 py-1">
      <div className="space-y-2">
        <Label htmlFor="pql-editor">PQL query</Label>
        <Textarea
          id="pql-editor"
          aria-label="PQL query"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder='priority = High AND (state = Todo OR state = "In Progress")'
          rows={3}
          className="font-mono text-xs"
        />
        {error && (
          <div
            role="alert"
            className="rounded-md border border-destructive/30 bg-destructive/10 px-2 py-1.5 text-xs text-destructive"
          >
            {error}
          </div>
        )}
        <div className="flex items-center gap-2">
          <Button
            type="button"
            size="sm"
            className="h-7"
            disabled={applying}
            onClick={() => void handleApply()}
          >
            {applying ? "Applying…" : "Apply"}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="h-7"
            disabled={applying}
            onClick={handleClear}
          >
            Clear
          </Button>
          <span className="text-xs text-muted-foreground">
            Empty query = no filter.
          </span>
        </div>
        {showSaveHint && (
          <p className="text-xs text-muted-foreground">
            To save this query, use Views → Save current view in the page
            header; it re-applies when the view is opened.
          </p>
        )}
      </div>

      <div className="mt-3 border-t pt-2">
        <p className="pb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
          Fields &amp; operators
        </p>
        <ul className="space-y-1">
          {PQL_CHEAT_SHEET.map((row) => (
            <li key={row.example} className="text-xs">
              <code className="rounded bg-muted px-1 py-0.5">
                {row.example}
              </code>{" "}
              <span className="text-muted-foreground">{row.meaning}</span>
            </li>
          ))}
        </ul>
        <p className="pt-2 text-xs text-muted-foreground">
          Unknown names → server error (never a silent empty list).
          Sorting and pagination stay as today — PQL has no ORDER BY /
          LIMIT clause this cycle.
        </p>
      </div>
    </div>
  );
}
