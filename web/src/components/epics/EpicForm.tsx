// C71T1: shared new/edit epic form (name + description). Used by the
// epics list (create) and the epic detail (edit) pages.

import { useState, type FormEvent } from "react";
import { Button } from "../ui/button";
import { DialogFooter } from "../ui/dialog";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Textarea } from "../ui/textarea";
import { Alert, AlertDescription } from "../ui/alert";

export default function EpicForm({
  initial,
  pending,
  error,
  submitLabel,
  onSubmit,
}: {
  initial: { name: string; description: string };
  pending: boolean;
  error: string | null;
  submitLabel: string;
  onSubmit: (v: { name: string; description: string }) => void;
}) {
  const [name, setName] = useState(initial.name);
  const [description, setDescription] = useState(initial.description);
  return (
    <form
      onSubmit={(e: FormEvent) => {
        e.preventDefault();
        onSubmit({ name: name.trim(), description: description.trim() });
      }}
      className="space-y-4"
    >
      <div className="space-y-2">
        <Label htmlFor="epic-name">Name</Label>
        <Input
          id="epic-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Auth revamp"
          maxLength={255}
          required
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="epic-desc">Description</Label>
        <Textarea
          id="epic-desc"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="What does this epic group?"
          rows={3}
        />
      </div>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <DialogFooter>
        <Button type="submit" disabled={pending || !name.trim()}>
          {pending ? "Saving…" : submitLabel}
        </Button>
      </DialogFooter>
    </form>
  );
}
