import { useEffect, useState } from "react";
import { Check } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Button } from "../ui/button";
import { Skeleton } from "../ui/skeleton";

export interface OrganizeItem {
  id: string;
  name: string;
  meta?: string;
}

/** Picker dialog for bulk "add to …" actions (C24T2). Lists cycles or
 *  modules; the user picks one and confirms. Rows reuse the OptionRow
 *  visual language from the per-issue property pickers (13px base,
 *  wash-hover rows, check on the picked row). */
export default function BulkOrganizeDialog({
  open,
  onOpenChange,
  title,
  description,
  items,
  loading,
  emptyText,
  confirmLabel,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  items: OrganizeItem[];
  loading: boolean;
  emptyText: string;
  confirmLabel: string;
  onConfirm: (id: string) => void;
}) {
  const [pickedId, setPickedId] = useState<string | null>(null);

  // Drop the selection when the dialog closes so reopening starts clean.
  useEffect(() => {
    if (!open) setPickedId(null);
  }, [open ]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {loading ? (
          <div className="space-y-2" aria-label="Loading options">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-8 w-full" />
            ))}
          </div>
        ) : items.length === 0 ? (
          <p className="py-2 text-sm text-muted-foreground">{emptyText}</p>
        ) : (
          <div
            className="max-h-64 space-y-0.5 overflow-y-auto"
            role="radiogroup"
            aria-label={title}
          >
            {items.map((item) => {
              const selected = pickedId === item.id;
              return (
                <button
                  key={item.id}
                  type="button"
                  role="radio"
                  aria-checked={selected}
                  onClick={() => setPickedId(item.id)}
                  className="flex w-full items-center gap-2 rounded-[4px] px-2 py-1.5 text-left text-[13px] text-muted-foreground hover:bg-wash-hover hover:text-foreground data-[selected=true]:bg-wash-hover"
                  data-selected={selected}
                >
                  <span className="flex-1 truncate">{item.name}</span>
                  {item.meta && (
                    <span className="shrink-0 text-xs text-muted-foreground">
                      {item.meta}
                    </span>
                  )}
                  {selected && (
                    <Check className="h-3.5 w-3.5 shrink-0" aria-hidden />
                  )}
                </button>
              );
            })}
          </div>
        )}
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            disabled={!pickedId || loading}
            onClick={() => {
              if (pickedId) onConfirm(pickedId);
            }}
          >
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
