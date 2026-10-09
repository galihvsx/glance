import { useNavigate, useParams } from "react-router-dom";
import { Calendar, Copy, MoreVertical } from "lucide-react";
import type { Issue, IssueState } from "../../lib/types";
import { priorityLabel } from "../../lib/types";
import { Badge } from "../ui/badge";
import { CardHeader, CardTitle } from "../ui/card";
import { ActionCard } from "../ui/action-card";
import { Checkbox } from "../ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import StateBadge from "./StateBadge";
import { useCloneIssue } from "./useCloneIssue";
import {
  formatIssueDateRange,
  type DisplayFields,
} from "./useDisplaySettings";

/** One row of the issue list: display_id badge, title, state, labels,
 *  priority, assignees. Click opens the issue — via `onOpen` when given
 *  (peek drawer), otherwise navigates to the detail page. The `fields`
 *  prop (from the Display panel) toggles which attributes render.
 *  `selected`/`onToggleSelect` render a multi-select checkbox (C5T8 bulk
 *  operations); the click is stopped so it never opens the issue.
 *  `slug`/`identifier` override the route params for the clone action —
 *  cards rendered outside the project route (e.g. Home's cross-project
 *  "your work" list) must pass the issue's own project coordinates. */
export default function IssueCard({
  issue,
  state,
  onOpen,
  fields,
  selected,
  onToggleSelect,
  slug: slugProp,
  identifier: identifierProp,
}: {
  issue: Issue;
  state: IssueState | undefined;
  onOpen?: (issue: Issue) => void;
  fields: DisplayFields;
  selected?: boolean;
  onToggleSelect?: (checked: boolean) => void;
  slug?: string;
  identifier?: string;
}) {
  const params = useParams<{
    slug: string;
    identifier: string;
  }>();
  const slug = slugProp ?? params.slug;
  const identifier = identifierProp ?? params.identifier;
  const navigate = useNavigate();
  const dateRange = formatIssueDateRange(issue.start_date, issue.target_date);
  // C8T4: row-menu clone — POST .../issues/{uuid}/clone, navigate to the
  // new issue. The trigger lives inside the link-card, so clicks are
  // stopped exactly like the multi-select checkbox below.
  const {
    cloneIssue,
    cloning,
    error: cloneError,
  } = useCloneIssue(slug ?? "", identifier ?? "");

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
          {onToggleSelect && (
            // stopPropagation: the card itself is a link — checking the
            // box must not open the issue.
            <span
              className="flex items-center"
              onClick={(e) => e.stopPropagation()}
              onKeyDown={(e) => e.stopPropagation()}
            >
              <Checkbox
                checked={selected ?? false}
                onCheckedChange={(v) => onToggleSelect(v === true)}
                aria-label={`Select issue ${issue.display_id}`}
              />
            </span>
          )}
          <Badge variant="outline" className="font-mono">
            {issue.display_id}
          </Badge>
          {fields.state && state && <StateBadge state={state} />}
          {fields.priority && (
            <span className="text-xs text-muted-foreground">
              {priorityLabel(issue.priority)}
            </span>
          )}
          {/* C8T4: row menu. stopPropagation: the card itself is a link —
              opening the menu must not open the issue. */}
          <span
            className="ml-auto flex items-center"
            onClick={(e) => e.stopPropagation()}
            onKeyDown={(e) => e.stopPropagation()}
          >
            <DropdownMenu>
              <DropdownMenuTrigger
                className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
                title="Issue actions"
                aria-label={`Actions for issue ${issue.display_id}`}
              >
                <MoreVertical className="h-4 w-4" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem
                  onClick={() => void cloneIssue(issue.id)}
                  disabled={cloning}
                >
                  <Copy className="h-3.5 w-3.5" />
                  {cloning ? "Cloning…" : "Clone issue"}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </span>
        </div>
        {cloneError && (
          <p className="text-xs text-destructive" role="alert">
            {cloneError}
          </p>
        )}
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
