// RiskCard (C30T3): one project's risk card — identifier chip, name, open
// count, level badge, score + bar, factor rows (in received order) or the
// no-factors state for a healthy project. Mirrors the prototype.

import { RISK_LEVEL_META, type ProjectRisk } from "../../lib/risk";
import RiskFactorRow from "./RiskFactorRow";
import RiskLevelBadge from "./RiskLevelBadge";

export default function RiskCard({
  slug,
  project,
}: {
  slug: string;
  project: ProjectRisk;
}) {
  const meta = RISK_LEVEL_META[project.risk_level];
  return (
    <article
      data-testid="risk-card"
      data-project-id={project.project_id}
      className="overflow-hidden rounded-[10px] border border-border bg-surface"
    >
      <div className="flex items-center gap-2.5 px-4 pt-3.5">
        <span className="rounded-md border border-border-hairline bg-wash px-2 py-[3px] font-mono text-[11px] tracking-wide text-ink-secondary">
          {project.identifier}
        </span>
        <span className="text-sm font-semibold">{project.name}</span>
        <span className="text-xs text-ink-muted tabular-nums">
          {project.open_count} open issue
          {project.open_count === 1 ? "" : "s"}
        </span>
        <span className="ml-auto">
          <RiskLevelBadge level={project.risk_level} />
        </span>
      </div>
      <div className="flex items-baseline gap-2.5 px-4 pt-3 pb-2.5">
        <span
          className="text-[30px] leading-none font-semibold tracking-tight tabular-nums"
          style={{ color: meta.color }}
        >
          {project.risk_score}
        </span>
        <span className="text-xs text-ink-muted">/ 100</span>
        <span className="text-[11.5px] text-ink-muted">
          composite risk score
        </span>
      </div>
      <div className="mx-4 mb-3.5 h-1.5 overflow-hidden rounded-full bg-wash">
        <div
          role="img"
          aria-label={`Risk score ${project.risk_score} of 100`}
          className="h-full rounded-full"
          style={{
            width: `${Math.min(100, Math.max(0, project.risk_score))}%`,
            backgroundColor: meta.color,
          }}
        />
      </div>
      {project.factors.length > 0 ? (
        <div className="border-t border-border-hairline px-4 py-1.5">
          {project.factors.map((f) => (
            <RiskFactorRow
              key={f.key}
              slug={slug}
              project={project}
              factor={f}
              color={meta.color}
            />
          ))}
        </div>
      ) : (
        <div className="flex items-center gap-2.5 border-t border-border-hairline p-4 text-[12.5px] text-ink-muted">
          <span
            aria-hidden="true"
            className="flex size-[22px] shrink-0 items-center justify-center rounded-full text-[13px] font-semibold"
            style={{
              backgroundColor: "rgba(66,200,138,0.15)",
              color: "#42c88a",
            }}
          >
            ✓
          </span>
          <span>
            No risk factors. Nothing overdue, stalled, unassigned or
            overloaded in this project.
          </span>
        </div>
      )}
    </article>
  );
}
