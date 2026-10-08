import { useNavigate } from "react-router-dom";
import { ExternalLink, X } from "lucide-react";
import { Sheet, SheetContent } from "../ui/sheet";
import { Button } from "../ui/button";
import IssueDetailContent from "./IssueDetailContent";

/**
 * Plane-style peek view: the issue detail slides in from the right while
 * the list/board stays mounted behind it (selection, scroll, filters
 * intact). Built on the shadcn Sheet (base-ui dialog), which gives us
 * Esc-to-close, overlay click-to-close, focus-on-open and focus restore
 * on close for free.
 */
export default function PeekDrawer({
  slug,
  identifier,
  uuid,
  onClose,
}: {
  slug: string;
  identifier: string;
  uuid: string;
  onClose: () => void;
}) {
  const navigate = useNavigate();

  return (
    <Sheet
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <SheetContent
        side="right"
        showCloseButton={false}
        aria-label="Issue peek view"
        className="w-full gap-0 overflow-y-auto p-0 sm:max-w-2xl"
      >
        <div className="p-6">
          <IssueDetailContent
            key={uuid}
            slug={slug}
            identifier={identifier}
            uuid={uuid}
            compact
            headerActions={
              <>
                <Button
                  variant="ghost"
                  size="sm"
                  className="gap-1.5"
                  onClick={() =>
                    navigate(`/w/${slug}/p/${identifier}/i/${uuid}`)
                  }
                >
                  <ExternalLink className="h-3.5 w-3.5" />
                  Open full page
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  onClick={onClose}
                  aria-label="Close peek view"
                >
                  <X className="h-4 w-4" />
                </Button>
              </>
            }
          />
        </div>
      </SheetContent>
    </Sheet>
  );
}
