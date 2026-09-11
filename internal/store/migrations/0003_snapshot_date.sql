-- When the export was generated, as opposed to when it happened to be
-- uploaded. Execution order follows this, so an old export can be added after a
-- newer one and still land in its rightful place in the history.
ALTER TABLE uploads ADD COLUMN snapshot_taken_at INTEGER;

-- Where that date came from, so the UI can show how much to trust it and a
-- wrong guess can be recognised.
ALTER TABLE uploads ADD COLUMN snapshot_source TEXT NOT NULL DEFAULT '';

-- Existing executions keep exactly the order they already had: before this
-- column existed, processing order was the only order.
UPDATE uploads
SET snapshot_taken_at = COALESCE(processed_at, uploaded_at),
    snapshot_source   = 'processing order'
WHERE snapshot_taken_at IS NULL;

-- A date supplied at upload time, held until the worker processes the file.
ALTER TABLE uploads ADD COLUMN snapshot_date_override INTEGER;

CREATE INDEX idx_uploads_account_snapshot ON uploads (account_id, snapshot_taken_at);
