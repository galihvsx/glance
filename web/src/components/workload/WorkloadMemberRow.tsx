// WorkloadMemberRow (C29T1): one workspace member's load-vs-capacity row
// on the Workload page. Shows the capacity bar, overdue/urgent flags,
// estimate-point load, 14-day velocity, and a drill-down link into the
// Issues view filtered by assignee.

import { Link } from "react-router-dom";
import { roleLabel } from "../../lib/types";
import {
  loadFraction,
  workloadBarColor,
  type WorkloadMember,
} from "../../lib/workload";
import { Avatar, AvatarFallback } from "../ui/avatar";
import { Badge } from "../ui/badge";

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  return (parts[0][0] + (parts.length > 1 ? parts[parts.length - 1][0] : ""))
    .toUpperCase()
    .slice(0, 2);
}

export default function WorkloadMemberRow({
  member,
  drilldownHref,
}: {
  member: WorkloadMember;
  /** Issues-view URL filtered by assignee (null when no project to scope it to). */
  drilldownHref: string | null;
}) {
  const fraction = loadFraction(member);
  const barColor = workloadBarColor(fraction);
  const capacitySet = member.weekly_capacity_points != null;

  return (
    <div
      data-testid="workload-member-row"
      data-user-id={member.user_id}
      className="rounded-lg border border-border-hairline bg-surface p-4"
    >
      <div className="flex items-start gap-3">
        <Avatar size="sm" aria-hidden="true">
          <AvatarFallback>{initials(member.display_name)}</AvatarFallback>
        </Avatar>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            {drilldownHref ? (
              <Link
                to={drilldownHref}
                aria-label={`View ${member.display_name}'s open issues`}
                className="truncate text-[13px] font-semibold text-ink hover:underline"
              >
                {member.display_name}
              </Link>
            ) : (
              <span className="truncate text-[13px] font-semibold text-ink">
                {member.display_name}
              </span>
            )}
            <Badge variant="secondary">{roleLabel(member.role)}</Badge>
            {member.overloaded && (
              <Badge variant="destructive" data-testid="overloaded-flag">
                Overloaded
              </Badge>
            )}
            {member.overdue_count > 0 && (
              <Badge
                variant="outline"
                data-testid="overdue-flag"
                className="border-[#f87171]/40 text-[#f87171]"
              >
                {member.overdue_count} overdue
              </Badge>
            )}
            {member.priority.urgent > 0 && (
              <Badge
                variant="outline"
                data-testid="urgent-flag"
                className="border-[#fb923c]/40 text-[#fb923c]"
              >
                {member.priority.urgent} urgent
              </Badge>
            )}
          </div>

          {/* Load-vs-capacity bar */}
          <div className="mt-2.5">
            <div className="flex items-baseline justify-between gap-2 text-[11px]">
              <span className="font-medium tabular-nums text-ink-secondary">
                {member.open_points} /{" "}
                {capacitySet
                  ? `${member.weekly_capacity_points} pts`
                  : "no capacity set"}
              </span>
              <span className="text-ink-muted">
                {member.open_count} open · {member.completed_14d} done in 14d
              </span>
            </div>
            <div
              role="progressbar"
              aria-label={`Load for ${member.display_name}`}
              aria-valuenow={member.open_points}
              aria-valuemax={member.weekly_capacity_points ?? undefined}
              className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-wash"
            >
              <div
                className="h-full rounded-full transition-[width]"
                style={{
                  width: `${Math.min(100, (fraction ?? 0) * 100)}%`,
                  backgroundColor: barColor,
                }}
              />
            </div>
          </div>

          {/* Priority breakdown */}
          <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-ink-muted">
            {(
              [
                ["Urgent", member.priority.urgent, "#f87171"],
                ["High", member.priority.high, "#fb923c"],
                ["Medium", member.priority.medium, "#fbbf24"],
                ["Low", member.priority.low, "#60a5fa"],
                ["None", member.priority.none, "#9ca3af"],
              ] as const
            ).map(([label, count, color]) =>
              count > 0 ? (
                <span key={label} className="tabular-nums">
                  <span
                    aria-hidden="true"
                    className="mr-1 inline-block size-1.5 rounded-full"
                    style={{ backgroundColor: color }}
                  />
                  {label} {count}
                </span>
              ) : null,
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
