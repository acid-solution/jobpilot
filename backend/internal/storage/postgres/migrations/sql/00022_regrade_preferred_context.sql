-- +goose Up
UPDATE jd_ability_level_jobs job
SET status='queued',attempts=0,next_attempt_at=NOW(),lease_token=NULL,heartbeat_at=NULL,
    lease_expires_at=NULL,last_error=NULL,completed_at=NULL,updated_at=NOW()
FROM job_descriptions jd
WHERE jd.id=job.job_description_id
  AND jd.status='included' AND jd.validation_status='valid'
  AND COALESCE(job.prompt_version,'')<>'jd-ability-level-v3';

-- +goose Down
-- The previous result is intentionally not restored automatically.
