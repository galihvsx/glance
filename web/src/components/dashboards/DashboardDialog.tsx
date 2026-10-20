// C81T1: create/edit dashboard dialog — name (required), description,
// visibility radio defaulting to public (Plane's default).
import { useEffect, useState } from "react";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { RadioGroup, RadioGroupItem } from "../ui/radio-group";
import { Textarea } from "../ui/textarea";
import { Alert, AlertDescription } from "../ui/alert";
import type { DashboardVisibility } from "../../lib/dashboards";

export interface DashboardFormValue {
  name: string;
  description: string;
  visibility: DashboardVisibility;
}

export default function DashboardDialog({
  open,
  onOpenChange,
  mode,
  initial,
  pending,
  serverError,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  mode: "create" | "edit";
  initial?: {
    name: string;
    description: string | null;
    visibility: DashboardVisibility;
  };
  pending?: boolean;
  serverError?: string | null;
  onSubmit: (v: DashboardFormValue) => void;
}) {
  // Plane's default: new dashboards are public. Reset the form every
  // time the dialog opens so a previous edit never leaks into create.
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [visibility, setVisibility] = useState<DashboardVisibility>("public");

  useEffect(() => {
    if (open) {
      setName(initial?.name ?? "");
      setDescription(initial?.description ?? "");
      setVisibility(initial?.visibility ?? "public");
    }
  }, [open, initial?.name, initial?.description, initial?.visibility]);

  const trimmed = name.trim();
  const canSave = trimmed.length > 0 && !pending;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {mode === "create" ? "New dashboard" : "Edit dashboard"}
          </DialogTitle>
          <DialogDescription>
            {mode === "create"
              ? "Dashboards are workspace-scoped canvases of widgets."
              : "Rename or change who can see this dashboard."}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="dashboard-name">Name</Label>
            <Input
              id="dashboard-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Sprint health"
              autoFocus
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="dashboard-description">Description</Label>
            <Textarea
              id="dashboard-description"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What is this dashboard for? (optional)"
              rows={2}
            />
          </div>

          <fieldset className="flex flex-col gap-2">
            <legend className="text-sm font-medium">Visibility</legend>
            <RadioGroup
              value={visibility}
              onValueChange={(v) =>
                setVisibility(v as DashboardVisibility)
              }
              className="flex flex-col gap-2"
            >
              <Label
                htmlFor="visibility-public"
                className="flex cursor-pointer items-start gap-2 rounded-md border p-3 has-checked:border-primary"
              >
                <RadioGroupItem id="visibility-public" value="public" />
                <span>
                  <span className="block text-sm font-medium">Public</span>
                  <span className="block text-xs text-muted-foreground">
                    Visible to all workspace members. The creator and
                    workspace admins can edit.
                  </span>
                </span>
              </Label>
              <Label
                htmlFor="visibility-private"
                className="flex cursor-pointer items-start gap-2 rounded-md border p-3 has-checked:border-primary"
              >
                <RadioGroupItem id="visibility-private" value="private" />
                <span>
                  <span className="block text-sm font-medium">Private</span>
                  <span className="block text-xs text-muted-foreground">
                    Visible only to you. Only you can edit.
                  </span>
                </span>
              </Label>
            </RadioGroup>
          </fieldset>

          {serverError && (
            <Alert variant="destructive">
              <AlertDescription>{serverError}</AlertDescription>
            </Alert>
          )}
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
          >
            Cancel
          </Button>
          <Button
            type="button"
            disabled={!canSave}
            onClick={() =>
              onSubmit({
                name: trimmed,
                description: description.trim(),
                visibility,
              })
            }
          >
            {pending
              ? mode === "create"
                ? "Creating…"
                : "Saving…"
              : mode === "create"
                ? "Create dashboard"
                : "Save changes"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
