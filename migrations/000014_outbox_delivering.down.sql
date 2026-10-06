-- 000014_outbox_delivering (down): restore the three-state outbox status.
-- In-flight 'delivering' rows are reset to 'pending' so the down-migration
-- never violates the restored CHECK constraint.
UPDATE outbox SET status = 'pending' WHERE status = 'delivering';
ALTER TABLE outbox DROP CONSTRAINT outbox_status_check;
ALTER TABLE outbox ADD CONSTRAINT outbox_status_check
    CHECK (status IN ('pending', 'done', 'failed'));
