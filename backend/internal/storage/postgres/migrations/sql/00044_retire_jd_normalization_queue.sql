-- +goose Up
-- Historical records are retained; no migration may trigger a paid reparse.
DROP TRIGGER IF EXISTS jd_normalization_changes ON job_descriptions;
DROP FUNCTION IF EXISTS enqueue_jd_normalization();
UPDATE jd_normalization_jobs SET status='failed',last_error='retired_use_jd_analysis',
 lease_token=NULL,heartbeat_at=NULL,lease_expires_at=NULL,updated_at=NOW()
 WHERE status IN ('queued','running');

-- +goose Down
-- Restore schema compatibility only. Archived jobs are not automatically replayed.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION enqueue_jd_normalization() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed BOOLEAN;
BEGIN
    changed := TG_OP='INSERT';
    IF TG_OP='UPDATE' THEN
        changed := NEW.raw_text IS DISTINCT FROM OLD.raw_text
            OR NEW.analysis_prompt_version IS DISTINCT FROM OLD.analysis_prompt_version
            OR NEW.normalization_prompt_version IS DISTINCT FROM OLD.normalization_prompt_version
            OR NEW.validation_status IS DISTINCT FROM OLD.validation_status;
    END IF;
    IF NEW.analysis_prompt_version<>'jd-source-stages-v15' AND NEW.validation_status='valid' AND NEW.normalization_prompt_version<>'jd-vector-candidates-v12'
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
