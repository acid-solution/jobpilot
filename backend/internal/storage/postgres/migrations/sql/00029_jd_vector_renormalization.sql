-- +goose Up
ALTER TABLE job_descriptions ADD COLUMN normalization_prompt_version VARCHAR(80) NOT NULL DEFAULT '';

-- An earlier rollout queued a full JD reparse for historical normalization.
-- Restore those jobs to their previous successful state and leave visible data intact.
UPDATE analysis_jobs job SET status='succeeded',preserve_previous_result=FALSE,
    lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,locked_at=NULL,last_error=NULL,updated_at=NOW()
FROM job_descriptions jd
WHERE job.job_description_id=jd.id AND job.job_type='jd_analysis'
  AND job.preserve_previous_result=TRUE AND jd.validation_status='valid';

CREATE TABLE jd_normalization_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    job_description_id UUID NOT NULL UNIQUE REFERENCES job_descriptions(id) ON DELETE CASCADE,
    source_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued' CHECK(status IN ('queued','running','succeeded','failed')),
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lease_token UUID,
    heartbeat_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX jd_normalization_claim ON jd_normalization_jobs(next_attempt_at,created_at) WHERE status='queued';

INSERT INTO jd_normalization_jobs(user_id,job_description_id,source_hash)
SELECT jd.user_id,jd.id,encode(digest(jd.raw_text,'sha256'),'hex')
FROM job_descriptions jd
WHERE jd.validation_status='valid' AND jd.normalization_prompt_version<>'jd-vector-candidates-v12'
  AND EXISTS(SELECT 1 FROM job_description_ability_requirements r WHERE r.job_description_id=jd.id);

-- +goose StatementBegin
CREATE FUNCTION enqueue_jd_normalization() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed BOOLEAN;
BEGIN
    changed := TG_OP='INSERT';
    IF TG_OP='UPDATE' THEN
        changed := NEW.raw_text IS DISTINCT FROM OLD.raw_text
            OR NEW.analysis_prompt_version IS DISTINCT FROM OLD.analysis_prompt_version
            OR NEW.normalization_prompt_version IS DISTINCT FROM OLD.normalization_prompt_version
            OR NEW.validation_status IS DISTINCT FROM OLD.validation_status;
    END IF;
    IF NEW.validation_status='valid' AND NEW.normalization_prompt_version<>'jd-vector-candidates-v12'
       AND EXISTS(SELECT 1 FROM job_description_ability_requirements r WHERE r.job_description_id=NEW.id)
       AND changed THEN
        INSERT INTO jd_normalization_jobs(user_id,job_description_id,source_hash)
        VALUES(NEW.user_id,NEW.id,encode(digest(NEW.raw_text,'sha256'),'hex'))
        ON CONFLICT(job_description_id) DO UPDATE SET
            source_hash=EXCLUDED.source_hash,status='queued',attempts=0,next_attempt_at=NOW(),
            lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,last_error='',updated_at=NOW();
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER jd_normalization_changes AFTER INSERT OR UPDATE OF raw_text,validation_status,analysis_prompt_version,normalization_prompt_version ON job_descriptions
FOR EACH ROW EXECUTE FUNCTION enqueue_jd_normalization();

-- +goose Down
DROP TRIGGER jd_normalization_changes ON job_descriptions;
DROP FUNCTION enqueue_jd_normalization();
DROP TABLE jd_normalization_jobs;
ALTER TABLE job_descriptions DROP COLUMN normalization_prompt_version;
