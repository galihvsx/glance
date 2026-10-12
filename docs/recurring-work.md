# Recurring issue schedules

glance can create issues on a schedule — a **recurring schedule** is a
cron-like recipe scoped to one project. Every occurrence creates one
issue whose title and description are rendered from templates with the
run time. Plane has no native equivalent (no scheduled issue creation);
glance ships it free.

A background ticker fires due schedules automatically; the same engine
is available on demand via **run-now** (the manual "fire now" for
testing a rule).

## Creating a schedule

`POST /api/v1/workspaces/{slug}/projects/{identifier}/schedules`
(member 15+):

```json
{
  "name": "Weekly retro",
  "frequency": "weekly",
  "days_of_week": [5],
  "hour": 9,
  "starts_on": "2026-10-12",
  "title_template": "Retro {{date}}",
  "description_template": "What went well?\n\nRun at {{datetime}}",
  "priority": 2,
  "label_ids": ["<uuid>"],
  "assignee_ids": ["<uuid>"]
}
```

Required: `name`, `frequency`, `starts_on` (YYYY-MM-DD), `title_template`.
Optional: `active` (default `true`; `false` creates the schedule paused),
`interval` (default `1` — every N days/weeks/months), `hour` (default `9`),
`ends_on` (YYYY-MM-DD), `description_template`, `state_id`, `priority`
(0–4, default `0`), `label_ids`, `assignee_ids`.

## Cadence options

| frequency | required | behavior |
|---|---|---|
| `daily` | — | every `interval` days at `hour:00` |
| `weekly` | `days_of_week` (0=Sunday…6=Saturday, at least one) | every `interval` weeks, on those weekdays, at `hour:00` |
| `monthly` | `day_of_month` (1–31) | every `interval` months on that day at `hour:00`; day 31 in February lands on Feb 28/29 (clamped to the month length) |

Occurrences are anchored at `starts_on`. Changing the cadence
(frequency, interval, days, hour, starts, ends) recomputes `next_run_at`
from `max(starts_on, now)`; editing anything else (name, templates,
labels, …) leaves the ticker's schedule untouched.

A schedule whose `ends_on` already exhausts it — no future run left —
is rejected with a 400 at create/update time. After the last run, the
ticker **deactivates** the schedule (`active=false`, `run_count` and
`last_run_at` record what happened); it never silently deletes it.

## Template variables

- `{{date}}` → `YYYY-MM-DD` of the run time
- `{{datetime}}` → RFC3339 of the run time

Unknown `{{…}}` tokens are left as-is (deliberate — you'll see them, not
a silent empty string).

**UTC limitation (honest):** in v1 every schedule runs on UTC.
`hour` is 0–23 UTC; `starts_on`/`ends_on` are UTC dates. Per-user or
per-workspace timezones are a later cycle — if your team is in WITA,
`hour: 1` is 09:00 your time.

## Reading schedules

- `GET …/schedules` — the project's schedules, ordered by creation
  (any workspace member, guest 5+ included; never `null` — `[]` when empty).
- `GET …/schedules/{id}` — one schedule, including `next_run_at`,
  `last_run_at`, and `run_count`.

## Updating, pausing, deleting

`PATCH …/schedules/{id}` (member 15+) is partial, with **tri-state** for
the nullable fields (`day_of_month`, `ends_on`, `description_template`,
`state_id`): an absent key is untouched, explicit JSON `null` **clears**
it, a value sets it.

- Pause: `{"active": false}`. Resume: `{"active": true}` (the ticker
  picks up at the stored `next_run_at` — a past-due run fires once on
  resume, then the cadence continues; a paused schedule never replays a
  whole backlog of missed runs).
- `DELETE …/schedules/{id}` → 204. Already-created issues are untouched:
  the schedule owns no issue rows.

## Run-now

`POST …/schedules/{id}/run-now` (member 15+) fires the schedule's
**pending** occurrence immediately — at `next_run_at`, not at wall-clock
now — with **the calling user as the issue's actor** (the issue's
`created_by` and its `_created` activity row name you, exactly as if the
ticker had fired and attributed it). The response is
`{"issue": {…standard issue JSON…}, "schedule": {…updated schedule…}}`.

This is the way to test a rule before trusting the ticker: check the
rendered title, the labels, the assignees, then let the schedule run on
its own. An inactive schedule is a 400; an exhausted one (no
`next_run_at` left) is a 400.

## When references go stale, or the creator leaves

- **Stale references** (a label deleted after the schedule was saved, an
  assignee who left the workspace, a state that no longer exists): the
  schedule **deactivates itself** instead of creating a half-wrong issue.
  Edit the schedule to fix the reference and re-activate it.
- **`state_id` is resolved at run time**, not frozen at creation: a null
  `state_id` means "the project's backlog state whenever the issue is
  created". If you rename or restructure states, unset schedules keep
  following the backlog.
- **The creator leaving** changes nothing: `created_by` is `ON DELETE
  SET NULL`, and firing attributes the issue to the actor that fires it
  (the ticker's system actor for automatic runs, you for run-now). The
  schedule keeps running.

## Error codes

The standard error envelope applies. Schedule-specific: `404
schedule_not_found` for an unknown schedule id (including a schedule id
from another project — ids never leak across the tenancy boundary);
`400 bad_request` for validation failures (bad frequency, weekly without
days, monthly without a day, `ends_on` before `starts_on`, exhausted at
creation, empty patch). Guests (role 5) may read; every write — create,
update, delete, run-now — needs member (15+).
