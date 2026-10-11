// Project general settings (C18T4): name, description, and the emoji
// icon. PATCH partial semantics — only changed fields are sent; an
// explicitly cleared icon is sent as "" (server normalizes to NULL).
// Mutations are member (15)+; the server is the gate.

import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import type { Project } from "../../lib/types";
import { isSingleEmoji } from "../../lib/emoji";
import EmojiPicker from "../project/EmojiPicker";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Skeleton } from "../ui/skeleton";
import { Textarea } from "../ui/textarea";

function projectKey(slug: string, identifier: string) {
  return ["project", slug, identifier];
}

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

export default function GeneralSection({
  slug,
  identifier,
  canEdit,
}: {
  slug: string;
  identifier: string;
  canEdit: boolean;
}) {
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: projectKey(slug, identifier),
    queryFn: () =>
      api.get<Project>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`,
      ),
  });

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [icon, setIcon] = useState<string | null>(null);
  const [base, setBase] = useState<{ name: string; description: string; icon: string | null } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    const p = query.data;
    if (p && !base) {
      setName(p.name);
      setDescription(p.description ?? "");
      setIcon(p.icon ?? null);
      setBase({ name: p.name, description: p.description ?? "", icon: p.icon ?? null });
    }
  }, [query.data, base]);

  const dirty =
    base !== null &&
    (name !== base.name || description !== base.description || icon !== base.icon);
  const iconValid = icon === null || isSingleEmoji(icon);

  const saveMutation = useMutation({
    mutationFn: () => {
      if (!base) throw new Error("not loaded");
      const body: Record<string, unknown> = {};
      if (name !== base.name) body.name = name.trim();
      if (description !== base.description) body.description = description;
      // Explicit clear sends "" (server → NULL); unchanged sends nothing.
      if (icon !== base.icon) body.icon = icon ?? "";
      return api.patch<Project>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`,
        body,
      );
    },
    onSuccess: (p) => {
      setBase({ name: p.name, description: p.description ?? "", icon: p.icon ?? null });
      setError(null);
      setSaved(true);
      void queryClient.invalidateQueries({ queryKey: projectKey(slug, identifier) });
      void queryClient.invalidateQueries({ queryKey: ["projects", slug] });
    },
    onError: (e) => {
      setError(errMsg(e, "Failed to save project"));
      setSaved(false);
    },
  });

  if (query.isPending || !base) {
    return <Skeleton className="h-64 w-full" />;
  }
  if (query.isError) {
    return (
      <Alert variant="destructive">
        <AlertDescription>
          {errMsg(query.error, "Failed to load project")}
        </AlertDescription>
      </Alert>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">General</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-end gap-3">
          <div className="space-y-2">
            <Label>Icon</Label>
            <EmojiPicker
              value={icon}
              onChange={(v) => {
                setIcon(v);
                setSaved(false);
              }}
              disabled={!canEdit}
              label="Choose project icon"
            />
          </div>
          <div className="flex-1 space-y-2">
            <Label htmlFor="gs-name">Name</Label>
            <Input
              id="gs-name"
              value={name}
              disabled={!canEdit}
              onChange={(e) => {
                setName(e.target.value);
                setSaved(false);
              }}
              maxLength={120}
            />
          </div>
        </div>
        {!iconValid && (
          <Alert variant="destructive">
            <AlertDescription>
              The icon must be a single emoji.
            </AlertDescription>
          </Alert>
        )}
        <div className="space-y-2">
          <Label htmlFor="gs-description">Description</Label>
          <Textarea
            id="gs-description"
            value={description}
            disabled={!canEdit}
            onChange={(e) => {
              setDescription(e.target.value);
              setSaved(false);
            }}
            rows={3}
          />
        </div>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {saved && !dirty && (
          <p className="text-sm text-muted-foreground">Saved.</p>
        )}
        {canEdit && (
          <Button
            disabled={!dirty || !iconValid || !name.trim() || saveMutation.isPending}
            onClick={() => saveMutation.mutate()}
          >
            {saveMutation.isPending ? "Saving…" : "Save changes"}
          </Button>
        )}
      </CardContent>
    </Card>
  );
}
