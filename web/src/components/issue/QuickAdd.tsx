import { useEffect, useRef, useState, type FormEvent } from "react";
import { Loader2, Maximize2, Plus } from "lucide-react";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

/** Inline "+ New work item" quick-add row, Plane-style.
 *
 * Idle: a subtle "+ New work item" button. Clicking turns it into an
 * inline input; Enter creates the item via onCreate, Esc cancels.
 * The expand icon hands the typed title to the full create form via
 * onExpand so no capability is lost. */
export default function QuickAdd({
  onCreate,
  onExpand,
  disabled,
  className,
}: {
  onCreate: (name: string) => Promise<void>;
  onExpand?: (name: string) => void;
  disabled?: boolean;
  className?: string;
}) {
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (editing) inputRef.current?.focus();
  }, [editing ]);

  function cancel() {
    setEditing(false);
    setValue("");
    setError(null);
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    const name = value.trim();
    if (!name) {
      setError("Give the issue a title.");
      inputRef.current?.focus();
      return;
    }
    setError(null);
    setPending(true);
    try {
      await onCreate(name);
      cancel();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create issue");
    } finally {
      setPending(false);
    }
  }

  if (!editing) {
    return (
      <button
        type="button"
        disabled={disabled}
        onClick={() => setEditing(true)}
        className={`flex w-full items-center gap-1.5 rounded-md px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50 ${className ?? ""}`}
        aria-label="Add new work item inline"
      >
        <Plus className="h-3.5 w-3.5" aria-hidden />
        New work item
      </button>
    );
  }

  return (
    <div className={className}>
      <form onSubmit={submit} className="flex items-center gap-1">
        <Input
          ref={inputRef}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              e.stopPropagation();
              cancel();
            }
          }}
          placeholder="Issue title — Enter to create, Esc to cancel"
          disabled={pending}
          aria-label="New work item title"
          className="h-8 text-sm"
        />
        {onExpand && (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="h-8 w-8 shrink-0"
            title="Open full create form"
            aria-label="Open full create form"
            disabled={pending}
            onClick={() => {
              onExpand(value);
              cancel();
            }}
          >
            <Maximize2 className="h-3.5 w-3.5" aria-hidden />
          </Button>
        )}
        {pending && (
          <Loader2
            className="h-4 w-4 shrink-0 animate-spin text-muted-foreground"
            aria-hidden
          />
        )}
      </form>
      {error && (
        <p role="alert" className="mt-1 px-2 text-xs text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}
