-- +goose Up
UPDATE jd_normalization_jobs job SET status='queued',attempts=0,
    next_attempt_at=NOW(),lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,
    last_error='model_not_configured',updated_at=NOW()
WHERE job.status='failed' AND job.last_error='normalization_failed'
  AND NOT EXISTS(SELECT 1 FROM model_configs config WHERE config.user_id=job.user_id AND config.provider='deepseek');

-- +goose Down
SELECT 1;
