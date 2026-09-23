-- +goose Up
-- Earlier versions could finish the basic DeepSeek extraction before job
-- classification and normalized abilities existed. Requeue only those
-- successful jobs that do not have a classification yet so the new pipeline
-- can finish them without touching already classified records.
UPDATE analysis_jobs AS job
SET status = 'queued',
    attempts = 0,
    next_attempt_at = NOW(),
    locked_at = NULL,
    lease_token = NULL,
    heartbeat_at = NULL,
    lease_expires_at = NULL,
    last_error = NULL,
    updated_at = NOW()
FROM job_descriptions AS jd
WHERE job.job_description_id = jd.id
  AND job.job_type = 'jd_analysis'
  AND job.status = 'succeeded'
  AND jd.validation_status IN ('pending', 'valid')
  AND NOT EXISTS (
      SELECT 1
      FROM job_description_classifications AS classification
      WHERE classification.job_description_id = jd.id
  );

-- +goose Down
-- Re-running the old partial state cannot be reconstructed safely.
SELECT 1;
