// Risk (C30T3): workspace-level risk radar — per-project risk score
// (0–100) with level, contributing factors, and drill-downs into Issues.
// Implements the approved C30T2 prototype
// (.superpowers/sdd/2026-10-13-glance-cycle30/prototype/risk-radar.html).
// Loading / empty / error states follow the Workload page's visual language.

import { useMemo } from "react";
import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import PageHeader from "../components/shell/PageHeader";
import RiskCard from "../components/risk/RiskCard";
import RiskSummaryStrip from "../components/risk/RiskSummaryStrip";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Skeleton } from "../components/ui/skeleton";
import { relativeTime } from "../lib/relativeTime";
import { fetchRisk } from "../lib/risk";

function Loading() {
  return (
    <div className="space-y-3" aria-label="Loading risk">
      {[0, 1, 2].map((i) => (
        <Skeleton
          key={i}
          data-testid="risk-skeleton"
          className="h-[176px] w-full rounded-[10px]"
        />
      ))}
    </div>
  );
}

export default function Risk() {
  const { slug = "" } = useParams<{ slug: string }>();

  const riskQuery = useQuery({
    queryKey: ["risk", slug],
    queryFn: () => fetchRisk(slug),
    enabled: !!slug,
  });
  const report = riskQuery.data;

  // Prototype order: risk score desc (backend returns identifier order).
  const projects = useMemo(
    () =>
      [...(report?.projects ?? [])].sort(
        (a, b) =>
          b.risk_score - a.risk_score ||
          a.identifier.localeCompare(b.identifier),
      ),
    [report],
  );

  return (
    <div className="w-full">
      <PageHeader
        viewName="Risk radar"
        count={`${projects.length} projects`}
        showAnalytics={false}
        addButton={null}
        extraControls={
          <span className="hidden rounded-md border border-border-hairline bg-wash px-2 py-1 font-mono text-[10.5px] text-ink-muted lg:inline-flex">
            GET /api/v1/workspaces/:slug/risk
          </span>
        }
      />
      <div className="mx-auto w-full max-w-[1060px] space-y-4 px-7 py-6">
        <div>
          <p className="max-w-[640px] text-[12.5px] leading-relaxed text-ink-secondary">
            A <strong className="font-semibold text-ink">heuristic composite</strong>{" "}
            of overdue, stalled, unassigned and overloaded-member signals per
            project — an early-warning glance,{" "}
            <strong className="font-semibold text-ink">not a prediction</strong>{" "}
            of delivery. Scores combine factor weights with observed values;
            the factors behind each score are listed on the card.
          </p>
          {report && report.generated_at && (
            <p className="mt-1 text-[11px] text-ink-muted">
              Generated {relativeTime(report.generated_at)}
            </p>
          )}
        </div>

        {riskQuery.isPending ? (
          <Loading />
        ) : riskQuery.isError ? (
          <Alert variant="destructive">
            <AlertDescription>
              Couldn&apos;t load risk. Try again in a moment.
            </AlertDescription>
          </Alert>
        ) : projects.length === 0 ? (
          <p className="py-8 text-center text-xs text-muted-foreground">
            No projects in this workspace yet.
          </p>
        ) : (
          <>
            <RiskSummaryStrip projects={projects} />
            <section aria-label="Project risk" className="space-y-3">
              {projects.map((p) => (
                <RiskCard key={p.project_id} slug={slug} project={p} />
              ))}
            </section>
          </>
        )}
      </div>
    </div>
  );
}
