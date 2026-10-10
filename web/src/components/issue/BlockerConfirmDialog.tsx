import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../ui/alert-dialog";

/**
 * Open-blockers confirm (C16T3). Shown when the API rejects a move into a
 * completed state with 409 code "open_blockers": it names the open
 * blockers and offers "Complete anyway", which retries the move with
 * ignore_blockers=true. Copy stays honest — it never claims the
 * dependencies are resolved.
 */
export default function BlockerConfirmDialog({
  open,
  blockers,
  pending,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  blockers: string[];
  pending: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <AlertDialog
      open={open}
      onOpenChange={(o) => {
        if (!o) onCancel();
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Complete with open blockers?</AlertDialogTitle>
          <AlertDialogDescription>
            This issue is still blocked by{" "}
            {blockers.length === 1 ? "this issue" : "these issues"}:{" "}
            <span className="font-mono font-medium text-foreground">
              {blockers.join(", ")}
            </span>
            . Completing now leaves{" "}
            {blockers.length === 1 ? "that dependency" : "those dependencies"}{" "}
            unresolved.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction disabled={pending} onClick={onConfirm}>
            Complete anyway
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
