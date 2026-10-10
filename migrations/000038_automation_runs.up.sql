-- 000038_automation_runs: per-firing run log for workflow automation
-- rules (C12T1). One row is written for every rule firing, inside the
-- same transaction as the state change that triggered it (see
-- runAutomationRulesTx in internal/service/automation.go), so the log
-- can never disagree with history: "why did this issue change?"
--
-- actions (JSONB): ordered array of {"type": "<assign|add_label|add_comment>",
-- "ok": bool, "error": "<text>"?} — the per-action outcome. A failed
-- action records ok:false with the error text while the state change
-- still commits (the automation ethos: a broken rule never blocks the
-- user's move). The run row is still written on partial failure.
--
-- issue_id has no FK on purpose: issues are soft-deleted (deleted_at),
-- never hard-deleted, so the sequence_id needed to derive the display
-- id survives. rule_id cascades: deleting a rule erases its history.
--
-- No retention pruning in v1 (documented honestly): the table grows one
-- row per firing. A future cycle can add a retention window; the
-- idx_automation_runs_rule index already serves a
-- DELETE ... WHERE fired_at < cutoff.
--
-- IF NOT EXISTS / IF NOT EXISTS index so the migration is re-runnable.
CREATE TABLE IF NOT EXISTS automation_runs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id      UUID NOT NULL REFERENCES automation_rules (id) ON DELETE CASCADE,
    issue_id     UUID NOT NULL,
    trigger_type TEXT NOT NULL,
    fired_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    actions      JSONB NOT NULL DEFAULT '[]'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_automation_runs_rule
    ON automation_runs (rule_id, fired_at DESC);

CREATE INDEX IF NOT EXISTS idx_automation_runs_issue
    ON automation_runs (issue_id, fired_at DESC);
