// AutomationPickers (C16T4): trigger filter fields + action parameter
// fields for the automation rule builder. Presentational only — data
// comes in as props, so unit tests need no API. All inputs use the
// existing shadcn primitives.

import {
  ACTION_LABELS,
  DEFAULT_STALE_DAYS,
  DEFAULT_WINDOW_HOURS,
  PRIORITY_OPTIONS,
  SCHEDULED_TRIGGER_TYPES,
  TRIGGER_LABELS,
  type ActionType,
  type DraftAction,
  type TriggerDraft,
  type TriggerType,
} from "../../lib/automationDraft";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Textarea } from "../ui/textarea";

const selectClass =
  "rounded-md border border-input bg-background px-2 py-1.5 text-sm";

export interface TriggerLookups {
  states: { id: string; name: string }[];
  labels: { id: string; name: string }[];
}

export interface ActionLookups {
  members: { id: string; name?: string | null; email: string; role?: number }[];
  labels: { id: string; name: string }[];
  states: { id: string; name: string }[];
  cycles: { id: string; name: string }[];
  modules: { id: string; name: string }[];
  /** Estimate scales with their selectable points. */
  estimateScales: {
    id: string;
    name: string;
    points: { id: string; key: string }[];
  }[];
}

/** "When" selector + the filter inputs for the chosen trigger type. */
export function TriggerFields({
  trigger,
  onChange,
  lookups,
}: {
  trigger: TriggerDraft;
  onChange: (patch: Partial<TriggerDraft>) => void;
  lookups: TriggerLookups;
}) {
  const isScheduled = SCHEDULED_TRIGGER_TYPES.includes(trigger.type);
  return (
    <div className="space-y-3">
      <div>
        <Label htmlFor="auto-trigger">When</Label>
        <select
          id="auto-trigger"
          className={`${selectClass} w-full`}
          value={trigger.type}
          onChange={(e) =>
            onChange({ type: e.target.value as TriggerType })
          }
        >
          <optgroup label="On an event">
            {(
              [
                "issue.state_changed",
                "issue.created",
                "issue.assigned",
                "issue.unassigned",
                "issue.labels_changed",
                "issue.priority_changed",
                "issue.due_date_changed",
                "issue.estimate_changed",
                "issue.comment_added",
              ] as TriggerType[]
            ).map((t) => (
              <option key={t} value={t}>
                {TRIGGER_LABELS[t]}
              </option>
            ))}
          </optgroup>
          <optgroup label="On a schedule">
            {(
              [
                "issue.due_soon",
                "issue.overdue",
                "issue.stale",
                "cycle.ending_soon",
              ] as TriggerType[]
            ).map((t) => (
              <option key={t} value={t}>
                {TRIGGER_LABELS[t]}
              </option>
            ))}
          </optgroup>
        </select>
        {isScheduled && (
          <p className="mt-1 text-xs text-muted-foreground">
            Scheduled triggers are checked by a ticker, not by issue
            events, and fire at most once per issue per day.
          </p>
        )}
        {trigger.type === "issue.created" && (
          <p className="mt-1 text-xs text-muted-foreground">
            Fires on every issue creation. No filters in this version.
          </p>
        )}
      </div>

      {trigger.type === "issue.state_changed" && (
        <div className="grid grid-cols-2 gap-3">
          <div>
            <Label htmlFor="auto-from">From state</Label>
            <select
              id="auto-from"
              className={`${selectClass} w-full`}
              value={trigger.fromState}
              onChange={(e) => onChange({ fromState: e.target.value })}
            >
              <option value="">Any state</option>
              {lookups.states.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
          <div>
            <Label htmlFor="auto-to">To state</Label>
            <select
              id="auto-to"
              className={`${selectClass} w-full`}
              value={trigger.toState}
              onChange={(e) => onChange({ toState: e.target.value })}
            >
              <option value="">Any state</option>
              {lookups.states.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
        </div>
      )}

      {trigger.type === "issue.labels_changed" && (
        <div>
          <Label htmlFor="auto-labels">
            Only for these labels{" "}
            <span className="text-muted-foreground">(empty = any label)</span>
          </Label>
          <select
            id="auto-labels"
            multiple
            size={Math.min(6, Math.max(3, lookups.labels.length))}
            className={`${selectClass} w-full`}
            value={trigger.labelIds}
            onChange={(e) =>
              onChange({
                labelIds: Array.from(e.target.selectedOptions).map(
                  (o) => o.value,
                ),
              })
            }
          >
            {lookups.labels.map((l) => (
              <option key={l.id} value={l.id}>
                {l.name}
              </option>
            ))}
          </select>
          <p className="mt-1 text-xs text-muted-foreground">
            Hold Ctrl/Cmd to select several. The rule fires when one of
            the selected labels is added to or removed from an issue.
          </p>
        </div>
      )}

      {trigger.type === "issue.priority_changed" && (
        <div className="grid grid-cols-2 gap-3">
          <div>
            <Label htmlFor="auto-from-pri">From priority</Label>
            <select
              id="auto-from-pri"
              className={`${selectClass} w-full`}
              value={trigger.fromPriority}
              onChange={(e) => onChange({ fromPriority: e.target.value })}
            >
              <option value="">Any priority</option>
              {PRIORITY_OPTIONS.map((p) => (
                <option key={p.value} value={p.value}>
                  {p.label}
                </option>
              ))}
            </select>
          </div>
          <div>
            <Label htmlFor="auto-to-pri">To priority</Label>
            <select
              id="auto-to-pri"
              className={`${selectClass} w-full`}
              value={trigger.toPriority}
              onChange={(e) => onChange({ toPriority: e.target.value })}
            >
              <option value="">Any priority</option>
              {PRIORITY_OPTIONS.map((p) => (
                <option key={p.value} value={p.value}>
                  {p.label}
                </option>
              ))}
            </select>
          </div>
        </div>
      )}

      {trigger.type === "issue.due_soon" && (
        <div>
          <Label htmlFor="auto-window-hours">Due within (hours)</Label>
          <Input
            id="auto-window-hours"
            inputMode="numeric"
            placeholder={`Default: ${DEFAULT_WINDOW_HOURS}`}
            value={trigger.windowHours}
            onChange={(e) => onChange({ windowHours: e.target.value })}
          />
          <p className="mt-1 text-xs text-muted-foreground">
            Fire for open issues whose due date falls within this many
            hours from now. Leave empty for the default (
            {DEFAULT_WINDOW_HOURS}h).
          </p>
        </div>
      )}

      {trigger.type === "issue.stale" && (
        <div>
          <Label htmlFor="auto-stale-days">Untouched for (days)</Label>
          <Input
            id="auto-stale-days"
            inputMode="numeric"
            placeholder={`Default: ${DEFAULT_STALE_DAYS}`}
            value={trigger.staleDays}
            onChange={(e) => onChange({ staleDays: e.target.value })}
          />
          <p className="mt-1 text-xs text-muted-foreground">
            Fire for open issues with no update in this many days. Leave
            empty for the default ({DEFAULT_STALE_DAYS} days).
          </p>
        </div>
      )}
    </div>
  );
}

/** Parameter fields for one action in the builder. */
export function ActionFields({
  index,
  action,
  onChange,
  lookups,
}: {
  index: number;
  action: DraftAction;
  onChange: (patch: Partial<DraftAction>) => void;
  lookups: ActionLookups;
}) {
  const memberOptions = lookups.members.filter(
    (m) => m.role === undefined || m.role >= 15,
  );
  return (
    <div className="flex-1">
      {action.type === "assign" && (
        <UserSelect
          label={`Action ${index + 1} user`}
          value={action.user_id}
          onChange={(v) => onChange({ user_id: v })}
          members={memberOptions}
        />
      )}
      {action.type === "add_label" && (
        <LabelSelect
          label={`Action ${index + 1} label`}
          value={action.label_id}
          onChange={(v) => onChange({ label_id: v })}
          labels={lookups.labels}
          verb="add"
        />
      )}
      {action.type === "remove_label" && (
        <LabelSelect
          label={`Action ${index + 1} label`}
          value={action.label_id}
          onChange={(v) => onChange({ label_id: v })}
          labels={lookups.labels}
          verb="remove"
        />
      )}
      {action.type === "add_comment" && (
        <Textarea
          aria-label={`Action ${index + 1} comment`}
          value={action.body}
          onChange={(e) => onChange({ body: e.target.value })}
          placeholder="Comment text…"
          rows={2}
          maxLength={10000}
        />
      )}
      {action.type === "set_priority" && (
        <select
          aria-label={`Action ${index + 1} priority`}
          className={`${selectClass} w-full`}
          value={action.priority}
          onChange={(e) => onChange({ priority: Number(e.target.value) })}
        >
          {PRIORITY_OPTIONS.map((p) => (
            <option key={p.value} value={p.value}>
              {p.label}
            </option>
          ))}
        </select>
      )}
      {action.type === "set_state" && (
        <select
          aria-label={`Action ${index + 1} state`}
          className={`${selectClass} w-full`}
          value={action.state_id}
          onChange={(e) => onChange({ state_id: e.target.value })}
        >
          <option value="">Select state…</option>
          {lookups.states.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
      )}
      {action.type === "unassign" && (
        <p className="text-sm text-muted-foreground">
          Clears every assignee on the issue. No further input.
        </p>
      )}
      {action.type === "set_estimate" && (
        <select
          aria-label={`Action ${index + 1} estimate`}
          className={`${selectClass} w-full`}
          value={action.estimate}
          onChange={(e) => onChange({ estimate: e.target.value })}
        >
          <option value="">Select estimate…</option>
          {lookups.estimateScales.map((scale) => (
            <optgroup key={scale.id} label={scale.name}>
              {scale.points.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.key}
                </option>
              ))}
            </optgroup>
          ))}
        </select>
      )}
      {action.type === "set_due_date" && (
        <div className="space-y-2">
          <div className="flex gap-2" role="radiogroup" aria-label={`Action ${index + 1} due date mode`}>
            {(
              [
                ["absolute", "Specific date"],
                ["relative", "Relative to when the rule fires"],
              ] as const
            ).map(([mode, text]) => (
              <label key={mode} className="flex items-center gap-1.5 text-sm">
                <input
                  type="radio"
                  name={`auto-duedate-mode-${index}`}
                  checked={action.dueDateMode === mode}
                  onChange={() => onChange({ dueDateMode: mode })}
                />
                {text}
              </label>
            ))}
          </div>
          {action.dueDateMode === "absolute" ? (
            <Input
              aria-label={`Action ${index + 1} due date`}
              type="date"
              value={action.dueDate}
              onChange={(e) => onChange({ dueDate: e.target.value })}
            />
          ) : (
            <div className="flex items-center gap-2">
              <Input
                aria-label={`Action ${index + 1} due date offset`}
                inputMode="numeric"
                placeholder="7"
                value={action.dueDateOffset}
                onChange={(e) => onChange({ dueDateOffset: e.target.value })}
                className="w-24"
              />
              <span className="text-sm text-muted-foreground">
                days after the rule fires
              </span>
            </div>
          )}
        </div>
      )}
      {action.type === "move_to_cycle" && (
        <select
          aria-label={`Action ${index + 1} cycle`}
          className={`${selectClass} w-full`}
          value={action.cycle_id}
          onChange={(e) => onChange({ cycle_id: e.target.value })}
        >
          <option value="">Select cycle…</option>
          {lookups.cycles.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
      )}
      {action.type === "move_to_module" && (
        <select
          aria-label={`Action ${index + 1} module`}
          className={`${selectClass} w-full`}
          value={action.module_id}
          onChange={(e) => onChange({ module_id: e.target.value })}
        >
          <option value="">Select module…</option>
          {lookups.modules.map((m) => (
            <option key={m.id} value={m.id}>
              {m.name}
            </option>
          ))}
        </select>
      )}
      {action.type === "add_watcher" && (
        <UserSelect
          label={`Action ${index + 1} watcher`}
          value={action.user_id}
          onChange={(v) => onChange({ user_id: v })}
          members={memberOptions}
        />
      )}
    </div>
  );
}

export function ActionTypeSelect({
  index,
  value,
  onChange,
}: {
  index: number;
  value: ActionType;
  onChange: (t: ActionType) => void;
}) {
  return (
    <select
      aria-label={`Action ${index + 1} type`}
      className={selectClass}
      value={value}
      onChange={(e) => onChange(e.target.value as ActionType)}
    >
      {(Object.keys(ACTION_LABELS) as ActionType[]).map((t) => (
        <option key={t} value={t}>
          {ACTION_LABELS[t]}
        </option>
      ))}
    </select>
  );
}

function UserSelect({
  label,
  value,
  onChange,
  members,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  members: { id: string; name?: string | null; email: string }[];
}) {
  return (
    <select
      aria-label={label}
      className={`${selectClass} w-full`}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    >
      <option value="">Select user…</option>
      {members.map((m) => (
        <option key={m.id} value={m.id}>
          {m.name || m.email}
        </option>
      ))}
    </select>
  );
}

function LabelSelect({
  label,
  value,
  onChange,
  labels,
  verb,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  labels: { id: string; name: string }[];
  verb: "add" | "remove";
}) {
  return (
    <select
      aria-label={label}
      className={`${selectClass} w-full`}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    >
      <option value="">{verb === "add" ? "Select label…" : "Select label to remove…"}</option>
      {labels.map((l) => (
        <option key={l.id} value={l.id}>
          {l.name}
        </option>
      ))}
    </select>
  );
}
