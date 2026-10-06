-- 000014_outbox_delivering: add the 'delivering' claim state to the outbox.
--
-- The webhook dispatcher (Task 26 fix round 1) claims due rows, marks them
-- 'delivering' with a lease (next_retry_at), and COMMITS the claim tx
-- before performing slow HTTP deliveries outside any transaction. Each
-- outcome is then recorded with a short single-statement write. Without
-- a durable claim marker, a second dispatcher pass (another tick, or
-- another instance) could re-claim rows whose claim tx already committed
-- but whose outcomes aren't recorded yet — hence the new status.
--
-- 'delivering' rows whose lease (next_retry_at) expires are reclaimed by
-- a later pass (crashed-worker recovery); deliveries stay at-least-once.
ALTER TABLE outbox DROP CONSTRAINT outbox_status_check;
ALTER TABLE outbox ADD CONSTRAINT outbox_status_check
    CHECK (status IN ('pending', 'delivering', 'done', 'failed'));
