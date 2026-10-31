-- 000079_membership_cascade: repair dangling teamspace memberships (C93T0).
--
-- The C90 quirk: RemoveMember (internal/service/workspace.go) cleared
-- Lead designations when a workspace member was removed but left the
-- removed user's teamspace_members rows in place, so the roster still
-- listed someone who was no longer a workspace member. The C93T0 code
-- fix deletes those rows in the same transaction going forward; this
-- migration repairs rows that already dangle: a teamspace_members row
-- whose user holds no workspace_members row in that teamspace's
-- workspace.
--
-- Honest framing (do not over-claim): a dangling row alone could NOT
-- regain project access — EffectiveProjectAccess requires a
-- workspace_members row first (resolveWorkspaceForActorRef) — so this
-- is a roster-integrity + defense-in-depth repair, not a fix for a
-- live privilege-escalation hole. Rows for users who ARE still
-- workspace members are untouched.
--
-- Idempotent: safe to re-run (a second apply deletes zero rows).
DELETE FROM teamspace_members tm
WHERE NOT EXISTS (
    SELECT 1
    FROM teamspaces t
    JOIN workspace_members wm ON wm.workspace_id = t.workspace_id
    WHERE t.id = tm.teamspace_id
      AND wm.user_id = tm.user_id
);
