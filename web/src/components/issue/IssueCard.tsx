import { useNavigate, useParams } from "react-router-dom";
import { Calendar } from "lucide-react";
import type { Issue, IssueState } from "../../lib/types";
import { priorityLabel } from "../../lib/types";
import { Badge } from "../ui/badge";
import { CardHeader, CardTitle } from "../ui/card";
import { ActionCard } from "../ui/action-card";
import StateBadge from "./StateBadge";
import {
  formatIssueDateRange,
  type DisplayFields,
} from "./useDisplaySettings";

/** One row of the issue list: display_id badge, title, state, labels,
 *  priority, assignees. Click opens the issue — via `onOpen` when given
 *  (peek drawer), otherwise navigates to the detail page. The `fields`
 *  prop (from the Display panel) toggles which attributes render. */
export default function IssueCard({
  issue,
  state,
  onOpen,
  fields,
}: {
  issue: Issue;
  state: IssueState | undefined;
  onOpen?: (issue: Issue) => void;
  fields: DisplayFields;
}) {
  const { slug, identifier } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const navigate = useNavigate();
  const dateRange = formatIssueDateRange(issue.start_date, issue.target_date);

  return (
    <ActionCard
      role="link"
      label={`Open issue ${issue.display_id}: ${issue.name}`}
      onActivate={() =>
        onOpen
          ? onOpen(issue)
          : navigate(`/w/${slug}/p/${identifier}/i/${issue.id}`)
      }
    >
      <CardHeader className="space-y-2 py-4">
        <div className="flex items-center gap-2">
          <Badge variant="outline" className="font-mono">
            {issue.display_id}
          </Badge>
          {fields.state && state && <StateBadge state={state} />}
          {fields.priority && (
            <span className="ml-auto text-xs text-muted-foreground">
              {priorityLabel(issue.priority)}
            </span>
          )}
        </div>
        <CardTitle className="text-base font-medium">{issue.name}</CardTitle>
        {((fields.labels && issue.labels.length > 0) ||
          (fields.assignees && issue.assignees.length > 0)) && (
          <div className="flex flex-wrap items-center gap-1.5">
            {fields.labels &&
              issue.labels.map((l) => (
                <Badge
                  key={l.id}
                  variant="secondary"
                  className="gap-1 text-xs font-normal"
                >
                  <span
                    className="h-2 w-2 rounded-full"
                    style={{ backgroundColor: l.color }}
                    aria-hidden
                  />
                  {l.name}
                </Badge>
              ))}
            {fields.assignees &&
              issue.assignees.map((a) => (
                <Badge
                  key={a.id}
                  variant="outline"
                  className="text-xs font-normal"
                >
                  {a.name}
                </Badge>
              ))}
          </div>
        )}
        {fields.dates && dateRange && (
          <div className="flex items-center gap-1 text-[11px] text-muted-foreground">
            <Calendar className="h-3 w-3" aria-hidden />
            <span>{dateRange}</span>
          </div>
        )}
      </CardHeader>
    </ActionCard>
  );
}
