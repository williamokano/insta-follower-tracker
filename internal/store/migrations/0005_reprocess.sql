-- Marks an execution to be read again from the file kept on disk.
--
-- Reprocessing exists because the service learns to read more than it could
-- before: lists that were skipped when an export was first uploaded, or an
-- export date that could not be established then. The raw file is retained, so
-- that knowledge can be applied without asking for the upload again.
ALTER TABLE uploads ADD COLUMN reprocess_at INTEGER;

CREATE INDEX idx_uploads_reprocess ON uploads (reprocess_at) WHERE reprocess_at IS NOT NULL;
