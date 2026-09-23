-- +goose Up
UPDATE jd_ability_level_jobs job
SET status='queued',attempts=0,next_attempt_at=NOW(),lease_token=NULL,heartbeat_at=NULL,
    lease_expires_at=NULL,last_error=NULL,completed_at=NULL,updated_at=NOW()
FROM job_descriptions jd
WHERE jd.id=job.job_description_id
  AND jd.status='included' AND jd.validation_status='valid'
  AND COALESCE(job.prompt_version,'')<>'jd-ability-level-v3';

-- Keep the previous assessments visible until the v2 grading result replaces
-- them atomically. A failed regrade therefore never removes usable levels.

-- +goose Down
-- Grading results are append-safe and should not be rolled back to an older
-- prompt automatically.
