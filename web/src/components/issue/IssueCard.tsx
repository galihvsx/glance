import { useNavigate, useParams } from "react-router-dom";
import type { Issue, IssueState } from "../../lib/types";
import { priorityLabel } from "../../lib/types";
import { Badge } from "../ui/badge";
import { Card, CardHeader, CardTitle } from "../ui/card";
import StateBadge from "./StateBadge";

/** One row of the issue list: display_id badge, title, state, labels,
 *  priority, assignees. Click navigates to the detail page. */
export default function IssueCard({
  issue,
  state,
}: {
  issue: Issue;
  state: IssueState | undefined;
}) {
  const { slug, identifier } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const navigate = useNavigate();

  return (
    <Card
      className="cursor-pointer transition-colors hover:bg-accent"
      onClick={() =>
        navigate(`/w/${slug}/p/${identifier}/i/${issue.id}`)
      }
    >
      <CardHeader className="space-y-2 py-4">
        <div className="flex items-center gap-2">
          <Badge variant="outline" className="font-mono">
            {issue.display_id}
          </Badge>
          {state && <StateBadge state={state} />}
          <span className="ml-auto text-xs text-muted-foreground">
            {priorityLabel(issue.priority)}
          </span>
        </div>
        <CardTitle className="text-base font-medium">{issue.name}</CardTitle>
        {(issue.labels.length > 0 || issue.assignees.length > 0) && (
          <div className="flex flex-wrap items-center gap-1.5">
            {issue.labels.map((l) => (
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
            {issue.assignees.map((a) => (
              <Badge key={a.id} variant="outline" className="text-xs font-normal">
                {a.name}
              </Badge>
            ))}
          </div>
        )}
      </CardHeader>
    </Card>
  );
}
