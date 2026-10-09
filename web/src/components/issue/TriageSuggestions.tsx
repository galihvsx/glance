import { useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, Sparkles } from "lucide-react";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Badge } from "../ui/badge";
import {
  isAINotConfigured,
  isAIProviderFailure,
  triageIssue,
  useAIAvailable,
  type AITriageResult,
} from "../../lib/ai";
import { tiptapText } from "../../lib/tiptap";
import {
  priorityLabel,
  type Issue,
  type IssueState,
  type Label as ProjectLabel,
} from "../../lib/types";
import { planTriageApply } from "./triagePlan";
import { AI_NOT_CONFIGURED_TOOLTIP } from "./DraftWithAI";

type TaxonomyIssue = Pick<
  Issue,
  "name" | "description" | "priority" | "state_id" | "labels"
>;

/**
 * "Triage suggestions" card for IssueDetail (C5T3).
 *
 * Fetches the AI triage suggestion and lets the user apply each part with
 * one click: priority, labels (each missing label added), and state.
 * Names that don't match the project's real taxonomy are shown greyed
 * out and never applied — no guessing.
 *
 * - 503 ai_not_configured → Suggest button disabled with the honest
 *   tooltip (shared with DraftWithAI).
 * - 502 provider failure → inline error in the card, Suggest stays.
 */
export default function TriageSuggestions({
  slug,
  identifier,
  issue,
  states,
  labels,
  onPatch,
  onAddLabel,
  disabled,
}: {
  slug: string;
  identifier: string;
  issue: TaxonomyIssue;
  states: IssueState[];
  labels: ProjectLabel[];
  onPatch: (patch: { priority?: number; state_id?: string }) => void;
  onAddLabel: (labelId: string) => void;
  disabled?: boolean;
}) {
  const queryClient = useQueryClient();
  const availability = useAIAvailable();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<AITriageResult | null>(null);

  const plan = useMemo(
    () =>
      result
        ? planTriageApply(result, issue, states, labels)
        : null,
    [result, issue, states, labels],
  );

  const notConfigured = availability.data === false;

  async function onSuggest() {
    if (!issue.name.trim()) {
      setError("The issue needs a title before triage.");
      return;
    }
    setPending(true);
    setError(null);
    try {
      const res = await triageIssue(
        slug,
        identifier,
        issue.name.trim(),
        tiptapText(issue.description),
      );
      setResult(res);
    } catch (e) {
      if (isAINotConfigured(e)) {
        queryClient.setQueryData(["ai-available"], false);
        setError("AI is not configured by the administrator.");
      } else if (isAIProviderFailure(e)) {
        setError("The AI provider failed. Try again in a moment.");
      } else {
        setError(e instanceof Error ? e.message : "Triage failed.");
      }
    } finally {
      setPending(false);
    }
  }

  const suggestedStateName = plan?.stateId
    ? states.find((s) => s.id === plan.stateId)?.name
    : plan?.unmatchedStateName;

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-3">
        <CardTitle className="flex items-center gap-1.5 text-base">
          <Sparkles className="h-4 w-4" aria-hidden />
          Triage suggestions
        </CardTitle>
        {notConfigured ? (
          <span title={AI_NOT_CONFIGURED_TOOLTIP}>
            <Button
              variant="outline"
              size="sm"
              disabled
              className="gap-1.5"
            >
              Suggest
            </Button>
          </span>
        ) : (
          <Button
            variant="outline"
            size="sm"
            className="gap-1.5"
            onClick={onSuggest}
            disabled={pending}
            title={pending ? undefined : "Suggest priority, labels and state"}
          >
            {pending ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
            ) : (
              <Sparkles className="h-3.5 w-3.5" aria-hidden />
            )}
            {pending ? "Suggesting…" : "Suggest"}
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-3">
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        {!result && !error && (
          <p className="text-sm text-muted-foreground">
            AI suggests a priority, labels and a state for this issue. You
            apply each one yourself — nothing changes automatically.
          </p>
        )}
        {plan && (
          <ul className="space-y-3">
            <li className="flex items-center justify-between gap-2">
              <span className="text-sm">
                Priority:{" "}
                <Badge variant="secondary">
                  {priorityLabel(plan.priority)}
                </Badge>
              </span>
              <Button
                size="sm"
                variant="outline"
                disabled={disabled || plan.priority === issue.priority}
                onClick={() => onPatch({ priority: plan.priority })}
              >
                {plan.priority === issue.priority
                  ? "Already set"
                  : "Apply"}
              </Button>
            </li>
            <li className="flex items-start justify-between gap-2">
              <span className="text-sm">
                State:{" "}
                {plan.stateId ? (
                  <Badge variant="secondary">{suggestedStateName}</Badge>
                ) : (
                  <span className="text-muted-foreground">
                    {plan.unmatchedStateName
                      ? `“${plan.unmatchedStateName}” — not a project state`
                      : "no suggestion"}
                  </span>
                )}
              </span>
              {plan.stateId && (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={disabled || plan.stateId === issue.state_id}
                  onClick={() => onPatch({ state_id: plan.stateId })}
                >
                  {plan.stateId === issue.state_id
                    ? "Already set"
                    : "Apply"}
                </Button>
              )}
            </li>
            <li className="flex items-start justify-between gap-2">
              <span className="text-sm">
                Labels:{" "}
                {plan.labelIdsToAdd.length === 0 &&
                plan.unmatchedLabelNames.length === 0 ? (
                  <span className="text-muted-foreground">no suggestion</span>
                ) : (
                  <span className="inline-flex flex-wrap gap-1 align-middle">
                    {plan.labelIdsToAdd.map((id) => {
                      const l = labels.find((x) => x.id === id);
                      return (
                        <Badge key={id} variant="secondary" className="gap-1">
                          <span
                            className="h-2 w-2 rounded-full"
                            style={{ backgroundColor: l?.color ?? "#888888" }}
                            aria-hidden
                          />
                          {l?.name ?? id}
                        </Badge>
                      );
                    })}
                    {plan.unmatchedLabelNames.map((n) => (
                      <Badge
                        key={n}
                        variant="outline"
                        className="text-muted-foreground"
                        title="No project label matches this name"
                      >
                        {n}
                      </Badge>
                    ))}
                  </span>
                )}
              </span>
              {plan.labelIdsToAdd.length > 0 && (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={disabled}
                  onClick={() =>
                    plan.labelIdsToAdd.forEach((id) => onAddLabel(id))
                  }
                >
                  Apply
                </Button>
              )}
            </li>
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
