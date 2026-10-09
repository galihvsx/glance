// Project settings (C6T2): taxonomy management — labels, states, estimate
// scales. Route: /w/:slug/p/:identifier/settings (inside ProjectScope).
// Mutations are member (15)+; guests get a read-only view and the server
// 403s any mutation attempt.

import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { api } from "../lib/api";
import type { Workspace } from "../lib/types";
import ProjectNav from "../components/project/ProjectNav";
import ThemeToggle from "../components/ThemeToggle";
import NotificationBell from "../components/notifications/NotificationBell";
import EstimatesSection from "../components/settings/EstimatesSection";
import LabelsSection from "../components/settings/LabelsSection";
import StatesSection from "../components/settings/StatesSection";
import { Skeleton } from "../components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../components/ui/tabs";

export default function ProjectSettings() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const [role, setRole] = useState<number | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .get<Workspace & { role: number }>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}`,
      )
      .then((ws) => {
        if (!cancelled) setRole(ws.role ?? 0);
      })
      .catch(() => {
        if (!cancelled) setRole(0);
      });
    return () => {
      cancelled = true;
    };
  }, [slug]);

  const canEdit = (role ?? 0) >= 15;

  return (
    <div className="mx-auto max-w-5xl space-y-4 p-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Project settings</h1>
        <div className="flex items-center gap-2">
          <NotificationBell />
          <ThemeToggle />
        </div>
      </div>
      <ProjectNav />
      {role === null ? (
        <Skeleton className="h-64 w-full" />
      ) : (
        <Tabs defaultValue="labels">
          <TabsList>
            <TabsTrigger value="labels">Labels</TabsTrigger>
            <TabsTrigger value="states">States</TabsTrigger>
            <TabsTrigger value="estimates">Estimates</TabsTrigger>
          </TabsList>
          <TabsContent value="labels" className="mt-4">
            <LabelsSection slug={slug} identifier={identifier} canEdit={canEdit} />
          </TabsContent>
          <TabsContent value="states" className="mt-4">
            <StatesSection slug={slug} identifier={identifier} canEdit={canEdit} />
          </TabsContent>
          <TabsContent value="estimates" className="mt-4">
            <EstimatesSection
              slug={slug}
              identifier={identifier}
              canEdit={canEdit}
            />
          </TabsContent>
        </Tabs>
      )}
    </div>
  );
}
