import { useEffect, useState } from "react";
import { CornerDownRight } from "lucide-react";
import type { Issue } from "../../lib/types";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import ParentPicker from "./ParentPicker";

/**
 * Convert-to-sub-issue dialog (C24T1). Lets the user pick the new parent
 * issue with the shared ParentPicker (the issue itself is excluded); the
 * caller owns the confirm call, toast and refetch. Sub-issues cannot be
 * picked as parents on the backend (max depth 1) — a 400 surfaces in the
 * dialog's error alert so the user can pick another parent.
 */
export default function ConvertIssueDialog({
  open,
  onOpenChange,
  slug,
  identifier,
  issue,
  onConfirm,
  confirming,
  error,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  slug: string;
  identifier: string;
  issue: Issue;
  onConfirm: (parent: Issue) => void;
  confirming: boolean;
  error: string | null;
}) {
  const [parent, setParent] = useState<Issue | null>(null);

  // Reset the picked parent every time the dialog opens.
  useEffect(() => {
    if (open) setParent(null);
  }, [open ]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[420px]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <CornerDownRight className="h-4 w-4" />
            Convert to sub-issue
          </DialogTitle>
          <DialogDescription>
            <span className="font-mono font-medium text-foreground">
              {issue.display_id}
            </span>{" "}
            becomes a sub-issue of the parent you pick below.
          </DialogDescription>
        </DialogHeader>
        <div className="py-2">
          <ParentPicker
            slug={slug}
            identifier={identifier}
            value={parent}
            onChange={setParent}
            disabled={confirming}
            excludeIds={[issue.id]}
            hideNoneOption
          />
        </div>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={confirming}
          >
            Cancel
          </Button>
          <Button
            onClick={() => parent && onConfirm(parent)}
            disabled={!parent || confirming}
          >
            {confirming ? "Converting…" : "Convert to sub-issue"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
