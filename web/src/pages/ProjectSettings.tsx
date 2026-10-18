// Project settings (C6T2): taxonomy management — labels, states, estimate
// scales. Route: /w/:slug/p/:identifier/settings (inside ProjectScope).
// Mutations are member (15)+; guests get a read-only view and the server
// 403s any mutation attempt.

import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { api } from "../lib/api";
import type { Workspace } from "../lib/types";
import PageHeader from "../components/shell/PageHeader";
import EstimatesSection from "../components/settings/EstimatesSection";
import GeneralSection from "../components/settings/GeneralSection";
import LabelsSection from "../components/settings/LabelsSection";
import StatesSection from "../components/settings/StatesSection";
import WorkflowSection from "../components/settings/WorkflowSection";
import TemplatesSection from "../components/settings/TemplatesSection";
import CustomFieldsSection from "../components/settings/CustomFieldsSection";
import ImportSection from "../components/settings/ImportSection";
import AutomationsSection from "../components/settings/AutomationsSection";
import DangerZoneSection from "../components/settings/DangerZoneSection";
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
  // C18T3: archive/restore + hard delete are admin (20) only; the server
  // 403s anything else, and the tab is not rendered for non-admins.
  const isAdmin = role === 20;

  return (
    <div className="w-full">
      {/* C19T4: shared page header replaces the per-page h1 + ProjectNav. */}
      <PageHeader
        viewName="Settings"
      />
      <div className="mx-auto w-full max-w-5xl space-y-4 p-4">
        {role === null ? (
          <Skeleton className="h-64 w-full" />
        ) : (
          <Tabs defaultValue="general">
            <TabsList>
              <TabsTrigger value="general">General</TabsTrigger>
              <TabsTrigger value="labels">Labels</TabsTrigger>
              <TabsTrigger value="states">States</TabsTrigger>
              <TabsTrigger value="workflow">Workflow</TabsTrigger>
              <TabsTrigger value="estimates">Estimates</TabsTrigger>
              <TabsTrigger value="templates">Templates</TabsTrigger>
              <TabsTrigger value="custom-fields">Custom fields</TabsTrigger>
              <TabsTrigger value="automations">Automations</TabsTrigger>
              <TabsTrigger value="import">Import</TabsTrigger>
              {isAdmin && (
                <TabsTrigger value="danger">Danger zone</TabsTrigger>
              )}
            </TabsList>
            <TabsContent value="general" className="mt-4">
              <GeneralSection slug={slug} identifier={identifier} canEdit={canEdit} />
            </TabsContent>
            <TabsContent value="labels" className="mt-4">
              <LabelsSection slug={slug} identifier={identifier} canEdit={canEdit} />
            </TabsContent>
            <TabsContent value="states" className="mt-4">
              <StatesSection slug={slug} identifier={identifier} canEdit={canEdit} />
            </TabsContent>
            <TabsContent value="workflow" className="mt-4">
              <WorkflowSection slug={slug} identifier={identifier} canEdit={canEdit} />
            </TabsContent>
            <TabsContent value="estimates" className="mt-4">
              <EstimatesSection
                slug={slug}
                identifier={identifier}
                canEdit={canEdit}
              />
            </TabsContent>
            <TabsContent value="templates" className="mt-4">
              <TemplatesSection
                slug={slug}
                identifier={identifier}
                canEdit={canEdit}
              />
            </TabsContent>
            <TabsContent value="custom-fields" className="mt-4">
              <CustomFieldsSection
                slug={slug}
                identifier={identifier}
                canEdit={canEdit}
              />
            </TabsContent>
            <TabsContent value="automations" className="mt-4">
              <AutomationsSection
                slug={slug}
                identifier={identifier}
                canEdit={canEdit}
              />
            </TabsContent>
            <TabsContent value="import" className="mt-4">
              <ImportSection
                slug={slug}
                identifier={identifier}
                canEdit={canEdit}
              />
            </TabsContent>
            {isAdmin && (
              <TabsContent value="danger" className="mt-4">
                <DangerZoneSection slug={slug} identifier={identifier} />
              </TabsContent>
            )}
          </Tabs>
        )}
      </div>
    </div>
  );
}
