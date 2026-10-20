// One row of the WorkspaceSettings Members list (C80T1). The row keeps
// the existing anatomy (identity left, role controls right) and adds an
// "Activity" affordance: an expandable per-member feed powered by the
// workspace activity endpoint's ?actor= filter. The affordance is only
// rendered when the viewer may read the feed (role >= 15 — the endpoint
// 403s guests and viewers), so guests never see it.
import { Activity, ChevronDown } from "lucide-react";
import type { WorkspaceMember } from "../../lib/types";
import { roleLabel, WORKSPACE_ROLES } from "../../lib/types";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { cn } from "cn";
import MemberActivityPanel from "./MemberActivityPanel";

export default function MemberListItem({
  slug,
  member,
  isAdmin,
  viewerCanViewActivity,
  actingId,
  activityOpen,
  onToggleActivity,
  onRoleChange,
  onRemove,
}: {
  slug: string;
  member: WorkspaceMember;
  isAdmin: boolean;
  /** False for viewers/guests (role < 15): the activity feed 403s them,
   *  so the affordance is hidden rather than erroring. */
  viewerCanViewActivity: boolean;
  actingId: string | null;
  activityOpen: boolean;
  onToggleActivity: () => void;
  onRoleChange: (member: WorkspaceMember, role: number) => void;
  onRemove: (member: WorkspaceMember) => void;
}) {
  return (
    <li className="py-2.5">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <p className="truncate font-medium">{member.name ?? member.email}</p>
          {member.name && (
            <p className="truncate text-xs text-muted-foreground">
              {member.email}
            </p>
          )}
        </div>
        <div className="flex items-center gap-2">
          {viewerCanViewActivity && (
            <Button
              variant="ghost"
              size="sm"
              onClick={onToggleActivity}
              aria-expanded={activityOpen}
              aria-label={`Activity for ${member.name ?? member.email}`}
            >
              <Activity className="h-3.5 w-3.5" />
              Activity
              <ChevronDown
                className={cn(
                  "h-3.5 w-3.5 transition-transform",
                  activityOpen && "rotate-180",
                )}
              />
            </Button>
          )}
          {isAdmin ? (
            <>
              <select
                aria-label={`Role for ${member.name ?? member.email}`}
                value={member.role}
                disabled={actingId === member.id}
                onChange={(e) => onRoleChange(member, Number(e.target.value))}
                className="rounded-md border border-border bg-[#111113] px-2 py-1"
              >
                {WORKSPACE_ROLES.map((r) => (
                  <option key={r.value} value={r.value}>
                    {r.label}
                  </option>
                ))}
              </select>
              <Button
                variant="ghost"
                size="sm"
                disabled={actingId === member.id}
                onClick={() => onRemove(member)}
              >
                {actingId === member.id ? "…" : "Remove"}
              </Button>
            </>
          ) : (
            <Badge variant="secondary">{roleLabel(member.role)}</Badge>
          )}
        </div>
      </div>
      {activityOpen && viewerCanViewActivity && (
        <MemberActivityPanel slug={slug} member={member} />
      )}
    </li>
  );
}
